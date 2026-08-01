package sim

import "testing"

// V3, test-the-adversary: storage-earned standing commits, a wash-serving
// Sybil ring is gatekept at both the propose and the quorum gate, and a
// validator that stops re-proving its bond loses its vote.
func TestBondStandingGatekeepsSybilsAndDecays(t *testing.T) {
	res, err := BondStanding(7, DefaultBondStandingOpts())
	if err != nil {
		t.Fatal(err)
	}
	if res.Committed != 1 {
		t.Fatalf("bonded quorum should commit exactly 1 block, got %d", res.Committed)
	}
	if res.ReplicasInSync < DefaultBondStandingOpts().Bonded {
		t.Fatalf("expected the committed block to reach the bonded replicas, in sync = %d", res.ReplicasInSync)
	}
	if !res.SybilProposalRejected {
		t.Fatal("a wash-serving sybil must not be able to propose")
	}
	if !res.SybilQuorumDenied {
		t.Fatal("an all-sybil attester set must not form a quorum")
	}
	if !res.DecayedOut {
		t.Fatal("validators that stopped re-proving their bond must lose their vote")
	}
}

func TestBondStandingIsDeterministic(t *testing.T) {
	a, err := BondStanding(11, DefaultBondStandingOpts())
	if err != nil {
		t.Fatal(err)
	}
	b, err := BondStanding(11, DefaultBondStandingOpts())
	if err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Fatalf("same seed diverged:\n  %s\n  %s", a.String(), b.String())
	}
}
