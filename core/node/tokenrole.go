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
	charge := n.tokenChargeFor(from, msg.Credit)
	if charge == nil {
		return reply // a credit was presented but is invalid or already spent
	}
	blindSig, err := n.tokenIssuer.Issue(charge, msg.Data)
	if err != nil {
		return reply // e.g. insufficient credit → OK=false, no token
	}
	reply.Data = blindSig
	reply.OK = true
	return reply
}

// tokenChargeFor returns the settlement closure for a token request, or nil if
// the request must be refused. The fee-decoupling is purely ADDITIVE (M0 privacy
// D3 / F4): if the request carries a valid, unspent prepaid credit, that credit
// is SPENT and the requester's durable identity is NOT charged — severing the
// per-publish fee link. With no credit attached the legacy path charges the
// durable identity, so every existing flow is unchanged. A credit that is
// present but invalid or already spent is refused (nil) rather than silently
// charged — the requester meant to spend a credit. Domain separation
// (VerifyCredit) means a credit can never double as a publish token.
func (n *Node) tokenChargeFor(from ports.NodeID, credit *ports.PublishCredit) func() error {
	if credit == nil {
		return func() error {
			if n.ledger != nil {
				return n.ledger.ChargePublish(from) // legacy: charges the durable identity
			}
			return nil
		}
	}
	if len(credit.Serial) == 0 || !blindtoken.VerifyCredit(n.tokenIssuer.Public(), credit.Serial, credit.Sig) {
		return nil // a credit was presented but does not verify
	}
	key := string(credit.Serial)
	if n.creditSpent[key] {
		return nil // double-spend
	}
	return func() error {
		// Spend on settlement so a signing failure does not burn the credit.
		n.creditSpent[key] = true
		return nil
	}
}

// AcquireCredits mints `count` prepaid publish credits from issuer `v` (a normal,
// fee-charged token request, blinded in the CREDIT domain), so the fee is paid
// now, in bulk, decoupled from any later publish. The returned credits are spent
// one-per-token at publish time (AcquireToken with a credit attached) with no
// further debit. rng is injected; issuerPub gives v's issuer public key.
func (n *Node) AcquireCredits(rng io.Reader, v ports.NodeID, count int,
	issuerPub func(ports.NodeID) *rsa.PublicKey, done func([]ports.PublishCredit, error)) {

	pub := issuerPub(v)
	if pub == nil || v == n.id || count <= 0 {
		done(nil, ErrTokenAcquire)
		return
	}
	var credits []ports.PublishCredit
	var next func(i int)
	next = func(i int) {
		if i >= count {
			done(credits, nil)
			return
		}
		serial, err := blindtoken.NewSerial(rng)
		if err != nil {
			done(nil, err)
			return
		}
		blinded, secret, err := blindtoken.BlindCredit(rng, pub, serial)
		if err != nil {
			done(nil, err)
			return
		}
		n.request(v, ports.Message{Kind: ports.MsgTokenRequest, Data: blinded},
			func(resp ports.Message, err error) {
				if err == nil && resp.OK && len(resp.Data) > 0 {
					if sig := blindtoken.Unblind(pub, resp.Data, secret); sig != nil {
						credits = append(credits, ports.PublishCredit{Serial: serial, Sig: sig})
					}
				}
				next(i + 1)
			})
	}
	next(0)
}

// AcquireToken collects blind signatures from up to len(validators) distinct
// issuers (never this node itself) until it has k, assembling an unlinkable
// publish token for serial. The requester's durable identity is charged the fee
// at each issuer. rng is injected (core touches no ambient randomness);
// issuerPub gives each validator's issuer public key.
func (n *Node) AcquireToken(rng io.Reader, serial []byte, validators []ports.NodeID,
	issuerPub func(ports.NodeID) *rsa.PublicKey, k int, done func(*ports.PublishToken, error)) {
	n.acquireToken(rng, serial, validators, issuerPub, nil, k, done)
}

// AcquireTokenWithCredits is the fee-decoupled acquisition (M0 privacy D3 / F4):
// each issuer's signature is paid for by SPENDING a prepaid credit from credits[v]
// (minted earlier via AcquireCredits) instead of charging the durable identity,
// so no per-publish fee links the standing key to this publish. An issuer for
// which no credit is held is skipped. Combine with an ephemeral/relayed transport
// to also sever the network-layer link (the remaining D3 residual).
func (n *Node) AcquireTokenWithCredits(rng io.Reader, serial []byte, validators []ports.NodeID,
	issuerPub func(ports.NodeID) *rsa.PublicKey, credits map[ports.NodeID]ports.PublishCredit, k int,
	done func(*ports.PublishToken, error)) {
	n.acquireToken(rng, serial, validators, issuerPub, func(v ports.NodeID) *ports.PublishCredit {
		if c, ok := credits[v]; ok {
			return &c
		}
		return nil
	}, k, done)
}

// acquireToken is the shared collector. credit, if non-nil, supplies the prepaid
// credit to attach for each issuer; a nil credit lookup (or a nil result) means
// the legacy fee-at-request path. When a credit lookup is given, an issuer with
// no credit is skipped rather than charged.
func (n *Node) acquireToken(rng io.Reader, serial []byte, validators []ports.NodeID,
	issuerPub func(ports.NodeID) *rsa.PublicKey, credit func(ports.NodeID) *ports.PublishCredit, k int,
	done func(*ports.PublishToken, error)) {

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
		var attached *ports.PublishCredit
		if credit != nil {
			if attached = credit(v); attached == nil {
				next(i + 1) // credit-backed acquisition, but none held for v
				return
			}
		}
		blinded, secret, err := blindtoken.Blind(rng, pub, serial)
		if err != nil {
			next(i + 1)
			return
		}
		n.request(v, ports.Message{Kind: ports.MsgTokenRequest, Data: blinded, Credit: attached},
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
