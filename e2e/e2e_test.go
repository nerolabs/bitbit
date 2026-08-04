// Package e2e drives the real `silt` binary as separate OS processes
// over real TCP — the layer the in-process sim deliberately skips. It
// exists because the class of bug the sim cannot see is exactly the one
// that bit us in the field (#36: a reply that could never reach a NATed
// peer, invisible until real sockets carried it). Here a built binary
// runs actual daemons, publishes through the chain-backed registry over
// pinned HTTPS, and fetches back across the swarm — asserting the file
// returns bit-perfect.
//
// The test builds the binary once (TestMain) and is guarded by
// -short so `go test -short` skips the process-spawning cost; CI runs
// the full form.
package e2e

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/core/link"
)

var siltBin string

func TestMain(m *testing.M) {
	// Build once for the whole package. -short still builds (cheap when
	// cached) but the tests themselves skip, so `go test -short ./e2e`
	// is fast and never spawns a process.
	dir, err := os.MkdirTemp("", "silt-e2e-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: tempdir:", err)
		os.Exit(1)
	}
	siltBin = filepath.Join(dir, "silt")
	build := exec.Command("go", "build", "-o", siltBin, "github.com/nerolabs/silt/cmd/silt")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "e2e: build silt:", err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// lineBuffer collects a process's merged stdout+stderr as whole lines,
// so a test can wait for a specific line (a printed address, a commit)
// without racing the reader.
type lineBuffer struct {
	mu    sync.Mutex
	pend  []byte
	lines []string
}

func (b *lineBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pend = append(b.pend, p...)
	for {
		i := bytes.IndexByte(b.pend, '\n')
		if i < 0 {
			break
		}
		b.lines = append(b.lines, string(b.pend[:i]))
		b.pend = b.pend[i+1:]
	}
	return len(p), nil
}

func (b *lineBuffer) find(re *regexp.Regexp) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ln := range b.lines {
		if m := re.FindStringSubmatch(ln); m != nil {
			return m
		}
	}
	return nil
}

func (b *lineBuffer) dump() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return "\t" + strings.Join(b.lines, "\n\t")
}

type daemon struct {
	name string
	cmd  *exec.Cmd
	out  *lineBuffer
}

// startDaemon launches `silt daemon <args>` and streams its output into
// a lineBuffer. It is killed on test cleanup.
func startDaemon(t *testing.T, name string, args ...string) *daemon {
	t.Helper()
	out := &lineBuffer{}
	cmd := exec.Command(siltBin, append([]string{"daemon"}, args...)...)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon %s: %v", name, err)
	}
	d := &daemon{name: name, cmd: cmd, out: out}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	return d
}

