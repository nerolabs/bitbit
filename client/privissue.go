// Package client holds fetcher-side helpers that compose the node with real transport
// for privacy operations a long-lived daemon does not perform directly.
//
// D3 issuance-mixing (#179) — slice 1, the ephemeral-identity withdrawal. The token
// issuer authenticates whoever dials it via the end-to-end TLS handshake, so a demand
// withdrawal made over a fetcher's durable identity LINKS that withdrawal to the
// fetcher — the blind signature hides only the token serial, not the network identity.
// This severs that link: the withdrawal is made over a FRESH EPHEMERAL identity (a
// throwaway keypair) and paid with a PREPAID BLIND CREDIT rather than a durable account,
// so the issuer authenticates only an unlinkable ephemeral key and charges no account it
// can tie to the fetcher. Relay-routing (the IP link, slice 2) and epoch-batching
// (timing, deferred to the H8 mixnet) are the further D3 hardening.
package client

import (
	"crypto/rsa"
	"fmt"
	"io"
	"time"

	"github.com/nerolabs/silt/adapters/eventloop"
	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/tcpnet"
	"github.com/nerolabs/silt/adapters/walltime"
	"github.com/nerolabs/silt/core/demand"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/ports"
)

// WithdrawDemandTokenPrivately performs a D3 private demand-token withdrawal from the
// issuer at issuerAddr, paying with a prepaid blind credit. It spins up a one-off
// ephemeral identity + transport, withdraws over it, and tears everything down on
// return. It returns the withdrawn token and the ephemeral NodeID the issuer saw (never
// the fetcher's durable one). issuerAddr is the issuer's dialable address (resolved via
// the durable node's routing table); issuerPub is its token-issuer public key; credit is
// a blind credit acquired earlier under the durable identity (Node.AcquireCredits).
func WithdrawDemandTokenPrivately(rng io.Reader, issuerID ports.NodeID, issuerAddr string, issuerPub *rsa.PublicKey, credit ports.PublishCredit, timeout time.Duration) (demand.Token, ports.NodeID, error) {
	eph, err := identity.Generate(rng)
	if err != nil {
		return demand.Token{}, ports.NodeID{}, fmt.Errorf("ephemeral identity: %w", err)
	}
	ephID := eph.NodeID()

	loop := eventloop.New()
	go loop.Run()
	defer loop.Stop()

	tr, err := tcpnet.New(loop, eph, "127.0.0.1:0")
	if err != nil {
		return demand.Token{}, ephID, fmt.Errorf("ephemeral transport: %w", err)
	}
	defer tr.Close()

	nd := node.New(ephID, node.DefaultConfig(), walltime.New(loop), tr, memstore.New())
	nd.SetEphemeral(true) // a short-lived client: peers must not route to it (#43)
	tr.AddPeer(issuerID, issuerAddr)

	type result struct {
		tok demand.Token
		err error
	}
	ch := make(chan result, 1)
	// Node methods run on the single-threaded event loop; post the withdrawal there.
	loop.Post(func() {
		nd.AcquireDemandTokenWithCredit(rng, issuerID, issuerPub, credit, func(tok demand.Token, err error) {
			ch <- result{tok, err}
		})
	})

	select {
	case r := <-ch:
		return r.tok, ephID, r.err
	case <-time.After(timeout):
		return demand.Token{}, ephID, fmt.Errorf("private demand withdrawal timed out after %s", timeout)
	}
}
