package demand

import (
	"crypto/ed25519"
	"crypto/sha256"

	"github.com/nerolabs/silt/ports"
)

// Optimistic fair exchange (D-DEMAND P2) — the ASW-shaped floor silt can build in
// pure Go. The exchange is content C (server → fetcher) ⟷ a delivery receipt
// (fetcher → server, the server's demand credit). Fair exchange PROVABLY needs a TTP
// (Pagnia–Gärtner); silt's is the VALIDATOR QUORUM as a threshold-distributed TTP
// (Asokan–Shoup–Waidner), meant to be invoked ONLY on dispute.
//
// WHAT M0 BUILDS (this file + the abort-safety regressions): the optimistic path and
// its two abort-SAFETY properties, which hold structurally today.
//
//  1. FETCHER-SIDE abort safety: an aborted exchange never CONSUMES the fetcher's
//     token. A serial is spent only by a completed Bank.Redeem (a valid PoR-bound
//     receipt), so a server that takes the commitment and then vanishes — or delivers
//     nothing — leaves the paid token UNSPENT and reusable at another server. The
//     fetcher cannot be robbed of its token by a non-delivering server.
//
//  2. SERVER-SIDE abort safety: a fetcher's pre-release ExchangeCommitment — a signed
//     promise made BEFORE content release, carrying NO proof of possession — is NOT a
//     redeemable receipt. It is domain-separated from the DeliveryReceipt signature
//     and carries no PoR proof, so a server cannot convert "the fetcher engaged" into
//     a fake completed delivery. The unforgeability bound (#receipts(C) ≤ #completed
//     paid correct deliveries) survives the abort path: only Ack's PoR-bound receipt
//     redeems.
//
// WHAT IS GATED (the dispute-RESOLUTION half, deliberately not built): converting a
// server-held commitment into a TTP-affidavit receipt on a fetcher default requires
// the quorum-TTP to VERIFY that delivery genuinely completed WITHOUT the fetcher's
// cooperation — i.e. verifiable encryption of C with the decryption key threshold-
// escrowed to the validator quorum. silt ships no threshold / verifiable-encryption
// crypto (no pure-Go library in 2026) — the same class of gap H7's blind proof-of-
// repair documents (a floor is shipped; the heavy crypto is a fast-follow). Demand-
// NEUTRALITY keeps the residual low-stakes for M0: an unresolved server-side abort
// only UNDERCOUNTS a neutral observable (never standing), so the missing affidavit
// path costs no security today. Held in tension; ExchangeCommitment is the exact seam
// the future threshold-crypto resolver consumes.

// commitDomain separates a pre-release exchange promise from a delivery receipt: a
// commitment signature can never be lifted onto a DeliveryReceipt (different domain,
// different message), so it cannot masquerade as proof of a completed delivery.
const commitDomain = "silt/demand/exchange-commit/v1"

// ExchangeCommitment is a fetcher's pre-release, non-repudiable promise to complete an
// exchange for (serial, object, server): the ASW artifact a server would present to
// the quorum-TTP on a fetcher default. It deliberately carries NO possession proof, so
// it is evidence of ENGAGEMENT, never of DELIVERY — the bank never credits demand from
// it; only a PoR-bound DeliveryReceipt (Ack → Redeem) does.
type ExchangeCommitment struct {
	Serial  []byte       // the token about to be spent
	Object  ports.Hash   // C — the object to be delivered
	Server  ports.NodeID // the server that will deliver (and would dispute)
	Fetcher []byte       // the committing fetcher's ed25519 public key
	Sig     []byte       // fetcher signature over commitmentMsg
}

// commitmentMsg is the bytes a fetcher signs to commit to an exchange — the same
// (serial‖object‖server‖fetcher) binding a receipt uses, under a DISTINCT domain so
// the two signatures are not interchangeable.
func commitmentMsg(serial []byte, object ports.Hash, server ports.NodeID, fetcher []byte) []byte {
	h := sha256.New()
	h.Write([]byte(commitDomain))
	h.Write(serial)
	h.Write(object[:])
	h.Write(server[:])
	h.Write(fetcher)
	return h.Sum(nil)
}

// Commit is the fetcher side of the ASW optimistic phase: sign a pre-release promise
// for the exchange that spends token to fetch object from server. It proves nothing
// about possession (the fetcher does not hold C yet) — it only binds the fetcher to
// this exchange so a defaulting fetcher leaves the server non-repudiable evidence.
func Commit(fetcher ed25519.PrivateKey, token Token, object ports.Hash, server ports.NodeID) ExchangeCommitment {
	pub := fetcher.Public().(ed25519.PublicKey)
	c := ExchangeCommitment{
		Serial:  append([]byte(nil), token.Serial...),
		Object:  object,
		Server:  server,
		Fetcher: append([]byte(nil), pub...),
	}
	c.Sig = ed25519.Sign(fetcher, commitmentMsg(c.Serial, c.Object, c.Server, c.Fetcher))
	return c
}

// VerifyCommitment reports whether c is a well-formed, correctly-signed pre-release
// commitment. A true result is evidence the fetcher ENGAGED this exchange — NOT that a
// delivery completed. No bank path credits demand from a commitment; redeeming demand
// still requires the PoR-bound DeliveryReceipt.
func VerifyCommitment(c ExchangeCommitment) bool {
	if len(c.Fetcher) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(c.Fetcher, commitmentMsg(c.Serial, c.Object, c.Server, c.Fetcher), c.Sig)
}