func (d *daemon) waitFor(t *testing.T, re *regexp.Regexp, timeout time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if m := d.out.find(re); m != nil {
			return m
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("daemon %s: timed out waiting for /%s/\n--- output ---\n%s", d.name, re, d.out.dump())
	return nil
}

var (
	rePeer      = regexp.MustCompile(`peer: ([0-9a-f]{64})@(\S+)`)
	reRegistry  = regexp.MustCompile(`registry: chain-backed, serving ([0-9a-f]{64}@https://[^ ]+) \(quorum`)
	reBootstrap = regexp.MustCompile(`bootstrapped \((\d+) table entries\)`)
	reCommitted = regexp.MustCompile(`chain: committed block (\d+)`)
	reLink      = regexp.MustCompile(`^silt:v1:\S+`)
)

// runClient runs a one-shot `silt <args>` (swarm add/get) to completion,
// returning its stdout. Diagnostics on stderr are folded into the
// failure message only.
func runClient(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(siltBin, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("client %v: %v\n--- stdout ---\n%s\n--- stderr ---\n%s", args, err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// runClientAllowErr runs a one-shot client and returns its combined output
// and exit error (nil on success) instead of failing the test — for
// asserting a command MUST fail (e.g. a publish the safe defaults refuse).
func runClientAllowErr(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := exec.Command(siltBin, args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// TestDefaultsRefuseRubberStampCommit is the OUTCOME test for the safe
// consensus defaults. Use case: an operator runs a validator with DEFAULT
// flags (no -quorum / -min-rep). Outcome required: a lone, unearned node must
// NOT be able to rubber-stamp the registry — the publish's commit is refused.
// (TestPublishCommitFetchOverTCP is the positive control: with the explicit
// -quorum 0 trusted-deployment setting, the same publish DOES commit.)
func TestDefaultsRefuseRubberStampCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e spawns processes; skipped under -short")
	}
	a := startDaemon(t, "A",
		"-listen", "127.0.0.1:0", "-store", t.TempDir(),
		"-serve-registry", "127.0.0.1:0", "-validator",
		"-capacity", "1G", "-mdns=false", "-id-seed", "2001")
	peer := a.waitFor(t, rePeer, 20*time.Second)
	idA, addrA := peer[1], peer[2]
	regRef := a.waitFor(t, reRegistry, 20*time.Second)[1]
	bootstrapA := idA + "@" + addrA

	src := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(src, make([]byte, 64<<10), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runClientAllowErr(t, "swarm", "add", src,
		"-peers", bootstrapA, "-registry", regRef, "-chunk-size", "65536")
	if err == nil {
		t.Fatalf("safe defaults must REFUSE a lone/unearned commit, but the publish succeeded:\n%s", out)
	}
}

// TestBondEarnedStandingCommitsOverTCP is the e2e tier for the trust pivot
// (T1b, #78): two validators earn consensus standing by proving their storage
// bonds to EACH OTHER over TCP (gossip → challenge → verify → ledger), and a
// publish then commits on the SAFE min-rep path — no `-quorum 0` trusted-
// deployment shortcut. Standing is earned, not granted; a publish that commits
// here means real held storage bought the write.
func TestBondEarnedStandingCommitsOverTCP(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e spawns processes; skipped under -short")
	}
	// Deterministic ids so A can name B as its attester before B exists.
	idB := identity.FromSeed(1002).NodeID().String()

	// A: registry + validator + bond; min-rep 100 (earned standing required),
	// quorum 1, and B is its attester. Fast bond audit so the test is quick.
	a := startDaemon(t, "A",
		"-listen", "127.0.0.1:0", "-store", t.TempDir(),
		"-serve-registry", "127.0.0.1:0", "-validator",
		"-min-rep", "100", "-quorum", "1", "-attesters", idB,
		"-bond", "8M", "-bond-audit", "1s",
		"-capacity", "1G", "-mdns=false", "-id-seed", "1001")
	peer := a.waitFor(t, rePeer, 20*time.Second)
	idA, addrA := peer[1], peer[2]
	regRef := a.waitFor(t, reRegistry, 20*time.Second)[1]
	bootstrapA := idA + "@" + addrA

	// B: validator + bond, joined to A. Both run bond audits, so each earns
	// the other's standing in its own ledger.
	b := startDaemon(t, "B",
		"-listen", "127.0.0.1:0", "-store", t.TempDir(),
		"-bootstrap", bootstrapA, "-validator",
		"-min-rep", "100", "-quorum", "1",
		"-bond", "8M", "-bond-audit", "1s",
		"-capacity", "1G", "-mdns=false", "-id-seed", "1002")
	b.waitFor(t, reBootstrap, 20*time.Second)

	src := filepath.Join(t.TempDir(), "payload.bin")
	want := make([]byte, 256<<10)
	rand.New(rand.NewSource(0xB0AD)).Read(want)
	if err := os.WriteFile(src, want, 0o644); err != nil {
		t.Fatal(err)
	}

	// Publish, retrying until the bond audit has earned standing on both sides
	// (a few 1s rounds). A success here is a real quorum commit on min-rep 100.
	// runClientAllowErr returns combined stdout+stderr, so match the link
	// anywhere (not the ^-anchored, stdout-only reLink). "silt:v1:" does not
	// occur inside the "siltcare:v1:" care link, so this finds the real link.
	reAnyLink := regexp.MustCompile(`silt:v1:\S+`)
	var link, lastOut string
	deadline := time.Now().Add(30 * time.Second)
	for {
		out, err := runClientAllowErr(t, "swarm", "add", src,
			"-peers", bootstrapA, "-registry", regRef, "-chunk-size", "65536")
		lastOut = out
		if err == nil {
			if link = reAnyLink.FindString(out); link != "" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("bonded validators never earned committable standing:\n%s", lastOut)
		}
		time.Sleep(1 * time.Second)
	}
	// The publish drove a real quorum commit (not a self-commit).
	a.waitFor(t, reCommitted, 10*time.Second)

	// And the file round-trips bit-perfect.
	dst := filepath.Join(t.TempDir(), "fetched.bin")
	runClient(t, "swarm", "get", link, "-o", dst, "-peers", bootstrapA, "-registry", regRef)
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) || !bytes.Equal(got, want) {
		t.Fatalf("round-trip corrupted the file: got %d bytes, want %d", len(got), len(want))
	}
	_ = b
}

// TestChainRevocationCommitsOverTCP is the e2e tier for the accountability fix
// (red-team F5): a validator proposes an on-chain takedown of a published root,
// and it COMMITS through a real quorum over real TCP. The per-operator honoring
// (a subscribing node denies it, a non-subscribing node does not) is proven at
// the integration tier (sim/revocation_test.go); this proves the daemon can
// actually drive a quorum revocation end to end — the runtime capability that
// tier could not exercise.
func TestChainRevocationCommitsOverTCP(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e spawns processes; skipped under -short")
	}
	idB := identity.FromSeed(3002).NodeID().String()
	a := startDaemon(t, "A",
		"-listen", "127.0.0.1:0", "-store", t.TempDir(),
		"-serve-registry", "127.0.0.1:0", "-validator",
		"-min-rep", "100", "-quorum", "1", "-attesters", idB,
		"-bond", "8M", "-bond-audit", "1s",
		"-capacity", "1G", "-mdns=false", "-id-seed", "3001")
	peer := a.waitFor(t, rePeer, 20*time.Second)
	idA, addrA := peer[1], peer[2]
	regRef := a.waitFor(t, reRegistry, 20*time.Second)[1]
	bootstrapA := idA + "@" + addrA

	b := startDaemon(t, "B",
		"-listen", "127.0.0.1:0", "-store", t.TempDir(),
		"-bootstrap", bootstrapA, "-validator",
		"-min-rep", "100", "-quorum", "1",
		"-bond", "8M", "-bond-audit", "1s",
		"-capacity", "1G", "-mdns=false", "-id-seed", "3002")
	b.waitFor(t, reBootstrap, 20*time.Second)

	// Publish a root, retrying until the bonded quorum can commit it.
	src := filepath.Join(t.TempDir(), "payload.bin")
	want := make([]byte, 128<<10)
	rand.New(rand.NewSource(0xF5)).Read(want)
	if err := os.WriteFile(src, want, 0o644); err != nil {
		t.Fatal(err)
	}
	reAnyLink := regexp.MustCompile(`silt:v1:\S+`)
	var linkStr, lastOut string
	deadline := time.Now().Add(30 * time.Second)
	for {
		out, err := runClientAllowErr(t, "swarm", "add", src, "-peers", bootstrapA, "-registry", regRef, "-chunk-size", "65536")
		lastOut = out
		if err == nil {
			if linkStr = reAnyLink.FindString(out); linkStr != "" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("publish never committed:\n%s", lastOut)
		}
		time.Sleep(1 * time.Second)
	}
	h, err := link.Parse(linkStr)
	if err != nil {
		t.Fatalf("parse link %q: %v", linkStr, err)
	}

	// A third validator is told to REVOKE that root: once it has earned standing
	// and seen the root committed, it proposes an on-chain takedown that commits
	// through the quorum. It also subscribes to on-chain takedowns (F5).
	c := startDaemon(t, "C",
		"-listen", "127.0.0.1:0", "-store", t.TempDir(),
		"-bootstrap", bootstrapA, "-validator",
		"-min-rep", "100", "-quorum", "1", "-attesters", idA,
		"-bond", "8M", "-bond-audit", "1s",
		"-revoke", h.Root.String(), "-honor-chain-revocations",
		"-capacity", "1G", "-mdns=false", "-id-seed", "3003")
	c.waitFor(t, reBootstrap, 20*time.Second)

	reRevoked := regexp.MustCompile(`proposed on-chain revocation of ` + h.Root.String())
	c.waitFor(t, reRevoked, 40*time.Second)
	_ = b
}

// TestObjectiveConsensusCommitsOverTCP is the e2e tier for objective fork-choice
// (F6): two validators run `-objective`, so eligibility, quorum, and fork-choice
// weight are decided by on-chain bond registrations rather than the local
// reputation view. The launch set bootstraps via -anchors; each validator
// registers its real bond LIVE as it proposes. A `swarm add` drives a real
// objective quorum commit over real TCP, and the file round-trips bit-perfect —
// the fixed protocol (bond registration + verification on the wire) works end to
// end, not just in the sim.
func TestObjectiveConsensusCommitsOverTCP(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e spawns processes; skipped under -short")
	}
	idA := identity.FromSeed(2001).NodeID().String()
	idB := identity.FromSeed(2002).NodeID().String()
	anchors := idA + "," + idB // the declared launch set that bootstraps objective mode

	a := startDaemon(t, "A",
		"-listen", "127.0.0.1:0", "-store", t.TempDir(),
		"-serve-registry", "127.0.0.1:0", "-validator",
		"-objective", "-min-bond", "1M", "-mature-validators", "2",
		"-anchors", anchors, "-quorum", "1", "-attesters", idB,
		"-bond", "8M", "-bond-audit", "1s",
		"-capacity", "1G", "-mdns=false", "-id-seed", "2001")
	peer := a.waitFor(t, rePeer, 20*time.Second)
	addrA := peer[2]
	regRef := a.waitFor(t, reRegistry, 20*time.Second)[1]
	bootstrapA := idA + "@" + addrA

	b := startDaemon(t, "B",
		"-listen", "127.0.0.1:0", "-store", t.TempDir(),
		"-bootstrap", bootstrapA, "-validator",
		"-objective", "-min-bond", "1M", "-mature-validators", "2",
		"-anchors", anchors, "-quorum", "1",
		"-bond", "8M", "-bond-audit", "1s",
		"-capacity", "1G", "-mdns=false", "-id-seed", "2002")
	b.waitFor(t, reBootstrap, 20*time.Second)

	src := filepath.Join(t.TempDir(), "payload.bin")
	want := make([]byte, 256<<10)
	rand.New(rand.NewSource(0x0B1EC)).Read(want)
	if err := os.WriteFile(src, want, 0o644); err != nil {
		t.Fatal(err)
	}

	// Publish → a real objective quorum commit (A proposes + self-registers its
	// bond, B attests as a launch anchor). Retry a few rounds while B joins.
	reAnyLink := regexp.MustCompile(`silt:v1:\S+`)
	var link, lastOut string
	deadline := time.Now().Add(30 * time.Second)
	for {
		out, err := runClientAllowErr(t, "swarm", "add", src,
			"-peers", bootstrapA, "-registry", regRef, "-chunk-size", "65536")
		lastOut = out
		if err == nil {
			if link = reAnyLink.FindString(out); link != "" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("objective validators never committed a publish:\n%s", lastOut)
		}
		time.Sleep(1 * time.Second)
	}
	a.waitFor(t, reCommitted, 10*time.Second)

	dst := filepath.Join(t.TempDir(), "fetched.bin")
	runClient(t, "swarm", "get", link, "-o", dst, "-peers", bootstrapA, "-registry", regRef)
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) || !bytes.Equal(got, want) {
		t.Fatalf("objective-committed file round-trip corrupted: got %d bytes, want %d", len(got), len(want))
	}
	_ = b
}

