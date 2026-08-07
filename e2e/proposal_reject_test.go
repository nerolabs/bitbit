package e2e

import (
	"regexp"
	"testing"
	"time"

	"github.com/nerolabs/silt/adapters/identity"
)

// The two ValidateProposal defences of #184, proven over the REAL WIRE: an honest
// validator, on receiving a proposal, refuses to attest it if the proposer signature
// is forged (forged-block→reject) or the proposer lacks a qualifying bond
// (low-bond→reject). Both are single-message exchanges — the honest node replies
// "won't attest", which the red-team daemon observes and reports.

func rejectVal(store string, extra ...string) []string {
	return append([]string{
		"-listen", "127.0.0.1:0", "-store", store, "-validator",
		"-objective=false", "-min-rep", "100", "-quorum", "1",
		"-bond", "8M", "-bond-audit", "1s",
		"-capacity", "1G", "-mdns=false",
	}, extra...)
}

// TestForgedBlockRejectedOverTCP: a validator proposes a block whose proposer signature
// has been corrupted; the honest target's ValidateProposal verify fails and it refuses
// to attest — over real TCP.
func TestForgedBlockRejectedOverTCP(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e spawns processes; skipped under -short")
	}
	idA := identity.FromSeed(8001).NodeID().String()

	a := startDaemon(t, "A(honest)", rejectVal(t.TempDir(), "-id-seed", "8001")...)
	pa := a.waitFor(t, rePeer, 20*time.Second)
	bootA := pa[1] + "@" + pa[2]

	f := startDaemon(t, "F(forger)", rejectVal(t.TempDir(),
		"-bootstrap", bootA, "-forge-block", idA, "-id-seed", "8002")...)
	f.waitFor(t, reBootstrap, 20*time.Second)

	// The forger reports that the honest validator refused its forged proposal.
	f.waitFor(t, regexp.MustCompile(`forge-block proposal correctly REJECTED by `+idA), 30*time.Second)
}

// TestLowBondProposerRejectedOverTCP: an UNDER-BONDED validator (a tiny bond, far below
// the min-rep bar) proposes a correctly-signed, well-formed block; the honest target
// refuses to attest because the proposer is not a qualified bonded validator — over
// real TCP.
func TestLowBondProposerRejectedOverTCP(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e spawns processes; skipped under -short")
	}
	idA := identity.FromSeed(8003).NodeID().String()

	a := startDaemon(t, "A(honest)", rejectVal(t.TempDir(), "-id-seed", "8003")...)
	pa := a.waitFor(t, rePeer, 20*time.Second)
	bootA := pa[1] + "@" + pa[2]

	// L holds only a 128 KiB bond — earns standing far below the -min-rep 100 bar, so it
	// is not a qualified proposer. Its block is otherwise valid and correctly signed, so
	// the ONLY reason for refusal is its insufficient bond.
	l := startDaemon(t, "L(low-bond)", rejectVal(t.TempDir(),
		"-bootstrap", bootA, "-bond", "128K", "-lowbond-propose", idA, "-id-seed", "8004")...)
	l.waitFor(t, reBootstrap, 20*time.Second)

	l.waitFor(t, regexp.MustCompile(`lowbond-propose proposal correctly REJECTED by `+idA), 30*time.Second)
}
