// Package demand is P0 of the blind demand receipt (D-DEMAND, issue #181): the
// interlock between the Sybil corner (standing should track WITNESSED demand, not
// self-declared popularity) and privacy (who-fetches-what stays unlinkable). This
// covers phases P0–P1 — blind-withdraw → PoR-bound delivery-ack → bank → redeem,
// for a SINGLE object — and it delivers exactly one provable property:
//
//	UNFORGEABILITY AT THE TOKEN LEVEL: a server cannot bank more receipts for an
//	object C than there were issued tokens spent on a fetcher-signed proof of having
//	held C's bytes. #receipts(C) ≤ #issued-tokens-spent-on-a-signed-C-delivery.
//
// What it deliberately does NOT prove (a Douceur limit, not an engineering gap, per
// the decision's doc-truth rule): **demand AUTHENTICITY.** A server can run its own
// fetcher, pay itself, fetch its own content, and mint perfectly valid receipts — a
// self-fetch IS a real paid correct delivery, and no cryptographic receipt certifies
// the counterparty was economically independent. Authenticity is re-priced, never
// proven, by cost-to-wash (fee-burn + bonded-fetcher credential — P3). Any claim
// that a receipt proves organic third-party demand is false.
//
// NEUTRAL BY CONSTRUCTION. A redeemed receipt records witnessed demand as an
// OBSERVABLE (Bank.Demand) that is NOT wired to consensus standing — so even a
// forged or self-dealt receipt buys ZERO standing. That is what keeps the γ→1/N
// shared-content sealing firewall intact (fusing demand into standing is gated on
// the open sealing problem, m0.md §10 / #182). Whether/how witnessed demand ever
// feeds standing is a separate, gated decision this package does not make.
//
// BLIND WITHDRAWAL IS BUILT (P1): the retrieval token is withdrawn under an issuer
// blind signature (Withdraw → SignWithdrawal → Unblind, core/blindtoken demand
// domain), so the issuer signs it without learning the serial — the redeemed token
// is cryptographically unlinkable to its withdrawal. But FETCHER-UNLINKABILITY is
// only NOMINAL until D3 issuance-mixing closes the IP/timing channel (shared with
// H8/#179): the blind signature hides the serial, not the network identity of the
// withdrawer. LATER PHASES: optimistic fair-exchange dispute (P2), and the
// cost-to-wash economics + self-dealing red-team (P3).
package demand

import (
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"io"

	"github.com/fxamacker/cbor/v2"
	"github.com/nerolabs/silt/core/blindtoken"
	"github.com/nerolabs/silt/core/por"
	"github.com/nerolabs/silt/ports"
)

// SampleCount is how many PoR blocks a delivery proof samples (Shacham–Waters
// §4.1): more samples raise the odds of catching a prover missing any fixed
// fraction of the object. Clamped to the object's block count. A tuning knob.
const SampleCount = 128

const (
	ackDomain = "silt/demand/ack/v1" // the PoR challenge seed binds serial‖object‖server
	porDomain = "silt/demand/por/v1" // per-object PoR key seed
)

// Token is an issuer-authorized retrieval credit, BLIND-WITHDRAWN (P1): a serial
// carrying an issuer blind signature in the demand domain. Because it was withdrawn
// blindly (Withdraw → SignWithdrawal → Unblind), the issuer signed the token
// WITHOUT learning its serial, so the redeemed token is cryptographically unlinkable
// to the withdrawal at issuance time. (Residual: the IP/timing channel is closed
// only by D3 issuance-mixing, shared with H8 — property (b) unlinkability is nominal
// until then.) A fetcher spends the token by producing a DeliveryReceipt that
// references its serial.
type Token struct {
	Serial []byte // unique; the double-spend key
	Sig    []byte // issuer blind signature over the serial (blindtoken demand domain)
}

// Withdraw is the fetcher side of a blind retrieval-token withdrawal: it blinds a
// fresh serial (see blindtoken.NewSerial) so the issuer signs it without learning
// the serial. It returns the blinded value to send the issuer and the secret to
// unblind the reply.
func Withdraw(rng io.Reader, issuerPub *rsa.PublicKey, serial []byte) (blinded, secret []byte, err error) {
	return blindtoken.BlindDemand(rng, issuerPub, serial)
}

