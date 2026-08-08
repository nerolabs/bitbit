// Package credit is the v1 ledger. It runs TWO economies that earlier
// versions conflated — and the conflation was the wash-serving hole:
//
//   - BALANCES (RecordServe → 1 byte served = 1 credit, minus a publish
//     fee). Still self-reported and DELIBERATELY GAMEABLE: two colluding
//     nodes can ping-pong a chunk to mint credit and nothing here
//     notices. That is fine — balances fund the anti-spam publish fee and
//     drive the observatory's who-earns/who-freeloads metrics. They are
//     NOT a security boundary and never gate consensus.
//
//   - STANDING (Reputation → the number the chain gates writes on). This
//     is NO LONGER self-reported. It is built only on evidence a Sybil
//     cannot fabricate: challenged, identity-bound held storage
//     (RecordBondChallenge, backed by core/bond) and passed storage
//     audits (core/node/por.go). Wash-serving moves balances but buys
//     ZERO standing — which is what closes the reputation→quorum-capture
//     path (threat-catalog D1/D3): standing now costs real disk, not
//     chatter. See Reputation and RecordBondChallenge.
package credit

import (
	"sort"

	"github.com/nerolabs/silt/ports"
)

type account struct {
	balance      int64
	servedBytes  int64
	fetchedBytes int64
	auditsPassed int
	auditsFailed int
	// Storage-bond standing — the Sybil cost. bondedBytes is the size of
	// the identity-bound bond (core/bond) this node last PROVED it holds
	// under a random challenge; it is the only large, unforgeable input to
	// Reputation, so N identities cost N real bonds on real disk. bondFails
	// counts challenges it could not answer.
	//
	// TIME (T axis), stated honestly: the T factor is shipped for standing
	// RETENTION only — DecayStale + BondMaxAge (below) retire standing that is
	// not *continuously* re-proven, so a released plot cannot coast. There is
	// NO acquisition-time accrual: full standing is granted on the first passing
	// bond challenge, so acquisition is priced by D (real bonded bytes) alone —
	// a zero-age identity that proves the bytes reaches full standing at once.
	// A time-of-ACQUISITION ramp is deferred: a bare age gate is pre-farmable
	// (the coin-age anti-pattern), and the only sound form is a continuous
	// bond-anchored VDF (M1+). lastBondTick drives the retention decay.
	// firstSeenTick is recorded but is currently DEAD for standing — no
	// standing calculation reads it; it is not an acquisition-age gate.
	bondedBytes   int64
	bondFails     int
	firstSeenTick uint64 // recorded; NOT read by any standing calc (see T-axis note above)
	lastBondTick  uint64
	// equivocations counts PROVEN consensus double-signs (core/chain). It is
	// the gravest offense — an attack on consensus itself — so it does not
	// merely dent standing, it buries it below any threshold forever.
	equivocations int
	// falseRepairs counts PROVEN false repair claims (core/repairproof): a
	// caretaker claimed a durability bounty for a shard that fails the public
	// correctness recompute. Deliberate but bounded fraud — a large finite dent,
	// not the permanent burial equivocation earns.
	falseRepairs int
}

// Ledger implements ports.CreditLedger plus the observability the sim's
// economy scenario reports on.
type Ledger struct {
	fee      int64
	grant    int64
	accounts map[ports.NodeID]*account
	order    []ports.NodeID // registration order: deterministic iteration
	// rootOwner binds each bond root to the first identity that proved it, so
	// a bond root builds standing for AT MOST ONE identity. A colluding
	// operator pointing N identities at one shared plot therefore earns one
	// bond's standing, not N — each identity needs its own distinct plot
	// (distinct secret ⇒ distinct root). Honest identities never collide.
	rootOwner map[ports.Hash]ports.NodeID

	// escrow holds each object's durability reserve, keyed by the object's
	// root (its Merkle identity). It is the H7/S7 durability budget: prepaid
	// credit that pays repair bounties, topped up by an auto-skim of the
	// object's own serving revenue. It lives in the BALANCE economy — it
	// moves the credit unit between balances and reserves — and NEVER feeds
	// Reputation. That firewall is the one load-bearing H7 invariant (the
	// durability budget must confer ZERO consensus standing, or one physical
	// copy of a shared shard could answer for N pledges); it is asserted
	// structurally by the Invariant-A guard, which classifies every escrow
	// press `neutral`. See escrow.go.
	escrow map[ports.Hash]*objectEscrow

	// Audit economics: storage that survives a spot-check earns rent;
	// storage that turns out to be a lie is slashed hard. Balances may
	// go negative — debt is the scarlet letter. Exported so scenarios
	// can tune the pain.
	AuditReward int64
	AuditSlash  int64
}

var _ ports.CreditLedger = (*Ledger)(nil)

