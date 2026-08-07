package e2e

import (
	"regexp"
	"testing"
	"time"

	"github.com/nerolabs/silt/adapters/identity"
)

// TestEquivocatorSlashedOverTCP is the #184 accountability gate over the REAL WIRE: a
// validator that DELIBERATELY double-signs (the `-equivocate` red-team harness) is
// caught and slashed by an honest replica that reconciles the fork — proving "a proven
// double-sign costs standing" (D2) holds against real daemons over real TCP, not only
// in the in-process sim.
//
// Topology (deterministic, no timing race — fork-choice is summed attester weight):
//   - A is the adversary. Once it has earned standing with both peers it double-signs
//     at height 1: it places block X on honest B (B = [g,X], weight 1) and a heavier
//     CONFLICTING fork Y,Z on honest C (C = [g,Y,Z], weight 2).
//   - B syncs the chain from C (its -attesters set), reconciles to the heavier history,
//     and chain.FindEquivocations catches A signing two different blocks at height 1 —
//     so B slashes A and prints it.
//   - C runs with NO -attesters, so it never syncs B's X-fork and stays a clean
//     recipient of the conflicting fork.
func TestEquivocatorSlashedOverTCP(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e spawns processes; skipped under -short")
	}
	idA := identity.FromSeed(6001).NodeID().String()
	idB := identity.FromSeed(6002).NodeID().String()
	idC := identity.FromSeed(6003).NodeID().String()

	// Legacy (subjective) consensus with earned bonded standing — the recipe
	// TestBondEarnedStandingCommitsOverTCP proves works over TCP. Fast bond audit so
	// standing warms up in a few seconds.
	common := func() []string {
		return []string{
			"-listen", "127.0.0.1:0", "-store", t.TempDir(), "-validator",
			"-objective=false", "-min-rep", "100", "-quorum", "1",
			"-bond", "8M", "-bond-audit", "1s",
			"-capacity", "1G", "-mdns=false",
		}
	}

	// A: the adversary. It will double-sign, placing X on B and the heavier (Y,Z) on C.
	a := startDaemon(t, "A(adversary)", append(common(),
		"-attesters", idB+","+idC, "-equivocate", idB+","+idC, "-id-seed", "6001")...)
	pa := a.waitFor(t, rePeer, 20*time.Second)
	bootA := pa[1] + "@" + pa[2]

	// B: the honest DETECTOR — holds A's block X, syncs from C, catches the double-sign.
	b := startDaemon(t, "B(honest-detector)", append(common(),
		"-bootstrap", bootA, "-attesters", idC, "-id-seed", "6002")...)
	b.waitFor(t, reBootstrap, 20*time.Second)

	// C: honest recipient of the heavier conflicting fork; no -attesters so it does not
	// sync the X-fork and stays clean to receive Y,Z.
	c := startDaemon(t, "C(honest)", append(common(),
		"-bootstrap", bootA, "-id-seed", "6003")...)
	c.waitFor(t, reBootstrap, 20*time.Second)

	// The adversary announces once it has completed the double-sign (it retries until it
	// has earned standing with both peers).
	a.waitFor(t, regexp.MustCompile(`adversary: equivocation complete`), 90*time.Second)

	// The honest detector slashes A for the equivocation — the accountability property,
	// proven over real TCP.
	b.waitFor(t, regexp.MustCompile(`chain: slashed equivocator `+idA), 90*time.Second)
	_ = c
}
