package sim

import (
	"crypto/rand"
	"crypto/rsa"
	mrand "math/rand"
	"testing"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/blindtoken"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/ports"
)

// Integration/sim tier for the M0 privacy fee decoupling (red-team F4). The unit
// tests (core/node/redteam_privacy_test.go) call the issuer handler directly;
// this drives the OUTCOME through the real node loop over the sim transport
// (MsgTokenRequest → answerTokenRequest → ledger). What good looks like: a
// publisher pays the fee ONCE, in bulk, at mint; publishing later by SPENDING a
// prepaid credit does NOT debit its durable standing key again — so no
// per-publish ledger event links the standing key to the publish.
func TestPrepaidCreditDecouplesFeeOverTheNetwork(t *testing.T) {
	const (
		seed = int64(31)
		fee  = int64(100)
		mint = 3
	)
	ledger := credit.New(fee, 500_000) // shared ledger; fee, grant
	issuerReg := map[ports.NodeID]*rsa.PublicKey{}
	issuerPub := func(id ports.NodeID) *rsa.PublicKey { return issuerReg[id] }

	sched := simclock.New()
	net := simnet.New(sched, seed, simnet.DefaultConfig())

	// The issuer validator.
	issuerIdent := identity.FromSeed(seed*1000 + 1)
	issuerID := issuerIdent.NodeID()
	issuer := node.New(issuerID, node.DefaultConfig(), sched, net.Endpoint(issuerID), memstore.New())
	issuer.SetLedger(ledger)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	issuer.EnableTokenIssuer(rsaKey)
	issuerReg[issuerID] = &rsaKey.PublicKey

	// The publisher (its durable standing key is what must not be re-charged).
	pubIdent := identity.FromSeed(seed*1000 + 2)
	durable := pubIdent.NodeID()
	publisher := node.New(durable, node.DefaultConfig(), sched, net.Endpoint(durable), memstore.New())
	publisher.SetLedger(ledger)
	publisher.Bootstrap([]ports.NodeID{issuerID}, func() {})
	sched.Run()

	ledger.Register(durable)
	start := ledger.Balance(durable)
	rng := mrand.New(mrand.NewSource(seed))

	// MINT: acquire prepaid credits over the wire — the fee is charged here, in
	// bulk, decoupled from any specific later publish.
	var credits []ports.PublishCredit
	done := false
	publisher.AcquireCredits(rng, issuerID, mint, issuerPub, func(cs []ports.PublishCredit, err error) {
		credits, done = cs, true
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
	})
	sched.Run()
	if !done || len(credits) != mint {
		t.Fatalf("expected %d prepaid credits minted over the wire, got %d", mint, len(credits))
	}
	afterMint := ledger.Balance(durable)
	if charged := start - afterMint; charged != int64(mint)*fee {
		t.Fatalf("mint should charge %d× the fee in bulk, charged %d", mint, charged)
	}

	// PUBLISH: spend one credit for a publish-token signature over the wire.
	serial, _ := blindtoken.NewSerial(rng)
	var tok *ports.PublishToken
	done = false
	publisher.AcquireTokenWithCredits(rng, serial, []ports.NodeID{issuerID}, issuerPub,
		map[ports.NodeID]ports.PublishCredit{issuerID: credits[0]}, 1,
		func(tk *ports.PublishToken, err error) {
			tok, done = tk, true
			if err != nil {
				t.Fatalf("credit-backed acquisition: %v", err)
			}
		})
	sched.Run()
	if !done || tok == nil || len(tok.Sigs) < 1 {
		t.Fatal("a credit-backed publish token should be acquired over the wire")
	}
	if !blindtoken.Verify(issuerPub(issuerID), serial, tok.Sigs[0].Sig) {
		t.Fatal("the issued publish token must verify")
	}

	// OUTCOME: publishing by spending a prepaid credit did NOT debit the durable
	// standing key — the ledger-level link the red-team exploited is severed.
	if ledger.Balance(durable) != afterMint {
		t.Fatalf("F4 integration: a credit-backed publish must not debit the durable identity over the wire "+
			"(balance %d, want %d)", ledger.Balance(durable), afterMint)
	}

	// And a spent credit cannot buy a second token (double-spend refused live).
	serial2, _ := blindtoken.NewSerial(rng)
	var tok2 *ports.PublishToken
	done = false
	publisher.AcquireTokenWithCredits(rng, serial2, []ports.NodeID{issuerID}, issuerPub,
		map[ports.NodeID]ports.PublishCredit{issuerID: credits[0]}, 1,
		func(tk *ports.PublishToken, _ error) { tok2, done = tk, true })
	sched.Run()
	if !done || tok2 != nil {
		t.Fatal("F4 integration: re-spending a credit over the wire must be refused (double-spend)")
	}
}
