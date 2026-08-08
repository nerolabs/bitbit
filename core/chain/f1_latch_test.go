package chain

import (
	"crypto/ed25519"
	"errors"
	"testing"

	"github.com/nerolabs/silt/ports"
)

// TestMaturityLatchDoesNotRearmAnchorsOnDemature is the F-1 regression — the blind
// red-team PoC (TestDematurationHaltsChainOrHandsPermanentPowerToRetiredAnchor)
// INVERTED. That PoC drove a matured network back to immature by CONCENTRATION (one
// honest whale growing REAL bond past ⌊total/3⌋, no attack primitive) and showed the
// two horns: HALT (a purely-real quorum is rejected, ErrAnchorRequired) and
// PERMANENT CENTER (a zero-bond anchor's signature is the only thing that re-commits).
//
// With the one-way `everMature` latch, neither horn is reachable:
//   - the anchor requirement is gated on EverMature() (the latch), not the live
//     Mature(), so a later drop in decentralization NEVER re-arms the anchors;
//   - a purely-real quorum with NO anchor keeps committing (HALT horn killed);
//   - once matured, a launch anchor loses bond-free eligibility forever, so a
//     zero-bond anchor is no longer qualified (PERMANENT-CENTER horn killed).
func TestMaturityLatchDoesNotRearmAnchorsOnDemature(t *testing.T) {
	const minBond = int64(1) << 20
	const ttl = uint64(50)

	a1 := key(1) // one launch anchor; AnchorQuorum=1
	w1 := key(10)
	s1, s2, s3 := key(11), key(12), key(13)

	cfg := Config{
		Quorum: 1, MinBond: minBond,
		Anchors:          map[ports.NodeID]bool{idOf(a1): true},
		AnchorQuorum:     1,
		MatureValidators: 2,
		BondTTLBlocks:    ttl,
	}
	c := New(cfg, func(ports.NodeID) int64 { return 0 })
	c.SetBondVerifier(objectiveVerify)

	regs := []BondReg{
		bondReg(w1, minBond, ports.Hash{}),
		bondReg(s1, minBond, ports.Hash{}),
		bondReg(s2, minBond, ports.Hash{}),
		bondReg(s3, minBond, ports.Hash{}),
	}
	g := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(0)}, BondRegs: regs}
	Sign(g, a1)
	if err := c.AppendGenesis(*g); err != nil {
		t.Fatalf("genesis: %v", err)
	}

	// Enroll the four real validators as non-proposer attesters; the anchor co-signs
	// while immature to satisfy the wheels.
	b := g
	rounds := []struct {
		proposer ed25519.PrivateKey
		group    []ed25519.PrivateKey
	}{
		{w1, []ed25519.PrivateKey{s1, s2, s3}},
		{s1, []ed25519.PrivateKey{w1}},
	}
	for i, r := range rounds {
		nb := &Block{Version: BlockVersion, Height: b.Height + 1, Prev: b.Hash(),
			Entries: []ports.Entry{entry(byte(i + 1))}}
		Sign(nb, r.proposer)
		nb.Atts = append(nb.Atts, Attest(nb, a1))
		for _, k := range r.group {
			nb.Atts = append(nb.Atts, Attest(nb, k))
		}
		if err := c.Append(*nb); err != nil {
			t.Fatalf("bootstrap commit height %d: %v", nb.Height, err)
		}
		b = nb
	}
	if !c.Mature() || !c.EverMature() {
		t.Fatalf("SETUP: network should be mature AND latched (mature=%v everMature=%v)", c.Mature(), c.EverMature())
	}

	// ---- DE-MATURE by CONCENTRATION (the honest-whale C2 residue, no attack). ----
	const whale = 10 * minBond
	for c.Mature() {
		nb := &Block{Version: BlockVersion, Height: b.Height + 1, Prev: b.Hash(),
			Entries:  []ports.Entry{entry(byte(b.Height + 1))},
			BondRegs: []BondReg{bondReg(w1, whale, b.Hash()), bondReg(s1, minBond, b.Hash())}}
		Sign(nb, w1)
		nb.Atts = []Attestation{Attest(nb, s1)}
		if err := c.Append(*nb); err != nil {
			t.Fatalf("renewal commit at height %d: %v", nb.Height, err)
		}
		b = nb
		if b.Height > ttl+5 {
			break
		}
	}

	// The LIVE metric de-matured, but the LATCH held — the whole point of F-1.
	if c.Mature() {
		t.Fatalf("SETUP: network should have de-matured live (coefficient %d < 2)", c.C2Metric().NakamotoOperators)
	}
	if !c.EverMature() {
		t.Fatal("F-1 REGRESSION: the maturity latch must STAY set after de-maturation — it is a one-way ratchet")
	}
	t.Log("de-matured LIVE, latch HELD: Mature()=false but EverMature()=true")

	// ---- HORN 1 (HALT) KILLED: a purely-real quorum with NO anchor still commits. ----
	realOnly := &Block{Version: BlockVersion, Height: b.Height + 1, Prev: b.Hash(),
		Entries:  []ports.Entry{entry(byte(b.Height + 1))},
		BondRegs: []BondReg{bondReg(w1, whale, b.Hash()), bondReg(s1, minBond, b.Hash())}}
	Sign(realOnly, w1)                                  // real proposer
	realOnly.Atts = []Attestation{Attest(realOnly, s1)} // real non-proposer attester; NO anchor
	if err := c.Append(*realOnly); err != nil {
		if errors.Is(err, ErrAnchorRequired) {
			t.Fatalf("HORN-1 (HALT) NOT killed: a real quorum was rejected for lack of an anchor after de-maturation — the latch failed to hold: %v", err)
		}
		t.Fatalf("HORN-1: a real quorum should commit after de-maturation, got: %v", err)
	}
	b = realOnly
	t.Log("HORN-1 (HALT) KILLED: an all-real quorum with NO anchor commits after de-maturation")

	// ---- HORN 2 (PERMANENT CENTER) KILLED: the zero-bond anchor is no longer qualified. ----
	if c.BondedSize(idOf(a1)) != 0 {
		t.Fatalf("precondition: anchor a1 should hold zero real bond, got %d", c.BondedSize(idOf(a1)))
	}
	if c.attesterQualified(idOf(a1)) || c.proposerQualified(idOf(a1)) {
		t.Fatal("HORN-2 (PERMANENT CENTER) NOT killed: a zero-bond anchor is still qualified after maturity — launchAnchor must gate on the latch, not live Mature()")
	}
	t.Log("HORN-2 (PERMANENT CENTER) KILLED: once matured, a zero-bond anchor is no longer attester/proposer-qualified")
}

