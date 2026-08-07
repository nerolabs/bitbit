package node

// H7 slice-2 acceptance red-team (design §11): drive the REAL repair-claim
// verification handler over a live network with self-dealing / false-repair
// adversaries and assert the WIRED verdict — slash for an attributable correctness
// lie, deny (never slash) for a retrievability shortfall. The pure legs are
// unit-tested in core/repairproof; these prove the node wiring
// (fetch-survivors + challenge-holder + Decide + settle) delivers the crypto's
// verdict to the ledger against a real attacker. Each is a permanent regression.

import (
	"bytes"
	"testing"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/erasure"
	"github.com/nerolabs/silt/core/link"
	"github.com/nerolabs/silt/core/manifest"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/core/por"
	"github.com/nerolabs/silt/core/registry"
	"github.com/nerolabs/silt/core/repairproof"
	"github.com/nerolabs/silt/ports"
)

// repairAdv is a small live cluster with a coded object distributed across it — the
// stage for driving handleRepairClaim against crafted claims.
type repairAdv struct {
	sched  *simclock.Scheduler
	net    *simnet.Network
	nodes  []*Node
	reg    ports.Registry
	ledger *credit.Ledger
	m      *manifest.Manifest
	root   ports.Hash
	porKey *por.Key
	h      link.Handle
}

func newRepairAdv(t *testing.T, seed int64) *repairAdv {
	t.Helper()
	const N = 24
	sched := simclock.New()
	net := simnet.New(sched, seed, simnet.DefaultConfig())
	reg := registry.New()
	ledger := credit.New(50_000, 10_000_000) // grant funders enough to prepay a reserve
	cfg := DefaultConfig()
	cfg.Replication = 3
	cfg.RepairBountyBase = 1000

	var nodes []*Node
	for i := 0; i < N; i++ {
		id := identity.FromSeed(int64(1000 + i)).NodeID()
		nd := New(id, cfg, sched, net.Endpoint(id), memstore.New())
		nd.SetLedger(ledger)
		ledger.Register(id)
		nodes = append(nodes, nd)
	}
	for i, nd := range nodes {
		if i == 0 {
			continue
		}
		var seeds []ports.NodeID
		for j := 0; j < i && j < 3; j++ {
			seeds = append(seeds, nodes[j].ID())
		}
		nd.Bootstrap(seeds, func() {})
	}
	sched.Run()

	data := make([]byte, 256<<10)
	for i := range data {
		data[i] = byte(i*7 + 3)
	}
	h, err := pipeline.Add(bg(), nodes[0].Store(), reg, bytes.NewReader(data),
		pipeline.Options{ChunkSize: 4 << 10, Mode: crypto.Convergent, Erasure: erasure.DefaultParams})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	entry, _, _ := reg.Lookup(bg(), h.Root)
	m, err := pipeline.LoadFull(bg(), nodes[0].Store(), entry, h)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	nodes[0].Distribute(entry, m, false, DerivePorKey(h.LayoutKey()), func(int, error) {})
	sched.Run()

	return &repairAdv{
		sched: sched, net: net, nodes: nodes, reg: reg, ledger: ledger,
		m: m, root: h.Root, porKey: DerivePorKey(h.LayoutKey()), h: h,
	}
}

// careJudge puts nodes[1] on duty as the caretaker-judge. It wires the caretaker
// state directly (registry + care-link) rather than calling Care(), which would
// also start the perpetual repair-tick loop and stop sched.Run() from ever reaching
// quiescence — the handler is driven directly here, so the loop isn't needed. The
// judge fetches the manifest itself when it judges.
func (s *repairAdv) careJudge() *Node {
	j := s.nodes[1]
	j.reg = s.reg
	j.care = append(j.care, s.h.Care())
	return j
}

func (s *repairAdv) fundEscrow(amount int64) {
	if err := s.ledger.FundEscrow(s.root, s.nodes[0].ID(), amount); err != nil {
		panic(err)
	}
}

// bond gives nd a comfortable bonded standing and returns it — the baseline a
// slash must lower and a deny must leave untouched.
func (s *repairAdv) bond(nd *Node) int64 {
	s.ledger.RecordBondChallenge(nd.ID(), nd.ID(), 128<<20, true, 1)
	return s.ledger.Reputation(nd.ID())
}

// deliverClaim hands a crafted claim to the judge as if `from` had broadcast it,
// then runs the network to quiescence so the two legs settle.
func (s *repairAdv) deliverClaim(judge *Node, from ports.NodeID, claim repairproof.RepairClaim) {
	data, err := claim.Marshal()
	if err != nil {
		panic(err)
	}
	judge.handleRepairClaim(from, ports.Message{Kind: ports.MsgRepairClaim, Data: data})
	s.sched.Run()
}