// SignWithdrawal is the issuer side: it blind-signs the withdrawal, learning nothing
// about the serial. Charging or burning the fetch fee against the withdrawal is the
// caller's job (the cost-to-wash knob is P3).
func SignWithdrawal(issuerPriv *rsa.PrivateKey, blinded []byte) []byte {
	return blindtoken.SignBlinded(issuerPriv, blinded)
}

// Unblind turns the issuer's blind signature into the unlinkable Token on the plain
// serial, using the secret from Withdraw.
func Unblind(issuerPub *rsa.PublicKey, serial, blindSig, secret []byte) Token {
	return Token{
		Serial: append([]byte(nil), serial...),
		Sig:    blindtoken.Unblind(issuerPub, blindSig, secret),
	}
}

// VerifyToken reports whether t carries a valid issuer signature in the demand
// domain (so a publish token or credit under the same key does not pass).
func VerifyToken(issuerPub *rsa.PublicKey, t Token) bool {
	return len(t.Serial) > 0 && blindtoken.VerifyDemand(issuerPub, t.Serial, t.Sig)
}

// ObjectKey derives object C's PoR verification key. In P0 the seed is PUBLIC
// (bound to C's content address), so the publisher can tag at serve time and any
// redeemer can verify — with the documented residual that a public key means tags
// are forgeable, so the proof certifies "held bytes consistent with C's demand key,"
// not "held the unique correct bytes of C" (the same tag-forgery gap H7 documents;
// the secret-keyed / content-committed binding is a fast-follow). Sound enough for
// P0 precisely because demand is NEUTRAL — a forged binding buys no standing.
func ObjectKey(object ports.Hash) *por.Key {
	seed := sha256.Sum256(append([]byte(porDomain), object[:]...))
	k, err := por.DeriveKey(seed[:], por.DefaultParams)
	if err != nil {
		panic(err) // DefaultParams is valid; cannot fail
	}
	return k
}

