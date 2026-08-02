package chain

import (
	"crypto/ed25519"
	"errors"
	"testing"

	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/ports"
)

// TestSybilCannotFormQuorumWithoutHeldStorage is a V3 "test the adversary"
// case (TENETS Part IV): write the attacker's win as a test that MUST fail
// for them. A Sybil ring mints unlimited SELF-REPORTED serving credit and
// then tries to assemble a write quorum. Because standing (credit.Ledger
// Reputation) now derives from CHALLENGED held storage, not from serving,
// the ring stays at zero reputation and the quorum is denied. The positive
// control proves the gate still OPENS for validators that actually prove
// storage — the cost is real, not prohibitive.
func TestSybilCannotFormQuorumWithoutHeldStorage(t *testing.T) {
	led := credit.New(50_000, 0)
	cfg := DefaultConfig() // MinProposerRep/MinAttesterRep 100, Quorum 3
	ch := New(cfg, led.Reputation)

	// Honest proposer: proves a 64 MiB identity-bound bond → rep ~1024.
	prop := key(1)
	led.RecordBondChallenge(idOf(prop), 64<<20, true, 1)

	// Four Sybils that wash-serve each other enormous volumes.
	var sybils []ed25519.PrivateKey
	for s := int64(10); s < 14; s++ {
		sybils = append(sybils, key(s))
	}
	chunk := ports.HashBytes([]byte("wash"))
	for _, a := range sybils {
		for _, b := range sybils {
			if idOf(a) != idOf(b) {
				led.RecordServe(idOf(a), idOf(b), chunk, 1<<40) // 1 TiB each way
			}
		}
	}

	// Wash-serving moved BALANCES (the gameable economy is still there) but
	// bought ZERO STANDING — the whole point of the split.
	for _, s := range sybils {
		if got := led.Reputation(idOf(s)); got != 0 {
			t.Fatalf("sybil reputation %d, want 0 — wash-serving must not create standing", got)
		}
		if led.Balance(idOf(s)) == 0 {
			t.Fatal("expected wash-serving to move balances (observability economy intact)")
		}
	}

	// The ring attests an honest proposal. Its signatures are valid but its
	// reputation is 0, so none count toward quorum.
	prev, height := ch.Head()
	b := &Block{Version: BlockVersion, Height: height, Prev: prev, Entries: []ports.Entry{entry(7)}}
	Sign(b, prop)
	for _, s := range sybils {
		b.Atts = append(b.Atts, Attest(b, s))
	}
	if err := ch.Append(*b); !errors.Is(err, ErrNoQuorum) {
		t.Fatalf("Sybil quorum was admitted (err=%v); want ErrNoQuorum", err)
	}

	// Positive control: three attesters now prove real 8 MiB bonds (→ rep
	// ~128 ≥ 100). The same block commits — standing is available to anyone
	// who actually pays storage, which is exactly the incentive we want.
	for i := 0; i < 3; i++ {
		led.RecordBondChallenge(idOf(sybils[i]), 8<<20, true, 2)
	}
	b2 := &Block{Version: BlockVersion, Height: height, Prev: prev, Entries: []ports.Entry{entry(7)}}
	Sign(b2, prop)
	for _, s := range sybils {
		b2.Atts = append(b2.Atts, Attest(b2, s))
	}
	if err := ch.Append(*b2); err != nil {
		t.Fatalf("block with three bonded attesters should commit, got %v", err)
	}
}