// parityTarget names stripe 0's first parity shard: its stripe position, its
// manifest-committed id, and its Merkle leaf index.
func (s *repairAdv) parityTarget() (pos int, id ports.ChunkID, leafIdx int) {
	return s.m.K, s.m.ParityIDs()[0], len(s.m.ChunkIDs())
}

// TestRedteamRepair_GarbageClaimIsSlashed (§11 a): a caretaker claims a repair it
// did not do — the claim names a real position but a BOGUS shard id. The judge
// recomputes the position from the manifest-anchored survivors, sees it does not
// match the claimed id (a self-attributing fraud proof), and SLASHES the claimant.
// No bounty is paid.
func TestRedteamRepair_GarbageClaimIsSlashed(t *testing.T) {
	s := newRepairAdv(t, 42)
	s.fundEscrow(5_000_000)
	judge := s.careJudge()
	attacker := s.nodes[2]
	baseline := s.bond(attacker)

	pos, _, _ := s.parityTarget()
	bogus := ports.HashBytes([]byte("not the real shard"))
	claim := repairproof.RepairClaim{
		Root: s.root, Stripe: 0, ShardPos: pos, ShardID: bogus, Holder: s.nodes[7].ID(),
	}
	s.deliverClaim(judge, attacker.ID(), claim)

	if judge.Stats.FalseRepairSlashes != 1 {
		t.Fatalf("garbage claim not slashed: FalseRepairSlashes=%d (correctness recompute must reject a wrong id)", judge.Stats.FalseRepairSlashes)
	}
	if got := s.ledger.Reputation(attacker.ID()); got >= baseline {
		t.Fatalf("claimant standing not docked: %d >= %d", got, baseline)
	}
	if paid := s.ledger.EscrowPaid(s.root); paid != 0 {
		t.Fatalf("garbage claim drew a bounty: %d", paid)
	}
	if judge.Stats.BountiesReleased != 0 {
		t.Fatal("garbage claim released a bounty")
	}
}

// TestRedteamRepair_ComputeButDontStoreIsDenied (§11 c): the claimed shard id IS
// correct (the correct bytes even exist on the survivors), but the NAMED holder is
// a liar that kept the proof + PoR tags and dropped the bytes. Its identity-bound
// retrievability challenge fails, so the bounty is DENIED — and, crucially, NOT
// slashed: a retrievability shortfall may be transient, and only the mathematically
// attributable correctness lie is ever punished. This also pins the anti-double-
// count property: retrievability binds to the NAMED holder, so "the correct bytes
// exist somewhere in the swarm" does not pay.
func TestRedteamRepair_ComputeButDontStoreIsDenied(t *testing.T) {
	s := newRepairAdv(t, 43)
	s.fundEscrow(5_000_000)
	judge := s.careJudge()
	attacker := s.nodes[2]
	baseline := s.bond(attacker)

	pos, parityID, leafIdx := s.parityTarget()
	liar := s.nodes[9]
	liar.SetLiar(true)
	s.stageShardOn(judge, liar, parityID, pos, leafIdx)

	claim := repairproof.RepairClaim{
		Root: s.root, Stripe: 0, ShardPos: pos, ShardID: parityID, Holder: liar.ID(),
	}
	s.deliverClaim(judge, attacker.ID(), claim)

	if judge.Stats.BountiesReleased != 0 {
		t.Fatal("a data-less holder was paid — retrievability must bind to the named holder's OWN bytes")
	}
	if judge.Stats.FalseRepairSlashes != 0 {
		t.Fatal("a retrievability shortfall was slashed — only an attributable correctness lie is punished")
	}
	if paid := s.ledger.EscrowPaid(s.root); paid != 0 {
		t.Fatalf("bounty drawn for a data-less holder: %d", paid)
	}
	if got := s.ledger.Reputation(attacker.ID()); got != baseline {
		t.Fatalf("standing moved on a deny: %d != %d", got, baseline)
	}
}