// ackSeed binds a delivery proof to THIS exact (serial, object, server): a receipt
// built for one cannot be replayed for another object or redeemed by another server,
// and the challenge cannot be precomputed before the token is known.
func ackSeed(serial []byte, object ports.Hash, server ports.NodeID) [32]byte {
	h := sha256.New()
	h.Write([]byte(ackDomain))
	h.Write(serial)
	h.Write(object[:])
	h.Write(server[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// challengeFor rebuilds the PoR challenge both the fetcher (proving) and the
// redeemer (verifying) derive from the receipt — nothing challenge-specific rides
// on the wire beyond the block count.
func challengeFor(serial []byte, object ports.Hash, server ports.NodeID, blocks int) por.Challenge {
	count := SampleCount
	if count > blocks {
		count = blocks
	}
	return por.Challenge{Seed: ackSeed(serial, object, server), Blocks: blocks, Count: count}
}

// DeliveryReceipt is a fetcher's PoR-bound, signed acknowledgement that it received
// the correct bytes of Object from Server, spending the token named by Serial. Its
// binding is threefold: the PoR Proof (held the bytes), the fetcher signature
// (only the token's spender can mint it), and the (serial‖object‖server)-bound
// challenge (can't be replayed). The server banks it and redeems it later.
type DeliveryReceipt struct {
	Serial  []byte       // the spent token's serial
	Object  ports.Hash   // C — the content-addressed object delivered
	Server  ports.NodeID // the delivering server (the redeemer); binds the ack
	Fetcher []byte       // the fetcher's ed25519 public key
	Blocks  int          // C's PoR block count (bounds the sample space)
	Proof   por.Proof    // PoR proof over the delivered bytes of C
	Sig     []byte       // fetcher's signature over receiptMsg
}

// receiptMsg is the bytes the fetcher signs: every field that fixes what was
// delivered, to whom-for, and the possession proof, so tampering any of them (or
// lifting the proof onto another receipt) breaks the signature.
func (r DeliveryReceipt) receiptMsg() []byte {
	h := sha256.New()
	h.Write([]byte("silt/demand/receipt/v1"))
	h.Write(r.Serial)
	h.Write(r.Object[:])
	h.Write(r.Server[:])
	h.Write(r.Fetcher)
	var nb [8]byte
	for i := 0; i < 8; i++ {
		nb[i] = byte(r.Blocks >> (8 * i))
	}
	h.Write(nb[:])
	for _, m := range r.Proof.Mu {
		h.Write(m)
	}
	h.Write(r.Proof.Sigma)
	return h.Sum(nil)
}

// Ack is the fetcher side: having received Object's bytes (data) and its PoR tags
// from Server (tags travel with served content), the fetcher spends token by
// producing a PoR proof over the bytes under the (serial‖object‖server)-bound
// challenge and signing the whole receipt. It returns an error only on malformed
// PoR input; a well-formed-but-wrong delivery simply fails to verify at redeem.
func Ack(fetcher ed25519.PrivateKey, token Token, object ports.Hash, server ports.NodeID, data []byte, tags [][]byte) (DeliveryReceipt, error) {
	blocks := por.DefaultParams.Blocks(len(data))
	c := challengeFor(token.Serial, object, server, blocks)
	proof, err := por.Prove(por.DefaultParams, data, tags, c)
	if err != nil {
		return DeliveryReceipt{}, err
	}
	r := DeliveryReceipt{
		Serial:  append([]byte(nil), token.Serial...),
		Object:  object,
		Server:  server,
		Fetcher: append([]byte(nil), fetcher.Public().(ed25519.PublicKey)...),
		Blocks:  blocks,
		Proof:   proof,
	}
	r.Sig = ed25519.Sign(fetcher, r.receiptMsg())
	return r, nil
}

// Bank is a server's neutral demand ledger: the set of already-redeemed token
// serials (double-spend guard) and the per-object count of witnessed deliveries.
// Demand is an OBSERVABLE — it is never read by consensus standing.
type Bank struct {
	spent  map[string]bool
	demand map[ports.Hash]int64
}

// NewBank returns an empty demand ledger.
func NewBank() *Bank {
	return &Bank{spent: map[string]bool{}, demand: map[ports.Hash]int64{}}
}

// Redeem verifies a banked receipt against the token that authorized it and, if it
// is a first-seen, validly-issued, correctly-delivered receipt for the named
// server, credits the object's witnessed-demand counter once. It reports whether it
// credited, plus the reason it didn't (for observability/tests). It NEVER touches
// standing.
//
// Rejections (each an unforgeability property): a token not signed by the issuer;
// a serial already redeemed (double-spend); a receipt whose serial ≠ the token's;
// a fetcher signature that doesn't verify; a PoR proof that doesn't verify against
// the object's key under the identity-bound challenge (no delivery, wrong object,
// wrong server, or data-less redeemer).
func (b *Bank) Redeem(issuerPub *rsa.PublicKey, token Token, r DeliveryReceipt) (credited bool, reason string) {
	if !VerifyToken(issuerPub, token) {
		return false, "token not issued"
	}
	if string(token.Serial) != string(r.Serial) {
		return false, "receipt serial does not match token"
	}
	if b.spent[string(r.Serial)] {
		return false, "double-spend: serial already redeemed"
	}
	if len(r.Fetcher) != ed25519.PublicKeySize || !ed25519.Verify(r.Fetcher, r.receiptMsg(), r.Sig) {
		return false, "fetcher signature invalid"
	}
	c := challengeFor(r.Serial, r.Object, r.Server, r.Blocks)
	if !ObjectKey(r.Object).Verify(r.Object[:], c, r.Proof) {
		return false, "delivery proof invalid (no correct delivery of this object to this server)"
	}
	b.spent[string(r.Serial)] = true
	b.demand[r.Object]++
	return true, ""
}

// Demand is the witnessed-delivery count banked for object — an observable, never
// standing. Reading it moves nothing.
func (b *Bank) Demand(object ports.Hash) int64 { return b.demand[object] }

// SubmittedReceipt bundles a delivery receipt with the token that authorized it —
// the pair a fetcher hands the server so the server can bank and redeem in one
// message. Marshal to CBOR for the wire.
type SubmittedReceipt struct {
	Token   Token
	Receipt DeliveryReceipt
}

// Marshal serializes the bundle to CBOR.
func (s SubmittedReceipt) Marshal() ([]byte, error) {
	return cbor.Marshal(s)
}

// UnmarshalSubmittedReceipt parses a wire bundle. Every field is untrusted input to
// be re-verified at Redeem, never established fact.
func UnmarshalSubmittedReceipt(b []byte) (SubmittedReceipt, error) {
	var s SubmittedReceipt
	if err := cbor.Unmarshal(b, &s); err != nil {
		return SubmittedReceipt{}, err
	}
	return s, nil
}
