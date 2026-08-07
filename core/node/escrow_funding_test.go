package node

import (
	"errors"
	"testing"

	"github.com/nerolabs/silt/ports"
)

// TestFundDurability_MovesBalanceIntoReserve: the publisher/operator funding API
// prepays an object's repair budget from the node's own balance, leaves standing
// untouched, and surfaces the reserve through DurabilityReserve.
func TestFundDurability_MovesBalanceIntoReserve(t *testing.T) {
	nd, l := mkJudge(t, 100) // grant 1_000_000 balance, ledger wired
	l.Register(nd.ID())
	standing := bondedStanding(l, nd.ID())
	before := l.Balance(nd.ID())

	var root ports.Hash
	root[0] = 0x7A
	if err := nd.FundDurability(root, 250_000); err != nil {
		t.Fatalf("fund: %v", err)
	}
	if got := before - l.Balance(nd.ID()); got != 250_000 {
		t.Fatalf("balance moved by %d, want 250000", got)
	}
	if got := nd.DurabilityReserve(root); got != 250_000 {
		t.Fatalf("reserve = %d, want 250000", got)
	}
	if got := l.EscrowFunded(root); got != 250_000 {
		t.Fatalf("EscrowFunded = %d, want 250000", got)
	}
	if got := l.Reputation(nd.ID()); got != standing {
		t.Fatalf("funding moved standing from %d to %d — it must be a pure balance move", standing, got)
	}
}

// TestFundDurability_NoLedgerIsAnError: funding a node with no ledger wired fails
// loudly rather than silently dropping the credit.
func TestFundDurability_NoLedgerIsAnError(t *testing.T) {
	nd, _ := mkJudge(t, 101)
	nd.SetLedger(nil)
	var root ports.Hash
	if err := nd.FundDurability(root, 1000); !errors.Is(err, ErrNoLedger) {
		t.Fatalf("want ErrNoLedger, got %v", err)
	}
	if got := nd.DurabilityReserve(root); got != 0 {
		t.Fatalf("reserve with no ledger = %d, want 0", got)
	}
}

// TestFundDurability_InsufficientCreditIsNoOp: a node that can't cover the amount
// gets ErrInsufficientCredit and nothing moves.
func TestFundDurability_InsufficientCreditIsNoOp(t *testing.T) {
	nd, l := mkJudge(t, 102)
	var root ports.Hash
	root[0] = 0x9B
	huge := l.Balance(nd.ID()) + 1 // one credit past what the node holds
	if err := nd.FundDurability(root, huge); !errors.Is(err, ports.ErrInsufficientCredit) {
		t.Fatalf("want ErrInsufficientCredit, got %v", err)
	}
	if got := l.EscrowFunded(root); got != 0 {
		t.Fatalf("insufficient funding still moved credit: EscrowFunded %d", got)
	}
}
