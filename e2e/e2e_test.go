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
