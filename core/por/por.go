// Package por is a real proof-of-retrievability: a verifier holding a small
// secret key can check that a prover still holds a chunk's bytes WITHOUT
// fetching them — the property the toy scheme in core/node/por.go lacks (it
// grades against ground truth it fetches itself).
//
// The construction is the private-verification Compact Proof of
// Retrievability of Shacham & Waters, "Compact Proofs of Retrievability"
// (ASIACRYPT 2008), §3 — adopted, not invented (tenet B8). It is a
// homomorphic linear authenticator over a prime field:
//
//	setup:      secret key = (PRF key k, sector secrets α₁..α_s ∈ Z_p)
//	tag:        for block i with sectors mᵢ₁..mᵢ_s,  σᵢ = f_k(i) + Σⱼ αⱼ·mᵢⱼ (mod p)
//	challenge:  a seed expands to l sampled blocks i with coefficients νᵢ ∈ Z_p
//	prove:      μⱼ = Σᵢ νᵢ·mᵢⱼ  and  σ = Σᵢ νᵢ·σᵢ   (aggregate — O(s), size-independent)
//	verify:     σ ?= Σᵢ νᵢ·f_k(i) + Σⱼ αⱼ·μⱼ        (no data touched)
//
// A prover that has deleted or altered any sampled block cannot produce a
// (μ, σ) that satisfies the verification equation except with negligible
// probability: σ is pinned by the stored tags, μⱼ is pinned by the data it
// no longer has, and it cannot forge a compensating μ without the secret αⱼ
// (which the tags do not reveal — that is the scheme's soundness proof). The
// verify key rides the silt care-link, so caretakers audit over ciphertext
// while storage-node provers, holding only chunk bytes + tags, cannot forge.
//
// This package is pure: it speaks in bytes and keys, touches no store, no
// network, no ports. Wiring it into the manifest, the node audit loop, and
// the credit ledger is a separate change (Gate 4a, #90).
package por

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
)

// prime is the field modulus p = 2²⁵⁵ − 19 (the well-studied Curve25519
// field prime). Every field element is reduced mod p and encoded as a fixed
// 32-byte big-endian value on the wire.
var prime = func() *big.Int {
	p := new(big.Int).Lsh(big.NewInt(1), 255)
	return p.Sub(p, big.NewInt(19))
}()

// SectorBytes is the width of one sector. 31 bytes = 248 bits < 255, so a
// sector parsed big-endian is always a canonical field element (< p) — no
// modular reduction of message data, which would make two distinct byte
// strings share a residue and open a forgery.
const SectorBytes = 31

// ElemBytes is the fixed encoding width of a field element (μⱼ, σ) on the
// wire: 32 bytes big-endian, enough to hold any residue mod p.
const ElemBytes = 32

// Params fixes the scheme layout. SectorsPerBlock (s) trades storage overhead
// — one tag per block, i.e. 32 bytes per s·31 data bytes ≈ 1/s — against
// proof size and key size, both O(s) field elements. It must match between
// Keygen, Tag, and Verify.
type Params struct {
	SectorsPerBlock int
}

// DefaultParams: s = 128 sectors/block ⇒ ~0.8% tag-storage overhead, a
// ~4 KiB block, and a proof/key of 128 field elements (~4 KiB) independent
// of file size.
var DefaultParams = Params{SectorsPerBlock: 128}

func (p Params) blockBytes() int { return p.SectorsPerBlock * SectorBytes }

// Blocks reports how many blocks a unit of n bytes splits into under p. The
// final block is zero-padded; padding is deterministic, so prover and
// verifier agree on it.
func (p Params) Blocks(n int) int {
	bb := p.blockBytes()
	if n <= 0 {
		return 0
	}
	return (n + bb - 1) / bb
}

// Key is the secret verification key: a PRF key and the s sector secrets αⱼ.
// It is held by the auditor (via the care-link) and never by a prover.
type Key struct {
	params Params
	prf    [32]byte
	alpha  []*big.Int // len == params.SectorsPerBlock
}

