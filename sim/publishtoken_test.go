package sim

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	mrand "math/rand"
	"testing"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/blindtoken"
	"github.com/nerolabs/silt/core/chain"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/core/genesis"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/ports"
)

// TestUnlinkablePublishViaQuorumToken is the end-to-end integration tier for
// publisher privacy (T3, #14/F1): a publisher acquires a publish token by
// collecting blind signatures from a QUORUM of validators over the node loop
// (paying the fee with its durable identity, but the issuers never see the
// serial), then publishes an entry that COMMITS carrying the token and NO
// Publisher NodeID — and the token's serial can be spent only once.
func TestUnlinkablePublishViaQuorumToken(t *testing.T) {
	const (
		seed = int64(3)
		V    = 3
		k    = 2
	)
	ledger := credit.New(50_000, 500_000) // fee, grant (enough to pay k issuers)
	repFn := func(n ports.NodeID) int64 { return ledger.Reputation(n) }
	issuerReg := map[ports.NodeID]*rsa.PublicKey{}
	issuerPub := func(id ports.NodeID) *rsa.PublicKey { return issuerReg[id] }
	cfg := chain.Config{MinProposerRep: 100, MinAttesterRep: 100, Quorum: k}

	sched := simclock.New()
	net := simnet.New(sched, seed, simnet.DefaultConfig())

	idents := make([]*identity.Identity, V)
	ids := make([]ports.NodeID, V)
	var nodes []*node.Node
	for i := 0; i < V; i++ {
		idents[i] = identity.FromSeed(seed*1000 + int64(i))
		ids[i] = idents[i].NodeID()
		st := memstore.New()
		nd := node.New(ids[i], node.DefaultConfig(), sched, net.Endpoint(ids[i]), st)
		nd.SetLedger(ledger)
		rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		nd.EnableTokenIssuer(rsaKey)
		issuerReg[ids[i]] = &rsaKey.PublicKey
		ledger.RecordBondChallenge(ids[i], 8<<20, true, 1) // bonded → qualified issuer/attester
		ch := chain.New(cfg, repFn)
		ch.RequireTokens(k, issuerPub)
		if gb, _, _, gerr := genesis.Build(st); gerr == nil {
			ch.AppendGenesis(gb)
		}
		nd.EnableChain(ch, idents[i].Signer())
		nodes = append(nodes, nd)
	}
	for i := 1; i < V; i++ {
		nodes[i].Bootstrap([]ports.NodeID{ids[0]}, func() {})
	}
	sched.Run()

	publisher := nodes[0]
	rng := mrand.New(mrand.NewSource(seed))
	serial, _ := blindtoken.NewSerial(rng)

	// Acquire a token from the OTHER validators (k blind signatures).
	var tok *ports.PublishToken
	var acqErr error
	acqDone := false
	publisher.AcquireToken(rng, serial, ids[1:], issuerPub, k, func(tk *ports.PublishToken, err error) {
		tok, acqErr, acqDone = tk, err, true
	})
	sched.Run()
	if !acqDone || acqErr != nil || tok == nil || len(tok.Sigs) < k {
		t.Fatalf("token acquisition failed: done=%v err=%v tok=%v", acqDone, acqErr, tok)
	}

	// Publish an entry carrying the token — NO Publisher identity.
	e := ports.Entry{
		Root:           ports.HashBytes([]byte("secret-doc")),
		ManifestChunks: []ports.ChunkID{ports.HashBytes([]byte("m"))},
		Token:          tok,
	}
	var propErr error
	propDone := false
	publisher.ProposeEntry(e, ids[1:], ids, k, func(err error) { propErr, propDone = err, true })
	sched.Run()
	if !propDone || propErr != nil {
		t.Fatalf("a token-carried publish should commit; done=%v err=%v", propDone, propErr)
	}

	// OUTCOME: committed, carrying the token and NO durable Publisher identity.
	got, ok := publisher.Chain().LookupRoot(e.Root)
	if !ok {
		t.Fatal("the published root did not commit")
	}
	if got.Publisher != (ports.NodeID{}) {
		t.Fatal("a token-authorized publish must carry NO Publisher identity (F1)")
	}
	if got.Token == nil {
		t.Fatal("the committed entry lost its publish token")
	}

	// OUTCOME: the serial can't be spent twice.
	reuse := ports.Entry{
		Root:           ports.HashBytes([]byte("reuse")),
		ManifestChunks: []ports.ChunkID{ports.HashBytes([]byte("m2"))},
		Token:          tok,
	}
	spent := false
	publisher.ProposeEntry(reuse, ids[1:], ids, k, func(err error) { propErr, spent = err, true })
	sched.Run()
	if !spent || !errors.Is(propErr, chain.ErrTokenSpent) {
		t.Fatalf("re-spending the token serial must be refused; got %v", propErr)
	}
}
