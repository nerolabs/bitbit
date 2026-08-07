// D-DEMAND wiring (#181): the delivery-receipt path that turns the pure
// core/demand primitive into a live network capability. A fetcher that received an
// object's bytes spends a blind-withdrawn retrieval token by submitting a
// PoR-bound, signed receipt to the server; the server banks it into a NEUTRAL
// witnessed-demand observable. Token issuance itself reuses the existing publish
// token-issuer path (MsgTokenRequest / blind-sign) — the fetcher just blinds in the
// demand domain (blindtoken.BlindDemand) — so no new issuance wire is needed here.
//
// NEUTRAL by construction: witnessed demand is an observable, never read by
// consensus standing, so a forged or self-dealt receipt buys ZERO standing (the
// γ→1/N firewall). The cost-to-wash re-pricing (fee-burn + bonded-fetcher
// credential) and fetcher-unlinkability (D3) are later D-DEMAND phases.
package node

import (
	"crypto/rsa"
	"errors"
	"io"

	"github.com/nerolabs/silt/core/blindtoken"
	"github.com/nerolabs/silt/core/demand"
	"github.com/nerolabs/silt/ports"
)

// ErrNoSigner is returned when a fetcher without a signing identity tries to
// produce a delivery receipt (SetSigner was never called).
var ErrNoSigner = errors.New("node: no signing identity (SetSigner) — cannot sign a delivery receipt")

// AcquireDemandToken is the fetcher side of a blind retrieval-token withdrawal: it
// blinds a fresh serial in the demand domain, sends it to issuer over the existing
// token-request wire (the issuer blind-signs it, charging its usual fee and learning
// nothing about the serial), and unblinds the reply into a Token. The issuer's key
// must be known (FetchIssuerKey). done fires once with the token or an error.
func (n *Node) AcquireDemandToken(rng io.Reader, issuer ports.NodeID, done func(demand.Token, error)) {
	pub := n.IssuerKeyOf(issuer)
	if pub == nil {
		done(demand.Token{}, errNoIssuerKey)
		return
	}
	serial, err := blindtoken.NewSerial(rng)
	if err != nil {
		done(demand.Token{}, err)
		return
	}
	blinded, secret, err := demand.Withdraw(rng, pub, serial)
	if err != nil {
		done(demand.Token{}, err)
		return
	}
	n.request(issuer, ports.Message{Kind: ports.MsgTokenRequest, Data: blinded}, func(resp ports.Message, rerr error) {
		if rerr != nil {
			done(demand.Token{}, rerr)
			return
		}
		if !resp.OK || len(resp.Data) == 0 {
			done(demand.Token{}, ErrTokenAcquire)
			return
		}
		done(demand.Unblind(pub, serial, resp.Data, secret), nil)
	})
}

// EnableDemandBank makes this node BANK delivery receipts: on a MsgDeliveryReceipt
// it verifies the receipt against issuerPub (the retrieval-token issuer it trusts)
// and, if valid + first-seen + a correct delivery to THIS server, credits the
// object's witnessed-demand counter. Demand is a neutral observable — never standing.
func (n *Node) EnableDemandBank(issuerPub *rsa.PublicKey) {
	n.demandBank = demand.NewBank()
	n.demandIssuer = issuerPub
}

// WitnessedDemand is the neutral count of correctly-delivered, token-backed
// retrievals banked for object. 0 when demand banking is off. Observability only.
func (n *Node) WitnessedDemand(object ports.Hash) int64 {
	if n.demandBank == nil {
		return 0
	}
	return n.demandBank.Demand(object)
}

// SubmitDeliveryReceipt is the fetcher side: having received object's bytes (data)
// from server, it spends token by producing a PoR-bound, signed delivery receipt and
// submitting it. done reports whether the server banked it. The fetcher derives the
// object's PoR tags from the bytes it received (ObjectKey is public), so nothing but
// the token and the bytes is needed.
func (n *Node) SubmitDeliveryReceipt(server ports.NodeID, token demand.Token, object ports.Hash, data []byte, done func(credited bool, err error)) {
	if n.signer == nil {
		done(false, ErrNoSigner)
		return
	}
	tags := demand.ObjectKey(object).Tags(object[:], data)
	receipt, err := demand.Ack(n.signer, token, object, server, data, tags)
	if err != nil {
		done(false, err)
		return
	}
	blob, err := demand.SubmittedReceipt{Token: token, Receipt: receipt}.Marshal()
	if err != nil {
		done(false, err)
		return
	}
	n.request(server, ports.Message{Kind: ports.MsgDeliveryReceipt, Data: blob}, func(resp ports.Message, rerr error) {
		if rerr != nil {
			done(false, rerr)
			return
		}
		done(resp.OK, nil)
	})
}

// handleDeliveryReceipt is the server side: verify + bank a submitted receipt, then
// ack whether it credited witnessed demand. It only banks receipts naming THIS node
// as the delivering server, so a receipt for another server can't be redirected here.
func (n *Node) handleDeliveryReceipt(from ports.NodeID, msg ports.Message) {
	deny := func() { n.reply(from, msg, ports.Message{Kind: ports.MsgDeliveryReceiptAck, OK: false}) }
	if n.demandBank == nil || n.demandIssuer == nil {
		deny()
		return
	}
	sub, err := demand.UnmarshalSubmittedReceipt(msg.Data)
	if err != nil {
		deny()
		return
	}
	if sub.Receipt.Server != n.id {
		deny() // a receipt must name this server; don't bank one addressed elsewhere
		return
	}
	credited, reason := n.demandBank.Redeem(n.demandIssuer, sub.Token, sub.Receipt)
	if credited {
		n.logf(ports.LogInfo, "delivery receipt banked", "object", sub.Receipt.Object,
			"demand", n.demandBank.Demand(sub.Receipt.Object))
	} else {
		n.logf(ports.LogDebug, "delivery receipt rejected", "reason", reason)
	}
	n.reply(from, msg, ports.Message{Kind: ports.MsgDeliveryReceiptAck, OK: credited})
}