// TestDeMatureSuperQuorumReplacesTheAnchorNet is the F-1 slice-2 regression: once a
// network has matured (anchors retired by the latch) and then de-matures, a commit no
// longer needs anchors but DOES need a real-bond super-majority (≥⅔ of live bonded
// weight). A small-validator coalition that meets the head-count quorum but holds < ⅔
// of the weight is rejected; a coalition holding ≥⅔ commits. This is the center-less
// liveness rule that replaces the retired anchor net (so the HALT horn stays dead
// without handing power back to a standing-free set).
func TestDeMatureSuperQuorumReplacesTheAnchorNet(t *testing.T) {
	const minBond = int64(1) << 20
	a1 := key(1)
	w1, s1, s2, s3 := key(10), key(11), key(12), key(13)
	cfg := Config{
		Quorum: 1, MinBond: minBond,
		Anchors:          map[ports.NodeID]bool{idOf(a1): true},
		AnchorQuorum:     1,
		MatureValidators: 2,
		// BondTTLBlocks 0: nothing lapses, so we de-mature by CONCENTRATION alone and
		// keep s1,s2,s3 bonded — the topology that lets a sub-⅔ coalition even form.
	}
	c := New(cfg, func(ports.NodeID) int64 { return 0 })
	c.SetBondVerifier(objectiveVerify)

	regs := []BondReg{bondReg(w1, minBond, ports.Hash{}), bondReg(s1, minBond, ports.Hash{}),
		bondReg(s2, minBond, ports.Hash{}), bondReg(s3, minBond, ports.Hash{})}
	g := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(0)}, BondRegs: regs}
	Sign(g, a1)
	if err := c.AppendGenesis(*g); err != nil {
		t.Fatalf("genesis: %v", err)
	}
	b := g
	rounds := []struct {
		p ed25519.PrivateKey
		g []ed25519.PrivateKey
	}{{w1, []ed25519.PrivateKey{s1, s2, s3}}, {s1, []ed25519.PrivateKey{w1}}}
	for i, r := range rounds {
		nb := &Block{Version: BlockVersion, Height: b.Height + 1, Prev: b.Hash(), Entries: []ports.Entry{entry(byte(i + 1))}}
		Sign(nb, r.p)
		nb.Atts = append(nb.Atts, Attest(nb, a1))
		for _, k := range r.g {
			nb.Atts = append(nb.Atts, Attest(nb, k))
		}
		if err := c.Append(*nb); err != nil {
			t.Fatalf("bootstrap commit %d: %v", nb.Height, err)
		}
		b = nb
	}
	if !c.EverMature() {
		t.Fatal("SETUP: network should be latched mature")
	}

	// De-mature by CONCENTRATION: w1 renews at 10x. Pre-block the network is still
	// mature, so this block commits under normal rules; after apply it is de-matured
	// while s1,s2,s3 stay bonded (TTL 0).
	const whale = 10 * minBond
	dm := &Block{Version: BlockVersion, Height: b.Height + 1, Prev: b.Hash(),
		Entries: []ports.Entry{entry(9)}, BondRegs: []BondReg{bondReg(w1, whale, b.Hash())}}
	Sign(dm, w1)
	dm.Atts = []Attestation{Attest(dm, s1)}
	if err := c.Append(*dm); err != nil {
		t.Fatalf("de-maturing commit: %v", err)
	}
	b = dm
	if c.Mature() {
		t.Fatalf("SETUP: network should have de-matured (coefficient %d < 2)", c.C2Metric().NakamotoOperators)
	}
	if !c.EverMature() {
		t.Fatal("latch must hold through de-maturation")
	}

	// A SMALL coalition (s1 proposer + s2 attester = 2 MiB of 13 MiB) meets the
	// head-count quorum (1) but is BELOW ⅔ of the bonded weight → rejected.
	small := &Block{Version: BlockVersion, Height: b.Height + 1, Prev: b.Hash(), Entries: []ports.Entry{entry(20)}}
	Sign(small, s1)
	small.Atts = []Attestation{Attest(small, s2)}
	if err := c.Append(*small); !errors.Is(err, ErrDeMatureQuorum) {
		t.Fatalf("a sub-⅔ coalition must be rejected with ErrDeMatureQuorum after de-maturation, got: %v", err)
	}
	t.Log("sub-⅔ coalition REJECTED: de-maturation requires a real-bond super-quorum, not just head-count")

	// A coalition INCLUDING the whale (w1 proposer + s1 attester = 11 MiB of 13 MiB)
	// clears ⅔ → commits, with NO anchor. HALT horn stays dead, center-lessly.
	big := &Block{Version: BlockVersion, Height: b.Height + 1, Prev: b.Hash(),
		Entries: []ports.Entry{entry(21)}, BondRegs: []BondReg{bondReg(w1, whale, b.Hash())}}
	Sign(big, w1)
	big.Atts = []Attestation{Attest(big, s1)}
	if err := c.Append(*big); err != nil {
		t.Fatalf("a ≥⅔ real-bond coalition must commit after de-maturation (no anchor), got: %v", err)
	}
	t.Log("≥⅔ real-bond coalition COMMITS with no anchor: liveness preserved center-lessly")
}