// New creates a ledger with the given publish fee. grant is the
// starting balance handed to each node on Register — the faucet that
// bootstraps a fresh economy (with zero grants, nobody could ever
// publish the first file).
func New(fee, grant int64) *Ledger {
	return &Ledger{
		fee: fee, grant: grant,
		accounts:    make(map[ports.NodeID]*account),
		rootOwner:   make(map[ports.Hash]ports.NodeID),
		escrow:      make(map[ports.Hash]*objectEscrow),
		AuditReward: 1_000,
		AuditSlash:  25_000,
	}
}

// Register creates the node's account and applies the starting grant.
// Registering twice is a no-op (no double grants).
func (l *Ledger) Register(n ports.NodeID) {
	if _, ok := l.accounts[n]; ok {
		return
	}
	l.accounts[n] = &account{balance: l.grant}
	l.order = append(l.order, n)
}

func (l *Ledger) acct(n ports.NodeID) *account {
	l.Register(n)
	return l.accounts[n]
}

func (l *Ledger) RecordServe(server, requester ports.NodeID, _ ports.ChunkID, bytes int64) {
	if bytes <= 0 || server == requester {
		return // self-serving earns nothing (the cheapest gaming blocked)
	}
	s := l.acct(server)
	s.balance += bytes // 1 byte served = 1 credit
	s.servedBytes += bytes
	l.acct(requester).fetchedBytes += bytes
}

func (l *Ledger) RecordAudit(prover ports.NodeID, _ ports.ChunkID, passed bool) {
	a := l.acct(prover)
	if passed {
		a.balance += l.AuditReward
		a.auditsPassed++
	} else {
		a.balance -= l.AuditSlash
		a.auditsFailed++
	}
}

func (l *Ledger) Audits(n ports.NodeID) (passed, failed int) {
	a := l.acct(n)
	return a.auditsPassed, a.auditsFailed
}

// RecordBondChallenge settles one storage-bond challenge (core/bond): the
// prover either answered a random challenge on its identity-bound bond of
// provenBytes, or failed to. Passing sets the node's challenged-storage
// standing — the large, unforgeable term Reputation is built on; failing
// zeroes it (a bond you cannot answer buys nothing). tick is a monotonic
// counter (the auditor's request clock, exactly like the PoR nonce in
// por.go) so DecayStale can retire standing that stops being re-proven.
//
// NOTE: intentionally NOT (yet) on ports.CreditLedger. The bond auditor
// reaches it through an optional interface (a type assertion) so this
// lands without touching every CreditLedger implementer; promote it to
// the port once the auditor is wired in core/node/por.go.
func (l *Ledger) RecordBondChallenge(prover ports.NodeID, root ports.Hash, provenBytes int64, passed bool, tick uint64) {
	a := l.acct(prover)
	if a.firstSeenTick == 0 {
		a.firstSeenTick = tick
	}
	if passed {
		// Root-owner dedup (see rootOwner): a bond root credits standing to at
		// most one identity, so a colluding operator cannot amortise one plot
		// across N identities. The FIRST identity to prove a root owns it; a
		// later identity advertising the SAME root earns nothing. Only the
		// true owner can produce the plot to answer challenges (core/bond seals
		// from a per-identity secret), so an outsider cannot grief a victim by
		// pre-claiming its root.
		if root != (ports.Hash{}) {
			if owner, ok := l.rootOwner[root]; ok && owner != prover {
				a.bondedBytes = 0 // root already backs another identity's standing
				return
			}
			l.rootOwner[root] = prover
		}
		a.bondedBytes = provenBytes
		a.lastBondTick = tick
		return
	}
	a.bondedBytes = 0
	a.bondFails++
}

// equivocationSlash is the reputation penalty per proven double-sign — large
// enough to bury any conceivable earned standing, so an equivocator is barred
// from proposing and attesting and its attestations stop counting toward any
// fork's weight. Double-signing attacks consensus itself; it costs everything.
const equivocationSlash = 1 << 40

// SlashEquivocation records a proven consensus double-sign against a validator
// (evidence verified by chain.VerifyEquivocation before this is called). It
// zeroes the node's bonded standing and applies a crushing, permanent
// reputation penalty, so the equivocator can no longer influence consensus.
func (l *Ledger) SlashEquivocation(id ports.NodeID) {
	a := l.acct(id)
	a.equivocations++
	a.bondedBytes = 0
}

// falseRepairSlash is the reputation penalty per PROVEN false repair claim. A
// caretaker claimed a durability bounty (core/credit escrow) for a shard that
// fails the public correctness recompute (core/repairproof), and that
// non-verifying transcript is the attributable fraud proof. Unlike an
// equivocation — which buries standing forever because it attacks consensus
// itself — a false repair is deliberate but bounded fraud, so the penalty is
// large but FINITE. The magnitude is a tuning parameter (Evolving, per the
// tenets): it must exceed the standing a typical attester earns and make a false
// claim strictly -EV against the bounty it targets (bounty-relative calibration
// is an open item, docs/design/h7-proof-of-repair.md §12).
const falseRepairSlash = 1_000_000

