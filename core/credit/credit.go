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
	// counts challenges it could not answer. firstSeen/lastBond bound when
	// standing began and was last refreshed, so DecayStale can retire it —
	// standing must be *continuously* re-proven, making time-in-good-
	// standing the scarce resource (you cannot buy last month's uptime).
	bondedBytes   int64
	bondFails     int
	firstSeenTick uint64
	lastBondTick  uint64
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
// fabricate:
//
//   - challenged, identity-bound held storage (bondedBytes, core/bond) —
//     the Sybil cost: N identities need N real bonds on real disk;
//   - passed storage audits (por.go), which a liar without the bytes
//     cannot answer; minus
//   - failed audits and failed bond challenges, which bite hard.
//
// Self-reported serving (servedBytes / RecordServe) is DELIBERATELY NOT
// here anymore: a wash-serving Sybil ring can move it freely, so it funds
// the balance economy and the observatory but buys zero standing. Each
// validator still computes this from its OWN observations, so lying to
// one buys nothing with the others.
func (l *Ledger) Reputation(n ports.NodeID) int64 {
	a := l.acct(n)
	return a.bondedBytes/bondUnit +
		int64(a.auditsPassed)*25 -
		int64(a.auditsFailed)*250 -
		int64(a.bondFails)*250
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