// TestWeakSubjectivityCheckpointRefusesLongRangeReorg is the F-1 slice-4 regression:
// a weak-subjectivity checkpoint pins a recent trusted block, so a fork that rewrites
// history AT OR BEFORE it is refused — even when it is strictly HEAVIER (the long-range
// attack that makes the maturity latch safe for a fresh node). A heavier fork that
// keeps the checkpoint and only reorgs AFTER it is still adopted normally.
func TestWeakSubjectivityCheckpointRefusesLongRangeReorg(t *testing.T) {
	mkBlock := func(w *world, prev ports.Hash, h uint64, e ports.Entry, nAtt int) *Block {
		b := &Block{Version: BlockVersion, Height: h, Prev: prev, Entries: []ports.Entry{e}}
		Sign(b, w.prop)
		for _, v := range w.vals[:nAtt] {
			b.Atts = append(b.Atts, Attest(b, v))
		}
		return b
	}

	// Canonical history g → c1 → c2 (weights 3, 3). Build it first to learn c1's hash.
	tmp := newWorld(DefaultConfig())
	g := tmp.genesis()
	c1 := mkBlock(tmp, g.Hash(), 1, entry(1), 3)
	c2 := mkBlock(tmp, c1.Hash(), 2, entry(2), 3)

	// Pin the checkpoint at height 1 (= c1). A fresh replica carrying it:
	cfg := DefaultConfig()
	cfg.WSCheckpoint = WSCheckpoint{Height: 1, Hash: c1.Hash()}
	w := newWorld(cfg)
	gg := w.genesis()
	if gg.Hash() != g.Hash() {
		t.Fatal("setup: genesis must match")
	}
	for _, b := range []*Block{c1, c2} {
		if err := w.c.Append(*b); err != nil {
			t.Fatalf("append canonical %d: %v", b.Height, err)
		}
	}

	// A HEAVIER fork that diverges at height 1 (rewrites the checkpoint block): weights
	// 4 + 4 = 8 > canonical 6. It must be REFUSED regardless of weight.
	preC1 := mkBlock(w, g.Hash(), 1, entry(3), 4)
	preC2 := mkBlock(w, preC1.Hash(), 2, entry(4), 4)
	adopted, err := w.c.Reconcile([]Block{*g, *preC1, *preC2})
	if adopted || !errors.Is(err, ErrPreCheckpointReorg) {
		t.Fatalf("a heavier fork rewriting the checkpoint block must be refused with ErrPreCheckpointReorg (adopted=%v err=%v)", adopted, err)
	}
	if _, ok := w.c.LookupRoot(entry(2).Root); !ok {
		t.Fatal("the canonical (checkpointed) history must remain after refusing the long-range fork")
	}
	t.Log("long-range reorg REFUSED: a heavier fork that rewrites pre-checkpoint history is rejected")

	// A HEAVIER fork that KEEPS the checkpoint (c1) and only reorgs AFTER it (height 2)
	// is adopted normally: weight 3 + 4 = 7 > canonical 6.
	postC2 := mkBlock(w, c1.Hash(), 2, entry(5), 4)
	adopted, err = w.c.Reconcile([]Block{*g, *c1, *postC2})
	if err != nil || !adopted {
		t.Fatalf("a heavier POST-checkpoint reorg must be adopted (adopted=%v err=%v)", adopted, err)
	}
	if _, ok := w.c.LookupRoot(entry(5).Root); !ok {
		t.Fatal("the post-checkpoint reorg's entry should be present after adoption")
	}
	t.Log("post-checkpoint reorg ADOPTED: weak subjectivity only pins history at/before the checkpoint")
}