// SlashFalseRepair records a PROVEN false repair claim against a caretaker
// (the failing correctness transcript verified by core/repairproof before this is
// called). It applies a crushing but finite reputation penalty. Like every slash
// it can only ever LOWER standing — a reduces-class press under Invariant A, never
// Sybil-amplifiable. It does NOT touch bondedBytes: the storage bond is
// independent of the repair lie, so only the earned standing is docked, and an
// honest node that later re-proves its bond is unaffected by the (finite) dent.
func (l *Ledger) SlashFalseRepair(id ports.NodeID) {
	l.acct(id).falseRepairs++
}

// DecayStale zeroes any standing whose last passing bond-challenge is
// older than maxAge, so a node that stops answering loses standing
// without anyone having to catch it lying. The validator/caretaker loop
// calls it with a monotonic now. This is what makes standing an integral
// over *sustained* proof rather than a one-time pass.
func (l *Ledger) DecayStale(now, maxAge uint64) {
	for _, a := range l.accounts {
		if a.bondedBytes > 0 && now > a.lastBondTick && now-a.lastBondTick > maxAge {
			a.bondedBytes = 0
		}
	}
}

// bondUnit converts bonded bytes into standing points: one point per
// 64 KiB of continuously-proven, identity-bound storage. This is the
// exchange rate between real disk and consensus weight — a tuning
// parameter (Evolving, per the tenets), NOT a fixed law.
const bondUnit = 64 << 10

// Reputation condenses a node's observed history into the number the
// chain consults (M12). It is built ONLY on evidence a node cannot
// fabricate, and — critically — it is minted by exactly ONE press:
//
//   - challenged, identity-bound held storage (bondedBytes, core/bond) —
//     the Sybil cost: N identities need N real bonds on real disk. This is
//     the ONLY term that GRANTS standing; minus
//   - failed audits and failed bond challenges, which bite hard.
//
// PoR audits (por.go) DO NOT grant Sybil-resistant standing (M0 hardening
// H1 / red-team RT-1). Plain proof-of-retrievability over shared, erasure-
// coded content proves POSSESSION of the bytes, not a DISTINCT physical
// replica: a data-less identity can RELAY a real holder's aggregated
// response and pass, because the proof is a pure function of (chunkID,
// challenge, data) — not bound to the prover (see docs/design/
// m0-hardening-strategy.md §4 S2, research memo 03). So a "+25 per pass"
// mint let a disk-less Sybil farm reach propose/attest eligibility with
// ZERO storage. Audits now fund only the BALANCE economy (RecordAudit
// credits balance) and act as a NEGATIVE integrity signal here — a failed
// audit on shards you CLAIMED to hold subtracts standing (it can never be
// Sybil-amplified, since it only ever reduces). Standing that gates
// consensus rests on the bond alone. Self-reported serving (servedBytes)
// is likewise not here: it funds the balance economy, not standing.
//
// Invariant A (docs/design/m0-hardening-strategy.md §2): no standing
// without a verified, identity-bound, deduped, bond-gated proof. Only the
// bond press satisfies it; the audit press is therefore denied a grant.
func (l *Ledger) Reputation(n ports.NodeID) int64 {
	a := l.acct(n)
	return a.bondedBytes/bondUnit -
		int64(a.auditsFailed)*250 -
		int64(a.bondFails)*250 -
		int64(a.falseRepairs)*falseRepairSlash -
		int64(a.equivocations)*equivocationSlash
}

func (l *Ledger) Balance(n ports.NodeID) int64      { return l.acct(n).balance }
func (l *Ledger) CanPublish(n ports.NodeID) bool    { return l.acct(n).balance >= l.fee }
func (l *Ledger) Fee() int64                        { return l.fee }
func (l *Ledger) ServedBytes(n ports.NodeID) int64  { return l.acct(n).servedBytes }
func (l *Ledger) FetchedBytes(n ports.NodeID) int64 { return l.acct(n).fetchedBytes }

func (l *Ledger) ChargePublish(n ports.NodeID) error {
	a := l.acct(n)
	if a.balance < l.fee {
		return ports.ErrInsufficientCredit
	}
	a.balance -= l.fee
	return nil
}

// Balances returns every registered node's balance in registration
// order.
func (l *Ledger) Balances() []int64 {
	out := make([]int64, len(l.order))
	for i, n := range l.order {
		out[i] = l.accounts[n].balance
	}
	return out
}

// Gini computes the Gini coefficient of the current balances: 0 means
// perfect equality, values toward 1 mean a few nodes hold everything.
// (Formula: mean absolute difference between all pairs, divided by
// twice the mean. Computed via the sorted form
// G = Σᵢ (2i − n − 1)·xᵢ / (n·Σ xᵢ) with 1-based ranks i.)
func (l *Ledger) Gini() float64 {
	return Gini(l.Balances())
}

func Gini(values []int64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	n := int64(len(sorted))
	var sum, weighted int64
	for i, v := range sorted {
		sum += v
		weighted += (2*int64(i+1) - n - 1) * v
	}
	if sum == 0 {
		return 0 // universal poverty is technically equality
	}
	return float64(weighted) / (float64(n) * float64(sum))
}
