// Package credit is the v1 credit ledger: every byte served earns one
// credit, publishing costs a flat fee, and each registered node starts
// with a small grant.
//
// THIS IS DELIBERATELY GAMEABLE. Serving claims are self-reported by
// whoever calls RecordServe — two colluding nodes could ping-pong a
// chunk forever and mint credit, and nothing here would notice. That is
// fine: v1's job is to make the ECONOMICS OBSERVABLE in sim metrics
// (who earns, who freeloads, how unequal it gets), not to be secure.
// The cryptographic proof-of-retrieval that would make serving claims
// trustworthy plugs in behind the same ports.CreditLedger interface —
// the Merkle inclusion proofs from core/manifest are the intended
// building block.
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
}

// Ledger implements ports.CreditLedger plus the observability the sim's
// economy scenario reports on.
type Ledger struct {
	fee      int64
	grant    int64
	accounts map[ports.NodeID]*account
	order    []ports.NodeID // registration order: deterministic iteration

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

// Reputation condenses a node's observed history into the number the
// chain consults (M12): storage honesty weighs most, failed audits
// bite hard, and serving earns steadily — a hoarder that stores but
// never serves builds reputation slowly, per the freeloader doctrine.
// Like everything in this ledger it is naively gameable; the chain's
// protection is that each validator computes it from its OWN
// observations, so lying to one buys nothing with the others.
func (l *Ledger) Reputation(n ports.NodeID) int64 {
	a := l.acct(n)
	return int64(a.auditsPassed)*25 - int64(a.auditsFailed)*250 + a.servedBytes/(64<<10)
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
