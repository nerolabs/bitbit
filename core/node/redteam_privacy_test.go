package node

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/blindtoken"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/ports"
)

// These are the M0 red-team privacy PoC (F4) INVERTED as a regression: the fee
// debit that let a colluding issuer tie a publish to the durable standing key is
// severed by prepaid credits — the fee is paid in bulk at mint, and a publish
// spends a credit with NO per-publish debit. See docs/design/m0-privacy-issuance.md
// §5 and docs/reviews/M0-REDTEAM-REPORT.md §4. (The network-layer link —
// relay/ephemeral transport + epoch batching — is the remaining D3 residual.)

func newIssuerNode(t *testing.T, fee int64) (*Node, *credit.Ledger, *rsa.PrivateKey) {
	t.Helper()
	sched := simclock.New()
	net := simnet.New(sched, 1, simnet.DefaultConfig())
	ledger := credit.New(fee, 50_000)
	id := identity.FromSeed(1)
	nd := New(id.NodeID(), DefaultConfig(), sched, net.Endpoint(id.NodeID()), memstore.New())
	nd.SetLedger(ledger)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	nd.EnableTokenIssuer(key)
	return nd, ledger, key
}

// The crux (F4): minting a credit charges the fee ONCE, and spending it for a
// publish token does NOT charge the durable identity again — so no per-publish
// debit links the standing key to the publish.
func TestRedteamF4_CreditDecouplesFeeFromPublish(t *testing.T) {
	const fee = 100
	nd, ledger, key := newIssuerNode(t, fee)
	pub := &key.PublicKey
	durable := identity.FromSeed(2).NodeID()
	ledger.Register(durable)
	start := ledger.Balance(durable)

	// MINT: a normal, charged token request blinded in the CREDIT domain.
	cs, _ := blindtoken.NewSerial(rand.Reader)
	cblind, csecret, err := blindtoken.BlindCredit(rand.Reader, pub, cs)
	if err != nil {
		t.Fatal(err)
	}
	mint := nd.answerTokenRequest(durable, ports.Message{Data: cblind})
	if !mint.OK {
		t.Fatal("mint (a charged credit request) should succeed")
	}
	credit := ports.PublishCredit{Serial: cs, Sig: blindtoken.Unblind(pub, mint.Data, csecret)}
	if !blindtoken.VerifyCredit(pub, credit.Serial, credit.Sig) {
		t.Fatal("minted credit must verify under the credit domain")
	}
	if charged := start - ledger.Balance(durable); charged != fee {
		t.Fatalf("mint should charge the fee once: charged %d, want %d", charged, fee)
	}
	afterMint := ledger.Balance(durable)

	// PUBLISH: spend the credit for a publish token — NO further debit.
	ts, _ := blindtoken.NewSerial(rand.Reader)
	tblind, tsecret, err := blindtoken.Blind(rand.Reader, pub, ts)
	if err != nil {
		t.Fatal(err)
	}
	pubReply := nd.answerTokenRequest(durable, ports.Message{Data: tblind, Credit: &credit})
	if !pubReply.OK {
		t.Fatal("publishing with a valid credit should succeed")
	}
	if tsig := blindtoken.Unblind(pub, pubReply.Data, tsecret); !blindtoken.Verify(pub, ts, tsig) {
		t.Fatal("the issued publish token must verify under the token domain")
	}
	if ledger.Balance(durable) != afterMint {
		t.Fatalf("F4 regression: publishing with a credit charged the durable identity again (balance %d, want %d)",
			ledger.Balance(durable), afterMint)
	}

	// DOUBLE-SPEND: the same credit cannot buy a second token.
	ts2, _ := blindtoken.NewSerial(rand.Reader)
	tblind2, _, _ := blindtoken.Blind(rand.Reader, pub, ts2)
	if r := nd.answerTokenRequest(durable, ports.Message{Data: tblind2, Credit: &credit}); r.OK {
		t.Fatal("F4 regression: a spent credit was accepted a second time (double-spend)")
	}
}

// Domain separation: a credit signature can never be presented as a publish
// token, nor a token as a credit — so one issuer key safely mints both without a
// minted credit doubling as a free token.
func TestRedteamF4_CreditAndTokenDomainsAreDistinct(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pub := &key.PublicKey
	serial, _ := blindtoken.NewSerial(rand.Reader)

	cb, cs, _ := blindtoken.BlindCredit(rand.Reader, pub, serial)
	csig := blindtoken.Unblind(pub, blindtoken.SignBlinded(key, cb), cs)
	if !blindtoken.VerifyCredit(pub, serial, csig) {
		t.Fatal("a credit signature must verify as a credit")
	}
	if blindtoken.Verify(pub, serial, csig) {
		t.Fatal("a credit signature must NOT verify as a publish token")
	}

	tb, ts, _ := blindtoken.Blind(rand.Reader, pub, serial)
	tsig := blindtoken.Unblind(pub, blindtoken.SignBlinded(key, tb), ts)
	if !blindtoken.Verify(pub, serial, tsig) {
		t.Fatal("a token signature must verify as a token")
	}
	if blindtoken.VerifyCredit(pub, serial, tsig) {
		t.Fatal("a token signature must NOT verify as a credit")
	}
}

// A forged credit is refused, and a refused credit request charges nobody (the
// requester meant to spend a credit, not pay).
func TestRedteamF4_ForgedCreditRefused(t *testing.T) {
	nd, ledger, key := newIssuerNode(t, 100)
	pub := &key.PublicKey
	durable := identity.FromSeed(2).NodeID()
	ledger.Register(durable)
	before := ledger.Balance(durable)

	forged := ports.PublishCredit{Serial: mustSerial(t), Sig: []byte("not a real signature")}
	ts := mustSerial(t)
	tblind, _, _ := blindtoken.Blind(rand.Reader, pub, ts)
	if r := nd.answerTokenRequest(durable, ports.Message{Data: tblind, Credit: &forged}); r.OK {
		t.Fatal("F4 regression: a forged credit was accepted")
	}
	if ledger.Balance(durable) != before {
		t.Fatal("a refused credit request must not charge the durable identity")
	}
}

// Legacy path unchanged: with no credit attached, the durable identity is charged
// exactly as before (zero blast radius on existing token flows).
func TestRedteamF4_NoCreditChargesLegacy(t *testing.T) {
	const fee = 100
	nd, ledger, key := newIssuerNode(t, fee)
	pub := &key.PublicKey
	durable := identity.FromSeed(2).NodeID()
	ledger.Register(durable)
	before := ledger.Balance(durable)

	ts := mustSerial(t)
	tblind, _, _ := blindtoken.Blind(rand.Reader, pub, ts)
	if r := nd.answerTokenRequest(durable, ports.Message{Data: tblind}); !r.OK {
		t.Fatal("a legacy uncredited request should succeed")
	}
	if charged := before - ledger.Balance(durable); charged != fee {
		t.Fatalf("a legacy uncredited request should charge the fee: charged %d, want %d", charged, fee)
	}
}

func mustSerial(t *testing.T) []byte {
	t.Helper()
	s, err := blindtoken.NewSerial(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