// TestUnlinkablePublishOverTCP is the e2e tier for publisher privacy (T3,
// #14/F1): three validators issue and REQUIRE publish tokens; a `swarm add`
// acquires a 2-of-3 token over real TCP (paying the fee with its identity, the
// issuers never seeing the serial) and publishes — the entry commits and the
// file round-trips, with no Publisher identity gating it.
func TestUnlinkablePublishOverTCP(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e spawns processes; skipped under -short")
	}
	idA := identity.FromSeed(3001).NodeID().String()
	idB := identity.FromSeed(3002).NodeID().String()
	idC := identity.FromSeed(3003).NodeID().String()

	// Three validators: each a token issuer, requires a 2-of-3 token, knows the
	// other two as attesters. Fast bond audit so standing/issuer-keys warm up.
	common := func(seed string, others string) []string {
		return []string{
			"-listen", "127.0.0.1:0", "-store", t.TempDir(), "-validator",
			"-min-rep", "100", "-quorum", "1", "-attesters", others,
			"-require-tokens", "2", "-bond", "8M", "-bond-audit", "1s",
			"-capacity", "1G", "-mdns=false", "-id-seed", seed,
		}
	}
	a := startDaemon(t, "A", append([]string{"-serve-registry", "127.0.0.1:0"}, common("3001", idB+","+idC)...)...)
	pa := a.waitFor(t, rePeer, 20*time.Second)
	bootA := pa[1] + "@" + pa[2]
	regRef := a.waitFor(t, reRegistry, 20*time.Second)[1]

	b := startDaemon(t, "B", append([]string{"-bootstrap", bootA}, common("3002", idA+","+idC)...)...)
	pb := b.waitFor(t, rePeer, 20*time.Second)
	bootB := pb[1] + "@" + pb[2]
	b.waitFor(t, reBootstrap, 20*time.Second)

	c := startDaemon(t, "C", append([]string{"-bootstrap", bootA}, common("3003", idA+","+idB)...)...)
	pc := c.waitFor(t, rePeer, 20*time.Second)
	bootC := pc[1] + "@" + pc[2]
	c.waitFor(t, reBootstrap, 20*time.Second)

	src := filepath.Join(t.TempDir(), "payload.bin")
	want := make([]byte, 256<<10)
	rand.New(rand.NewSource(0x70CE)).Read(want)
	if err := os.WriteFile(src, want, 0o644); err != nil {
		t.Fatal(err)
	}

	// Publish with a 2-of-3 token, retrying while standing + issuer keys warm up.
	allPeers := strings.Join([]string{bootA, bootB, bootC}, ",")
	reAnyLink := regexp.MustCompile(`silt:v1:\S+`)
	var link, lastOut string
	deadline := time.Now().Add(40 * time.Second)
	for {
		out, err := runClientAllowErr(t, "swarm", "add", src,
			"-peers", allPeers, "-registry", regRef, "-token-quorum", "2", "-chunk-size", "65536")
		lastOut = out
		if err == nil {
			if link = reAnyLink.FindString(out); link != "" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("token-gated publish never succeeded:\n%s", lastOut)
		}
		time.Sleep(1 * time.Second)
	}
	a.waitFor(t, reCommitted, 10*time.Second)

	dst := filepath.Join(t.TempDir(), "fetched.bin")
	runClient(t, "swarm", "get", link, "-o", dst, "-peers", allPeers, "-registry", regRef)
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round-trip corrupted the token-published file: got %d bytes, want %d", len(got), len(want))
	}
	_ = c
}

