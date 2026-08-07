package credit

// Durability escrow — the H7/S7 funding layer (issue #95, docs/decisions.md
// D-S7). The repair loop that keeps content alive under churn must be paid in
// equilibrium, not charity — the wound that killed Freenet/GNUnet. This file is
// the accounting for that: a per-object credit reserve that pays repair
// bounties, kept solvent by an auto-skim of the object's own serving revenue,
// with a rarest-shard multiplier that pays most to repair the stripe closest to
// data loss.
//
// THE ONE LOAD-BEARING INVARIANT. Escrow lives in the BALANCE economy: it moves
// the credit unit between node balances and object reserves, and that is ALL it
// does. No field here ever feeds Reputation (core/credit.go). The durability
// budget confers ZERO consensus standing. If it did, the shared-content sealing
// hole (γ→1/N) would re-open: one physical copy of an erasure-coded shard could
// answer for N pledges and buy N nodes' standing. Standing is minted by the bond
// press alone (RecordBondChallenge). Every method in this file is classified
// `neutral` by the Invariant-A guard (invariant_a_test.go), and that guard fails
// the build if a new escrow method ships unclassified — so the firewall cannot
// silently erode.
//
// Prototype-first (this is H7 slice 1): these are the ledger primitives. Wiring
// the auto-skim into the live serve path (node.go) and gating PayBounty on a
// verified proof-of-repair transcript are later slices; here PayBounty trusts
// its caller, and RecordServeToObject is the object-aware serve the wiring will
// call.

import "github.com/nerolabs/silt/ports"

// objectEscrow is one object's durability reserve. balance is the credit
// currently available to pay repair bounties; funded and paid are lifetime
// totals kept for the horizon math (H7 slice 3, instrument g) and the
// observatory. None of these is ever read by Reputation.
type objectEscrow struct {
	balance int64 // credits available now to pay repair bounties
	funded  int64 // lifetime credits deposited (prepay + auto-skim)
	paid    int64 // lifetime bounties paid out to repairers
}

// Skim is the protocol-fixed fraction of an object's serving revenue that routes
// back into that object's durability escrow, expressed as SkimNum/SkimDen. This
// is what makes popular data self-fund its own durability (every fetch tops up
// the reserve) while cold data draws down what it prepaid. It is a tuning
// parameter (Evolving, per the tenets), not a fixed law — 1/8 is a starting
// point; the value that actually matters is g (slice 3), the credit-cost trend
// of a shard-repair, which decides perpetual-vs-finite.
const (
	SkimNum = 1
	SkimDen = 8
)

// escrowFor returns the object's reserve, creating an empty one on first touch.
func (l *Ledger) escrowFor(root ports.Hash) *objectEscrow {
	e, ok := l.escrow[root]
	if !ok {
		e = &objectEscrow{}
		l.escrow[root] = e
	}
	return e
}

// FundEscrow moves amount credits from funder's BALANCE into object root's
// durability reserve — the publisher (or anyone) prepaying a repair budget. It
// returns ErrInsufficientCredit without side effects if the funder cannot cover
// it, and ignores a non-positive amount. It never touches standing: the funder's
// bonded storage, and therefore its Reputation, is unchanged.
func (l *Ledger) FundEscrow(root ports.Hash, funder ports.NodeID, amount int64) error {
	if amount <= 0 {
		return nil
	}
	a := l.acct(funder)
	if a.balance < amount {
		return ports.ErrInsufficientCredit
	}
	a.balance -= amount
	e := l.escrowFor(root)
	e.balance += amount
	e.funded += amount
	return nil
}