// TestMaturityLatchSurvivesReloadAndReconcile pins that the latch is a CONSENSUS
// fact (a pure function of the committed blocks), not process-local memory: a fresh
// replica that Reloads the same history, and a replica that Reconciles onto it,
// both re-derive everMature=true.
func TestMaturityLatchSurvivesReloadAndReconcile(t *testing.T) {
	const minBond = int64(1) << 20
	a1 := key(1)
	w1, s1, s2, s3 := key(10), key(11), key(12), key(13)
	cfg := Config{
		Quorum: 1, MinBond: minBond,
		Anchors:          map[ports.NodeID]bool{idOf(a1): true},
		AnchorQuorum:     1,
		MatureValidators: 2,
	}
	mk := func() *Chain {
		c := New(cfg, func(ports.NodeID) int64 { return 0 })
		c.SetBondVerifier(objectiveVerify)
		return c
	}

	c := mk()
	regs := []BondReg{bondReg(w1, minBond, ports.Hash{}), bondReg(s1, minBond, ports.Hash{}),
		bondReg(s2, minBond, ports.Hash{}), bondReg(s3, minBond, ports.Hash{})}
	g := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(0)}, BondRegs: regs}
	Sign(g, a1)
	if err := c.AppendGenesis(*g); err != nil {
		t.Fatalf("genesis: %v", err)
	}
	b := g
	rounds := []struct {
		p ed25519.PrivateKey
		g []ed25519.PrivateKey
	}{{w1, []ed25519.PrivateKey{s1, s2, s3}}, {s1, []ed25519.PrivateKey{w1}}}
	for i, r := range rounds {
		nb := &Block{Version: BlockVersion, Height: b.Height + 1, Prev: b.Hash(), Entries: []ports.Entry{entry(byte(i + 1))}}
		Sign(nb, r.p)
		nb.Atts = append(nb.Atts, Attest(nb, a1))
		for _, k := range r.g {
			nb.Atts = append(nb.Atts, Attest(nb, k))
		}
		if err := c.Append(*nb); err != nil {
			t.Fatalf("commit %d: %v", nb.Height, err)
		}
		b = nb
	}
	if !c.EverMature() {
		t.Fatal("SETUP: source chain should be latched mature")
	}
	blocks := c.Blocks(0)

	// Reload into a fresh replica → the latch must re-derive from the history alone.
	rc := mk()
	if _, err := rc.Reload(blocks); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !rc.EverMature() {
		t.Fatal("F-1 REGRESSION: a Reloaded replica must re-derive everMature=true from the committed history (consensus fact, not local memory)")
	}

	// Reconcile a fresh replica onto the same history → adopt must carry the latch.
	rec := mk()
	Sign(g, a1)
	if err := rec.AppendGenesis(*g); err != nil {
		t.Fatalf("reconcile genesis: %v", err)
	}
	if _, err := rec.Reconcile(blocks); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !rec.EverMature() {
		t.Fatal("F-1 REGRESSION: a Reconciled replica must carry everMature=true through adopt()")
	}
}