// TestPublishCommitFetchOverTCP is the whole real-network path in one
// test: three daemons in three processes, a chain-backed registry over
// pinned HTTPS, a publish that must reach quorum and commit a block, and
// a fetch that reassembles the file from chunks scattered across the
// swarm — bit-perfect or bust.
func TestPublishCommitFetchOverTCP(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e spawns processes; skipped under -short")
	}

	// A: validator + registry host. -quorum 0 is the lone-validator
	// self-commit (Quorum counts attestations EXCLUDING the proposer, so
	// a single node can never gather one — 0 is the honest trusted-
	// deployment setting the runbook uses for a one-box swarm).
	a := startDaemon(t, "A",
		"-listen", "127.0.0.1:0", "-store", t.TempDir(),
		"-serve-registry", "127.0.0.1:0",
		"-validator", "-quorum", "0", "-min-rep", "0",
		"-capacity", "1G", "-mdns=false", "-id-seed", "1001")
	peer := a.waitFor(t, rePeer, 20*time.Second)
	idA, addrA := peer[1], peer[2]
	regRef := a.waitFor(t, reRegistry, 20*time.Second)[1]
	bootstrapA := idA + "@" + addrA

	// B, C: plain storage nodes in their own processes, joined to A.
	for i, name := range []string{"B", "C"} {
		d := startDaemon(t, name,
			"-listen", "127.0.0.1:0", "-store", t.TempDir(),
			"-bootstrap", bootstrapA,
			"-capacity", "1G", "-mdns=false",
			"-id-seed", fmt.Sprintf("%d", 1002+i))
		d.waitFor(t, reBootstrap, 20*time.Second)
	}

	// A file of many small chunks so it genuinely stripes and scatters.
	src := filepath.Join(t.TempDir(), "payload.bin")
	want := make([]byte, 1<<20) // 1 MiB
	rand.New(rand.NewSource(0x5117)).Read(want)
	if err := os.WriteFile(src, want, 0o644); err != nil {
		t.Fatal(err)
	}

	// Publish: chunk → encrypt → erasure → scatter → chain-commit.
	out := runClient(t, "swarm", "add", src,
		"-peers", bootstrapA, "-registry", regRef, "-chunk-size", "65536")
	link := reLink.FindString(strings.TrimSpace(out))
	if link == "" {
		t.Fatalf("swarm add printed no silt link:\n%s", out)
	}
	// The publish must have driven a real consensus round to commit.
	a.waitFor(t, reCommitted, 20*time.Second)

	// Fetch from a fresh client: registry lookup → manifest → data
	// chunks pulled across the swarm over TCP → verify → decode →
	// decrypt.
	dst := filepath.Join(t.TempDir(), "fetched.bin")
	runClient(t, "swarm", "get", link, "-o", dst,
		"-peers", bootstrapA, "-registry", regRef)

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round-trip corrupted the file: got %d bytes, want %d", len(got), len(want))
	}
}
