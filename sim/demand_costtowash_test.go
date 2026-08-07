package sim

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"testing"

	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/core/demand"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/ports"
)

// TestDemandWashCostsRealFees is the D-DEMAND P3 cost-to-wash property (the "single
// most important knob"): a receipt CANNOT prove demand authenticity — a server that
// runs its own fetcher and fetches its own content mints perfectly valid receipts
// (Douceur). What the mechanism guarantees instead is that each fake receipt costs a
// real, BURNED retrieval fee: washing N demands burns N fees for zero standing (demand
// is neutral). This test pins exactly that — the receipts verify, the counter rises,
// the washer's balance drops by N·fee, and NO standing moves.
func TestDemandWashCostsRealFees(t *testing.T) {
	const seed = 20260809
	const fee = int64(1000)
	cl := NewCluster(seed, 8, simnet.DefaultConfig(), node.DefaultConfig())
	issuer, server, fetcher := cl.Nodes[0], cl.Nodes[1], cl.Nodes[2]

	// The issuer charges + BURNS the retrieval fee (ChargePublish debits, credits no
	// one) against the token requester's balance.
	ledger := credit.New(fee, 100*fee) // fee, and a grant that funds many withdrawals
	issuer.SetLedger(ledger)
	issuerKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	issuer.EnableTokenIssuer(issuerKey)

	// The washer controls BOTH the server (banks) and the fetcher (withdraws + acks) —
	// the strongest self-dealer. Bond the fetcher so a spurious standing motion would show.
	server.EnableDemandBank(&issuerKey.PublicKey)
	_, fetcherSigner, _ := ed25519.GenerateKey(rand.Reader)
	fetcher.SetSigner(fetcherSigner)
	ledger.Register(fetcher.ID())
	ledger.RecordBondChallenge(fetcher.ID(), fetcher.ID(), 64<<20, true, 1)
	baselineStanding := ledger.Reputation(fetcher.ID())
	startBalance := ledger.Balance(fetcher.ID())

	fetcher.FetchIssuerKey(issuer.ID(), func(error) {})
	cl.Sched.Run()

	data := make([]byte, 16<<10)
	cl.rng.Read(data)
	object := ports.HashBytes(data)

	// Wash N fake demands: each is a genuine, verifiable delivery receipt (a self-fetch
	// IS a real paid delivery — no receipt can tell otherwise).
	const N = 5
	washed := 0
	for i := 0; i < N; i++ {
		var tok demand.Token
		fetcher.AcquireDemandToken(rand.Reader, issuer.ID(), func(tk demand.Token, err error) {
			if err != nil {
				t.Fatalf("acquire %d: %v", i, err)
			}
			tok = tk
		})
		cl.Sched.Run()
		fetcher.SubmitDeliveryReceipt(server.ID(), tok, object, data, func(c bool, err error) {
			if err == nil && c {
				washed++
			}
		})
		cl.Sched.Run()
	}

	// The receipts are valid and the counter rises — authenticity is NOT provable.
	if washed != N || server.WitnessedDemand(object) != int64(N) {
		t.Fatalf("washed=%d witnessed=%d, want %d each (valid receipts are indistinguishable from honest)",
			washed, server.WitnessedDemand(object), N)
	}
	// But each cost a real burned fee — cost-to-wash is N·fee, the actual defense.
	if spent := startBalance - ledger.Balance(fetcher.ID()); spent != int64(N)*fee {
		t.Fatalf("wash burned %d credits, want %d (N·fee) — the fee must be the real cost", spent, int64(N)*fee)
	}
	// And it bought NOTHING that matters to consensus: demand is neutral.
	if got := ledger.Reputation(fetcher.ID()); got != baselineStanding {
		t.Fatalf("washing moved standing from %d to %d — demand must confer ZERO standing", baselineStanding, got)
	}
}