// DeriveKey derives the secret verification key DETERMINISTICALLY from a
// seed — so the publisher (which tags at Stage/Distribute time) and the
// auditor (which verifies later) independently arrive at the SAME key
// without transmitting it. In silt the seed is bound to a file's layout
// key (see core/node), so a care-link holder can derive the key and audit,
// while a storage node — holding only chunk bytes and tags, never the
// layout key — cannot, which is what keeps a prover from forging.
//
// The derivation is a fixed protocol constant: it expands the seed into a
// deterministic byte stream (HMAC-SHA256 in counter mode) and feeds that to
// the same key-construction Keygen uses, so a derived key is drawn from the
// identical distribution as a random one. Changing this function or Keygen's
// read pattern would change every derived key, so both are frozen.
func DeriveKey(seed []byte, params Params) (*Key, error) {
	if params.SectorsPerBlock <= 0 {
		return nil, errors.New("por: SectorsPerBlock must be positive")
	}
	return Keygen(&detReader{seed: seed}, params)
}

// detReader is an infinite deterministic byte stream keyed by a seed:
// HMAC-SHA256(seed, "silt/por/v1/derive" ‖ counter) blocks concatenated.
// Keygen reads exactly what it needs from it, so the same seed always
// yields the same key.
type detReader struct {
	seed []byte
	ctr  uint32
	buf  []byte
}

