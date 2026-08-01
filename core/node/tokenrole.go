// Publish-token issuance and acquisition (T3, #14 / F1). A validator can be a
// token ISSUER: it blind-signs a requester's blinded serial, charging the fee
// to the requester's durable identity but learning nothing about the serial.
// A publisher ACQUIRES a token by collecting k such blind signatures from
// DISTINCT validators, so its publish carries a quorum credential and no
// durable identity. Verification and double-spend live in the chain
// (chain.RequireTokens); the fee links to identity, the token does not.
package node

import (
	"crypto/rsa"
	"errors"
	"io"

	"github.com/nerolabs/silt/core/blindtoken"
	"github.com/nerolabs/silt/ports"
)

var (
	// ErrTokenAcquire means fewer than k issuers granted a signature (e.g.
	// offline or out of the requester's credit).
	ErrTokenAcquire = errors.New("node: could not gather enough publish-token signatures")
	errNoIssuerKey  = errors.New("node: peer has no issuer key")
)

// EnableTokenIssuer makes this validator blind-sign publish-token requests
// (charging the fee to each requester) and serve its issuer public key to
// peers who ask (MsgGetIssuerKey).
func (n *Node) EnableTokenIssuer(key *rsa.PrivateKey) {
	n.tokenIssuer = blindtoken.NewIssuer(key)
	n.issuerKeyDER = blindtoken.MarshalPub(&key.PublicKey)
}

func (n *Node) answerIssuerKey() ports.Message {
	return ports.Message{Kind: ports.MsgIssuerKeyReply, Data: n.issuerKeyDER, OK: len(n.issuerKeyDER) > 0}
}

// FetchIssuerKey asks v for its token-issuer public key and caches it, so this
// node can blind against it (as a publisher) or verify its token signatures
// (as a validator).
func (n *Node) FetchIssuerKey(v ports.NodeID, done func(error)) {
	n.request(v, ports.Message{Kind: ports.MsgGetIssuerKey}, func(resp ports.Message, err error) {
		switch {
		case err != nil:
			done(err)
		case !resp.OK || len(resp.Data) == 0:
			done(errNoIssuerKey)
		default:
			pub, perr := blindtoken.ParsePub(resp.Data)
			if perr == nil {
				n.peerIssuerKeys[v] = pub
			}
			done(perr)
		}
	})
}

// IssuerKeyOf returns v's token-issuer public key — this node's own, or one
// cached from a FetchIssuerKey. Used as the chain's issuerKey lookup and as the
// publisher's blind-against key.
func (n *Node) IssuerKeyOf(v ports.NodeID) *rsa.PublicKey {
	if v == n.id && n.tokenIssuer != nil {
		return n.tokenIssuer.Public()
	}
	return n.peerIssuerKeys[v]
}

func (n *Node) answerTokenRequest(from ports.NodeID, msg ports.Message) ports.Message {
	reply := ports.Message{Kind: ports.MsgTokenReply}
	if n.tokenIssuer == nil || len(msg.Data) == 0 {
		return reply // not an issuer / nothing to sign → OK=false
	}
	charge := func() error {
		if n.ledger != nil {
			return n.ledger.ChargePublish(from) // charges the durable identity, not the blinded serial
		}
		return nil
	}
	blindSig, err := n.tokenIssuer.Issue(charge, msg.Data)
	if err != nil {
		return reply // e.g. insufficient credit → OK=false, no token
	}
	reply.Data = blindSig
	reply.OK = true
	return reply
}

// AcquireToken collects blind signatures from up to len(validators) distinct
// issuers (never this node itself) until it has k, assembling an unlinkable
// publish token for serial. rng is injected (core touches no ambient
// randomness); issuerPub gives each validator's issuer public key.
func (n *Node) AcquireToken(rng io.Reader, serial []byte, validators []ports.NodeID,
	issuerPub func(ports.NodeID) *rsa.PublicKey, k int, done func(*ports.PublishToken, error)) {

	tok := &ports.PublishToken{Serial: serial}
	var next func(i int)
	next = func(i int) {
		if len(tok.Sigs) >= k {
			done(tok, nil)
			return
		}
		if i >= len(validators) {
			done(nil, ErrTokenAcquire)
			return
		}
		v := validators[i]
		pub := issuerPub(v)
		if pub == nil || v == n.id {
			next(i + 1)
			return
		}
		blinded, secret, err := blindtoken.Blind(rng, pub, serial)
		if err != nil {
			next(i + 1)
			return
		}
		n.request(v, ports.Message{Kind: ports.MsgTokenRequest, Data: blinded},
			func(resp ports.Message, err error) {
				if err == nil && resp.OK && len(resp.Data) > 0 {
					if sig := blindtoken.Unblind(pub, resp.Data, secret); sig != nil {
						tok.Sigs = append(tok.Sigs, ports.TokenSig{Validator: v, Sig: sig})
					}
				}
				next(i + 1)
			})
	}
	next(0)
}