// RecordServeToObject is the object-aware serve: like RecordServe, it credits the
// server for delivering bytes of a chunk to a requester, but it diverts an
// auto-skim (SkimNum/SkimDen) of that revenue into object root's durability
// escrow so popular data pays for its own repair. The server keeps the net; the
// skim funds the reserve. It returns the credits skimmed. Self-serving earns and
// skims nothing (the cheapest gaming, blocked exactly as in RecordServe). Bytes
// served is still counted in full for observability — the work happened; a slice
// of the reward just funds durability. Standing is untouched: serving funds the
// balance economy, never Reputation (wash-serving buys zero standing).
func (l *Ledger) RecordServeToObject(server, requester ports.NodeID, root ports.Hash, id ports.ChunkID, bytes int64) int64 {
	if bytes <= 0 || server == requester {
		return 0
	}
	skim := bytes * SkimNum / SkimDen
	s := l.acct(server)
	s.balance += bytes - skim // 1 byte served = 1 credit, less the durability skim
	s.servedBytes += bytes
	l.acct(requester).fetchedBytes += bytes
	e := l.escrowFor(root)
	e.balance += skim
	e.funded += skim
	return skim
}

// PayBounty releases up to amount credits from object root's durability escrow to
// a repairer, crediting the repairer's BALANCE — durability work earns spendable
// credit, never standing. It pays min(amount, available): when the reserve is
// short it pays what is left and returns that, which is exactly the object's
// funded horizon running out (finite-but-renewable; re-endow before expiry). It
// returns the credits actually paid.
//
// SLICE-1 CONTRACT: the caller is trusted to invoke this only on a repair it has
// VERIFIED. The proof-of-repair gate — bounty releases iff BOTH correctness and
// retrievability verify, and a false claim is bond-slashed — is H7 slice 2 and
// wraps this primitive; it is not enforced here.
func (l *Ledger) PayBounty(root ports.Hash, repairer ports.NodeID, amount int64) int64 {
	if amount <= 0 {
		return 0
	}
	e, ok := l.escrow[root]
	if !ok || e.balance <= 0 {
		return 0
	}
	if amount > e.balance {
		amount = e.balance // pay what the reserve can cover; the horizon is spent
	}
	e.balance -= amount
	e.paid += amount
	l.acct(repairer).balance += amount
	return amount
}

// BountyFor is the rarest-shard bounty multiplier: the credits owed to repair one
// shard of a stripe that currently has `reachable` of its `n` shards alive, given
// a base bounty. The multiplier rises with how under-replicated the stripe is —
// every shard already lost adds one base unit, so repairing the last spare before
// unrecoverable data loss is worth (n-k+1)× a top-up on a healthy stripe. This is
// what steers scarce repair effort at the stripes nearest the cliff first.
//
// Formally: lost = n - reachable, and bounty = base * (lost + 1), clamped so a
// stripe at or below the k-floor (already unrecoverable-adjacent) pays the
// maximum (n-k+1)× rather than running past it. base<=0 or a degenerate stripe
// (k<=0, n<k) yields 0.
func BountyFor(base int64, k, n, reachable int) int64 {
	if base <= 0 || k <= 0 || n < k {
		return 0
	}
	lost := n - reachable
	if lost < 0 {
		lost = 0 // a stripe reporting more than n reachable pays the base
	}
	if maxLost := n - k; lost > maxLost {
		lost = maxLost // past the k-floor the multiplier is already maxed
	}
	return base * int64(lost+1)
}

// EscrowBalance is the credit currently available to pay repair bounties for
// object root — its remaining funded horizon in credits. Observability; reading
// it moves nothing.
func (l *Ledger) EscrowBalance(root ports.Hash) int64 {
	if e, ok := l.escrow[root]; ok {
		return e.balance
	}
	return 0
}

// EscrowFunded is the lifetime credits deposited into object root's reserve
// (prepay plus auto-skim) — the numerator the horizon math draws on.
func (l *Ledger) EscrowFunded(root ports.Hash) int64 {
	if e, ok := l.escrow[root]; ok {
		return e.funded
	}
	return 0
}

// EscrowPaid is the lifetime bounties paid out of object root's reserve — the
// realised cost of keeping it alive so far (the g-instrument reads this).
func (l *Ledger) EscrowPaid(root ports.Hash) int64 {
	if e, ok := l.escrow[root]; ok {
		return e.paid
	}
	return 0
}