func (r *detReader) Read(p []byte) (int, error) {
	for len(r.buf) < len(p) {
		mac := hmac.New(sha256.New, r.seed)
		mac.Write([]byte("silt/por/v1/derive"))
		var cb [4]byte
		binary.BigEndian.PutUint32(cb[:], r.ctr)
		mac.Write(cb[:])
		r.ctr++
		r.buf = mac.Sum(r.buf)
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

// Keygen draws a fresh secret key for the given params.
func Keygen(rng io.Reader, params Params) (*Key, error) {
	if params.SectorsPerBlock <= 0 {
		return nil, errors.New("por: SectorsPerBlock must be positive")
	}
	k := &Key{params: params, alpha: make([]*big.Int, params.SectorsPerBlock)}
	if _, err := io.ReadFull(rng, k.prf[:]); err != nil {
		return nil, err
	}
	for j := range k.alpha {
		a, err := randElem(rng)
		if err != nil {
			return nil, err
		}
		k.alpha[j] = a
	}
	return k, nil
}

// Params returns the layout this key was generated for.
func (k *Key) Params() Params { return k.params }

// Tags computes the authenticator σᵢ for every block of unit `data`. unitID
// domain-separates the PRF so tags from different chunks never collide (a
// prover can't answer a challenge on chunk A with chunk B's stored tags).
// The returned slice has one 32-byte tag per block; store it with the chunk.
func (k *Key) Tags(unitID []byte, data []byte) [][]byte {
	nb := k.params.Blocks(len(data))
	tags := make([][]byte, nb)
	acc := new(big.Int)
	term := new(big.Int)
	for i := 0; i < nb; i++ {
		// σᵢ = f_k(i) + Σⱼ αⱼ·mᵢⱼ
		sigma := k.prf_i(unitID, i)
		for j := 0; j < k.params.SectorsPerBlock; j++ {
			mij := k.sector(data, i, j)
			if mij.Sign() == 0 {
				continue
			}
			term.Mul(k.alpha[j], mij)
			acc.Add(sigma, term)
			sigma.Mod(acc, prime)
		}
		tags[i] = encodeElem(sigma)
	}
	return tags
}

// Challenge names the blocks a prover must aggregate and the coefficients to
// weight them by. It is compact — a seed and a count — because both sides
// expand it identically; nothing block-specific travels on the wire.
type Challenge struct {
	Seed   [32]byte
	Blocks int // total blocks in the unit (bounds the sample space)
	Count  int // number of distinct blocks sampled
}

// NewChallenge draws a fresh challenge sampling min(count, blocks) distinct
// blocks. A larger count raises the probability of catching a prover missing
// any fixed fraction of the data (Shacham–Waters §4.1).
func NewChallenge(rng io.Reader, blocks, count int) (Challenge, error) {
	var c Challenge
	if blocks <= 0 {
		return c, errors.New("por: unit has no blocks to challenge")
	}
	if _, err := io.ReadFull(rng, c.Seed[:]); err != nil {
		return c, err
	}
	if count > blocks {
		count = blocks
	}
	c.Blocks = blocks
	c.Count = count
	return c, nil
}

// query is one (index, coefficient) pair the challenge expands to.
type query struct {
	i  int
	nu *big.Int
}

// expand deterministically derives the sampled (index, coefficient) pairs
// from the seed — computed identically by prover and verifier, so only the
// seed and counts cross the wire. Indices are sampled without replacement.
func (c Challenge) expand() []query {
	qs := make([]query, 0, c.Count)
	used := make(map[int]bool, c.Count)
	var ctr uint32
	for len(qs) < c.Count {
		i := int(prfUint(c.Seed, "index", ctr) % uint64(c.Blocks))
		ctr++
		if used[i] {
			continue // distinct blocks; re-draw on collision
		}
		used[i] = true
		nu := new(big.Int).Mod(prfElem(c.Seed, "coeff", uint32(i)), prime)
		if nu.Sign() == 0 {
			nu.SetInt64(1) // coefficients must be non-zero
		}
		qs = append(qs, query{i: i, nu: nu})
	}
	return qs
}

// Proof is a prover's compact response: one aggregated sector value μⱼ per
// sector position, plus the aggregated tag σ. Its size is O(s), independent
// of the chunk's size.
type Proof struct {
	Mu    [][]byte // len == SectorsPerBlock, each ElemBytes
	Sigma []byte   // ElemBytes
}

// Prove is the prover side: aggregate the challenged blocks of `data` and
// their stored `tags` into a Proof. It needs no key — an honest holder of the
// bytes and tags can always answer; a holder that lost bytes cannot make the
// answer verify.
func Prove(params Params, data []byte, tags [][]byte, c Challenge) (Proof, error) {
	if params.SectorsPerBlock <= 0 {
		return Proof{}, errors.New("por: bad params")
	}
	if c.Blocks != len(tags) {
		return Proof{}, errors.New("por: challenge/tags block-count mismatch")
	}
	mu := make([]*big.Int, params.SectorsPerBlock)
	for j := range mu {
		mu[j] = new(big.Int)
	}
	sigma := new(big.Int)
	tmp := new(big.Int)
	for _, q := range c.expand() {
		// σ += νᵢ·σᵢ
		si := new(big.Int).SetBytes(tags[q.i])
		tmp.Mul(q.nu, si)
		sigma.Add(sigma, tmp)
		sigma.Mod(sigma, prime)
		// μⱼ += νᵢ·mᵢⱼ
		for j := 0; j < params.SectorsPerBlock; j++ {
			mij := sectorOf(params, data, q.i, j)
			if mij.Sign() == 0 {
				continue
			}
			tmp.Mul(q.nu, mij)
			mu[j].Add(mu[j], tmp)
			mu[j].Mod(mu[j], prime)
		}
	}
	p := Proof{Mu: make([][]byte, params.SectorsPerBlock), Sigma: encodeElem(sigma)}
	for j := range mu {
		p.Mu[j] = encodeElem(mu[j])
	}
	return p, nil
}

// Verify checks a Proof against the challenge using only the secret key — no
// chunk data. Returns true iff the prover demonstrably holds every sampled
// block's bytes. unitID must match the one the tags were made under.
func (k *Key) Verify(unitID []byte, c Challenge, p Proof) bool {
	if len(p.Mu) != k.params.SectorsPerBlock || len(p.Sigma) != ElemBytes {
		return false
	}
	// lhs = σ
	lhs := new(big.Int).SetBytes(p.Sigma)
	if lhs.Cmp(prime) >= 0 {
		return false
	}
	// rhs = Σᵢ νᵢ·f_k(i) + Σⱼ αⱼ·μⱼ
	rhs := new(big.Int)
	tmp := new(big.Int)
	for _, q := range c.expand() {
		tmp.Mul(q.nu, k.prf_i(unitID, q.i))
		rhs.Add(rhs, tmp)
		rhs.Mod(rhs, prime)
	}
	for j := 0; j < k.params.SectorsPerBlock; j++ {
		muj := new(big.Int).SetBytes(p.Mu[j])
		if muj.Cmp(prime) >= 0 {
			return false // non-canonical element: reject
		}
		tmp.Mul(k.alpha[j], muj)
		rhs.Add(rhs, tmp)
		rhs.Mod(rhs, prime)
	}
	return lhs.Cmp(rhs) == 0
}

// --- field & PRF helpers ---

// sector reads sector j of block i out of data as a field element, using this
// key's params. Out-of-range or padding sectors read as zero.
func (k *Key) sector(data []byte, i, j int) *big.Int {
	return sectorOf(k.params, data, i, j)
}

func sectorOf(params Params, data []byte, i, j int) *big.Int {
	off := i*params.blockBytes() + j*SectorBytes
	if off >= len(data) {
		return new(big.Int)
	}
	end := off + SectorBytes
	if end > len(data) {
		end = len(data)
	}
	return new(big.Int).SetBytes(data[off:end])
}

// prf_i = f_k(i): the block's PRF term, HMAC-SHA256(k, "blk" ‖ unitID ‖ i)
// reduced into the field. The reduction bias is ≈ 2⁻²⁴⁸ (256-bit HMAC into a
// 255-bit prime) — negligible.
func (k *Key) prf_i(unitID []byte, i int) *big.Int {
	mac := hmac.New(sha256.New, k.prf[:])
	mac.Write([]byte("silt/por/v1/blk"))
	var lb [4]byte
	binary.BigEndian.PutUint32(lb[:], uint32(len(unitID)))
	mac.Write(lb[:])
	mac.Write(unitID)
	var ib [8]byte
	binary.BigEndian.PutUint64(ib[:], uint64(i))
	mac.Write(ib[:])
	return new(big.Int).Mod(new(big.Int).SetBytes(mac.Sum(nil)), prime)
}

// prfUint derives a 64-bit value from a seed for index sampling.
func prfUint(seed [32]byte, domain string, ctr uint32) uint64 {
	sum := prfBytes(seed, domain, ctr)
	return binary.BigEndian.Uint64(sum[:8])
}

// prfElem derives a field-sized big.Int from a seed (used for coefficients).
func prfElem(seed [32]byte, domain string, ctr uint32) *big.Int {
	sum := prfBytes(seed, domain, ctr)
	return new(big.Int).SetBytes(sum[:])
}

func prfBytes(seed [32]byte, domain string, ctr uint32) [32]byte {
	mac := hmac.New(sha256.New, seed[:])
	mac.Write([]byte("silt/por/v1/"))
	mac.Write([]byte(domain))
	var cb [4]byte
	binary.BigEndian.PutUint32(cb[:], ctr)
	mac.Write(cb[:])
	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}

// randElem draws a uniform field element in [0, p) by rejection sampling.
func randElem(rng io.Reader) (*big.Int, error) {
	var buf [32]byte
	for {
		if _, err := io.ReadFull(rng, buf[:]); err != nil {
			return nil, err
		}
		// clear the top bit so the value is < 2²⁵⁵, then reject the tiny
		// window [p, 2²⁵⁵) so the result is uniform in [0, p).
		buf[0] &= 0x7f
		v := new(big.Int).SetBytes(buf[:])
		if v.Cmp(prime) < 0 {
			return v, nil
		}
	}
}

// encodeElem fixes a field element to 32 bytes big-endian.
func encodeElem(v *big.Int) []byte {
	out := make([]byte, ElemBytes)
	v.FillBytes(out)
	return out
}