// stageShardOn fetches the real shard from the swarm and re-places it on `holder`
// WITH a valid Merkle proof and PoR tags. An honest holder keeps the bytes (and can
// answer retrievability); a liar holder (SetLiar) keeps the receipt + tags but drops
// the bytes — the compute-but-don't-store adversary. The caller sets liar-ness.
func (s *repairAdv) stageShardOn(fetcher, holder *Node, id ports.ChunkID, pos, leafIdx int) {
	// A coded shard registers under its COLUMN key, not its own id, so resolve the
	// column's providers and fetch the specific shard from them.
	fetcher.resolveProviders(colKey(s.root, pos), func(provs []ports.NodeID) {
		fetcher.fetchFrom(id, provs, func(bool) {})
	})
	s.sched.Run()
	c, err := fetcher.Store().Get(bg(), id)
	if err != nil {
		panic("could not fetch the shard to stage on the liar")
	}
	pr, err := manifest.Prove(s.m.Leaves(), leafIdx)
	if err != nil {
		panic(err)
	}
	proof := &ports.StorageProof{
		Root: s.root, Index: pr.Index, Total: pr.Total, Path: pr.Path, Column: pos,
		PorTags: s.porKey.Tags(id[:], c.Data),
	}
	s.nodes[0].placeAt(id, c.Data, proof, []ports.NodeID{holder.ID()}, 1, nil, func(int) {})
	s.sched.Run()
}

// TestRedteamRepair_HonestClaimIsPaid is the positive control: the SAME judge that
// slashes a garbage claim and denies a data-less one PAYS a genuine repair — a
// correct shard id on a holder that really holds the bytes clears both legs and the
// bounty flows to the holder, still moving no standing. Without this, the deny/slash
// tests could be passing by rejecting everything.
func TestRedteamRepair_HonestClaimIsPaid(t *testing.T) {
	s := newRepairAdv(t, 44)
	s.fundEscrow(5_000_000)
	judge := s.careJudge()
	attacker := s.nodes[2] // an honest caretaker here; kept named for symmetry

	pos, parityID, leafIdx := s.parityTarget()
	holder := s.nodes[9] // honest: keeps the bytes
	holderStanding := s.bond(holder)
	holderBalance := s.ledger.Balance(holder.ID())
	s.stageShardOn(judge, holder, parityID, pos, leafIdx)

	claim := repairproof.RepairClaim{
		Root: s.root, Stripe: 0, ShardPos: pos, ShardID: parityID, Holder: holder.ID(),
	}
	s.deliverClaim(judge, attacker.ID(), claim)

	if judge.Stats.BountiesReleased != 1 {
		t.Fatalf("an honest repair was not paid: BountiesReleased=%d", judge.Stats.BountiesReleased)
	}
	if judge.Stats.FalseRepairSlashes != 0 {
		t.Fatal("an honest repair was slashed")
	}
	if paid := s.ledger.EscrowPaid(s.root); paid <= 0 {
		t.Fatalf("EscrowPaid = %d, want > 0", paid)
	}
	if got := s.ledger.Balance(holder.ID()); got <= holderBalance {
		t.Fatalf("holder balance did not rise: %d <= %d", got, holderBalance)
	}
	if got := s.ledger.Reputation(holder.ID()); got != holderStanding {
		t.Fatalf("holder standing moved from %d to %d on a paid bounty", holderStanding, got)
	}
}

// TestRedteamRepair_QuorumIsDiscoverableAcrossNodes (§11 d, availability side): the
// quorum a repairer broadcasts to is discovered through the careKey rendezvous, and
// EVERY caretaker that announces there is found — so honest caretakers can't be
// silently excluded from the vote by a repairer that only knows nodes near itself.
// (Full domain-DIVERSE quorum SELECTION — refusing to let one failure domain pack
// the vote — is future hardening; today the guarantee is that all announcers are
// reachable.) Uses real Care(), so it advances the clock a bounded amount rather
// than running to quiescence (the repair loop reschedules forever).
func TestRedteamRepair_QuorumIsDiscoverableAcrossNodes(t *testing.T) {
	s := newRepairAdv(t, 45)
	caretakers := []*Node{s.nodes[3], s.nodes[5], s.nodes[7]}
	want := map[ports.NodeID]bool{}
	for _, c := range caretakers {
		c.Care(s.reg, s.h.Care()) // real Care → announces under careKey
		want[c.ID()] = true
	}
	s.sched.RunUntil(s.sched.Now().Add(5 * ports.Second))

	// A repairer resolves the careKey rendezvous and must find every announcer.
	found := map[ports.NodeID]bool{}
	s.nodes[0].resolveProviders(ports.ChunkID(careKey(s.root)), func(provs []ports.NodeID) {
		for _, p := range provs {
			found[p] = true
		}
	})
	s.sched.RunUntil(s.sched.Now().Add(5 * ports.Second))

	for id := range want {
		if !found[id] {
			t.Fatalf("caretaker %s announced under careKey but was not discovered — the quorum would silently exclude it", id.String()[:8])
		}
	}
}
