package chain

import (
	"crypto/ed25519"
	"errors"
	"testing"

	"github.com/nerolabs/silt/ports"
)

// TestTrainingWheelsGateYoungNetworkThenShed is the core outcome of the
// launch-window training wheels (risk 15): while the network is immature a
// commit needs anchor sign-off (so a Sybil quorum can't capture it early), and
// the requirement sheds MECHANICALLY once enough distinct non-anchor
// validators have participated — never a flag day.
func TestTrainingWheelsGateYoungNetworkThenShed(t *testing.T) {
	prop := key(1)
	anchorA, anchorB := key(2), key(3)
	v := []ed25519.PrivateKey{key(10), key(11), key(12), key(13)}

	reps := map[ports.NodeID]int64{idOf(prop): 1000, idOf(anchorA): 1000, idOf(anchorB): 1000}
	for _, x := range v {
		reps[idOf(x)] = 1000
	}
	cfg := Config{
		MinProposerRep: 100, MinAttesterRep: 100, Quorum: 3,
		Anchors:          map[ports.NodeID]bool{idOf(anchorA): true, idOf(anchorB): true},
		AnchorQuorum:     1,
		MatureValidators: 3,
	}
	c := New(cfg, func(n ports.NodeID) int64 { return reps[n] })

	commit := func(e ports.Entry, signers ...ed25519.PrivateKey) error {
		prev, height := c.Head()
		b := &Block{Height: height, Prev: prev, Entries: []ports.Entry{e}}
		Sign(b, prop)
		for _, s := range signers {
			b.Atts = append(b.Atts, Attest(b, s))
		}
		return c.Append(*b)
	}

	// OUTCOME 1: young network — a full quorum of independents but NO anchor
	// is refused. A Sybil quorum cannot capture the launch window.
	if err := commit(entry(1), v[0], v[1], v[2]); !errors.Is(err, ErrAnchorRequired) {
		t.Fatalf("immature commit without an anchor must be refused; got %v", err)
	}

	// OUTCOME 2: same young network, quorum WITH anchor sign-off — commits.
	if err := commit(entry(1), anchorA, v[0], v[1]); err != nil {
		t.Fatalf("immature commit with anchor sign-off should succeed; got %v", err)
	}
	if c.Mature() {
		t.Fatal("network should still be immature after one anchor-backed block (2 independents seen)")
	}

	// Accumulate distinct non-anchor validators via anchor-backed commits.
	if err := commit(entry(2), anchorA, v[1], v[2]); err != nil { // adds v2 → 3 distinct
		t.Fatal(err)
	}
	if !c.Mature() {
		t.Fatal("network should be mature after 3 distinct non-anchor validators attested")
	}

	// OUTCOME 3: mature network — a quorum with NO anchor commits. The wheels
	// shed on measured decentralization, mechanically.
	if err := commit(entry(3), v[0], v[1], v[3]); err != nil {
		t.Fatalf("mature commit without anchors should succeed (wheels shed); got %v", err)
	}
}

// Anchors are only a launch-window control: with no anchors configured (the
// default), the chain behaves exactly as before — no maturity gate.
func TestNoAnchorsMeansNoTrainingWheels(t *testing.T) {
	c := New(DefaultConfig(), func(ports.NodeID) int64 { return 1000 })
	if !c.Mature() {
		t.Fatal("a chain with no MatureValidators threshold is always 'mature' (wheels off)")
	}
}
