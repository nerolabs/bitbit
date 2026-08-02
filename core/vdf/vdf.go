// Package vdf is a verifiable delay function: evaluating it takes a
// prescribed number of INHERENTLY SEQUENTIAL steps (you cannot parallelize
// your way to the answer), yet the result carries a short proof anyone can
// check almost instantly. It is the "time" in silt's proof-of-space-time
// bond (Gate 4b): a bond proof must bind a fresh challenge to real elapsed,
// non-parallelizable work, so a Sybil cannot retroactively fake having held
// its pledged space across an epoch, nor buy its way out of the wall-clock
// with more cores.
//
// Construction — Wesolowski, "Efficient Verifiable Delay Functions"
// (EUROCRYPT 2019), adopted, not invented (tenet B8). Over a group of
// unknown order (here Z_N^* for an RSA modulus N whose factorisation nobody
// knows):
//
//	eval:    y = x^(2^T) mod N          — T sequential squarings (the delay)
//	prove:   ℓ = HashToPrime(N,x,y,T)
//	         π = x^(⌊2^T / ℓ⌋) mod N     — computed in T steps, no huge exponent
//	verify:  r = 2^T mod ℓ;  check  π^ℓ · x^r ≡ y (mod N)
//
// Sequentiality rests on the belief that computing x^(2^T) needs T squarings
// when the group order is unknown (you cannot reduce the exponent 2^T without
// the order). Soundness rests on Wesolowski's adaptive-root argument: a prover
// who did not actually reach y cannot produce a π that satisfies the check
// except with negligible probability. BOTH assumptions require that no one
// knows the order of the group — i.e. no one knows the factorisation of N.
// Choosing N is therefore a trust anchor, handled by Params (see Default and
// the package notes); the class-group variant removes the anchor entirely and
// is the documented upgrade path, not built here.
//
// This package is pure: it speaks in big integers and bytes, touches no
// store, no network, no clock. Plotting the bond dataset, running the epoch
// proof off the core loop, and checking it on-loop behind bond.Verify are
// separate changes (Gate 4b wiring).
package vdf

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
)

// primeBits is the size of the Fiat–Shamir challenge prime ℓ. 128 bits keeps
// the adaptive-root soundness error at ~2^-128 while keeping verify's π^ℓ a
// cheap 128-bit exponentiation.
const primeBits = 128

// Params fixes the group the VDF runs in. Modulus is an RSA modulus N whose
// factorisation must be UNKNOWN to every participant — that unknown order is
// the whole security assumption (see the package doc). It is a shared,
// public constant, not a per-node secret.
type Params struct {
	Modulus *big.Int
}

// Default returns the shared VDF parameters silt uses: the RSA-2048 modulus
// from the RSA Factoring Challenge. Its factorisation is not publicly known
// (the challenge was withdrawn unsolved), so it is a well-scrutinised
// unknown-order group needing NO fresh trusted setup — an explicit, documented
// launch anchor (a "scaffolding that sheds" per immutable #3). The standard
// caveat applies: whoever generated it once knew the factors and claims to
// have destroyed them; the class-group variant removes the anchor entirely and
// is the recorded upgrade path.
func Default() Params {
	n, ok := new(big.Int).SetString(rsa2048, 10)
	if !ok {
		panic("vdf: bad default modulus") // static constant; cannot fail
	}
	return Params{Modulus: n}
}

// rsa2048 is the RSA-2048 challenge number (617 decimal digits), factorisation
// unknown. See https://en.wikipedia.org/wiki/RSA_numbers#RSA-2048.
const rsa2048 = "25195908475657893494027183240048398571429282126204032027777137836043662020707595556264018525880784406918290641249515082189298559149176184502808489120072844992687392807287776735971418347270261896375014971824691165077613379859095700097330459748808428401797429100642458691817195118746121515172654632282216869987549182422433637259085141865462043576798423387184774447920739934236584823824281198163815010674810451660377306056201619676256133844143603833904414952634432190114657544454178424020924616515723350778707749817125772467962926386356373289912154831438167899885040445364023527381951378636564391212010397122822120720357"

// Validate rejects params that cannot host a sound VDF.
func (p Params) Validate() error {
	if p.Modulus == nil || p.Modulus.Sign() <= 0 {
		return errors.New("vdf: modulus must be a positive integer")
	}
	if p.Modulus.BitLen() < 1024 {
		return fmt.Errorf("vdf: modulus is %d bits; want >= 1024 for a meaningful unknown-order group", p.Modulus.BitLen())
	}
	return nil
}

// Proof is the result of an evaluation: the output y, the Wesolowski proof
// π, and the delay parameter T the pair was produced under. All three are
// needed to verify; y is also the useful VDF output.
type Proof struct {
	Y  []byte // x^(2^T) mod N, big-endian
	Pi []byte // x^(⌊2^T/ℓ⌋) mod N, big-endian
	T  uint64 // number of sequential squarings
}

var (
	one = big.NewInt(1)
	two = big.NewInt(2)
)

// normalizeInput reduces an arbitrary seed into a canonical group element in
// [2, N-1]. Callers pass a domain-bound seed (e.g. the bond root ‖ epoch
// challenge); the exact element does not matter for security as long as
// prover and verifier derive it identically, which they do from the same
// bytes.
func normalizeInput(p Params, seed []byte) *big.Int {
	// Hash the seed to a wide value, then map into [2, N-1] so we avoid the
	// trivial elements 0/1 (whose powers are fixed and would be forgeable).
	h := sha256.Sum256(append([]byte("silt/vdf/v1/input"), seed...))
	x := new(big.Int).SetBytes(h[:])
	x.Mod(x, p.Modulus)
	if x.Cmp(two) < 0 {
		x.Add(x, two) // lift 0/1 out of the fixed points
	}
	return x
}

// Eval computes the VDF over T sequential squarings of the group element
// derived from seed, returning y = x^(2^T) mod N together with the
// Wesolowski proof. The T squarings are the delay: they are inherently
// sequential and cannot be shortcut without knowing the group order.
func Eval(p Params, seed []byte, T uint64) (Proof, error) {
	if err := p.Validate(); err != nil {
		return Proof{}, err
	}
	if T == 0 {
		return Proof{}, errors.New("vdf: T must be positive")
	}
	N := p.Modulus
	x := normalizeInput(p, seed)

	// y = x^(2^T) mod N by T sequential squarings.
	y := new(big.Int).Set(x)
	for i := uint64(0); i < T; i++ {
		y.Mul(y, y)
		y.Mod(y, N)
	}

	pi := prove(N, x, y, T)
	return Proof{Y: y.Bytes(), Pi: pi.Bytes(), T: T}, nil
}

// prove computes π = x^(⌊2^T/ℓ⌋) mod N using Wesolowski's long-division
// recurrence, so it never materialises the T-bit exponent 2^T. It runs in T
// group multiplications:
//
//	r₀ = 1;  for i in 1..T:  b = ⌊2·r/ℓ⌋ ∈ {0,1};  r = 2·r mod ℓ;  π = π²·x^b
func prove(N, x, y *big.Int, T uint64) *big.Int {
	ell := hashToPrime(N, x, y, T)
	pi := big.NewInt(1)
	r := big.NewInt(1)
	tmp := new(big.Int)
	for i := uint64(0); i < T; i++ {
		pi.Mul(pi, pi) // π²
		tmp.Lsh(r, 1)  // 2r
		// b = ⌊2r/ℓ⌋ ∈ {0,1}; if b==1, π *= x and r = 2r - ℓ.
		if tmp.Cmp(ell) >= 0 {
			pi.Mul(pi, x)
			tmp.Sub(tmp, ell)
		}
		pi.Mod(pi, N)
		r.Set(tmp)
	}
	return pi
}

// Verify checks a proof for the element derived from seed in O(log ℓ + log T)
// group operations — no replay of the T squarings. Returns true iff y really
// is x^(2^T) and π attests to the sequential work.
func Verify(p Params, seed []byte, proof Proof) bool {
	if p.Validate() != nil || proof.T == 0 {
		return false
	}
	N := p.Modulus
	x := normalizeInput(p, seed)
	y := new(big.Int).SetBytes(proof.Y)
	pi := new(big.Int).SetBytes(proof.Pi)
	// Reject non-canonical / out-of-range group elements.
	if y.Sign() <= 0 || y.Cmp(N) >= 0 || pi.Sign() <= 0 || pi.Cmp(N) >= 0 {
		return false
	}
	ell := hashToPrime(N, x, y, proof.T)

	// r = 2^T mod ℓ.
	r := new(big.Int).Exp(two, new(big.Int).SetUint64(proof.T), ell)
	// check π^ℓ · x^r ≡ y (mod N)
	lhs := new(big.Int).Exp(pi, ell, N)
	xr := new(big.Int).Exp(x, r, N)
	lhs.Mul(lhs, xr)
	lhs.Mod(lhs, N)
	return lhs.Cmp(y) == 0
}

// hashToPrime derives the Fiat–Shamir challenge prime ℓ deterministically
// from the full transcript (N, x, y, T), so prover and verifier agree and a
// prover cannot grind y against a known ℓ (ℓ depends on y). It returns the
// first probable prime at or above the hash-seeded odd start.
func hashToPrime(N, x, y *big.Int, T uint64) *big.Int {
	h := sha256.New()
	h.Write([]byte("silt/vdf/v1/prime"))
	writeLenPrefixed(h, N.Bytes())
	writeLenPrefixed(h, x.Bytes())
	writeLenPrefixed(h, y.Bytes())
	var tb [8]byte
	binary.BigEndian.PutUint64(tb[:], T)
	h.Write(tb[:])
	seed := h.Sum(nil)

	// Expand the 32-byte seed to primeBits and search upward for a prime.
	cand := new(big.Int).SetBytes(expand(seed, (primeBits+7)/8))
	// Force exactly primeBits and oddness so the search space is well-defined.
	cand.SetBit(cand, primeBits-1, 1)
	cand.SetBit(cand, 0, 1)
	for !cand.ProbablyPrime(20) {
		cand.Add(cand, two)
	}
	return cand
}

// expand stretches a seed to n bytes via SHA-256 in counter mode.
func expand(seed []byte, n int) []byte {
	out := make([]byte, 0, n)
	var ctr uint32
	for len(out) < n {
		h := sha256.New()
		h.Write([]byte("silt/vdf/v1/expand"))
		var cb [4]byte
		binary.BigEndian.PutUint32(cb[:], ctr)
		h.Write(cb[:])
		h.Write(seed)
		out = h.Sum(out)
		ctr++
	}
	return out[:n]
}

func writeLenPrefixed(h interface{ Write([]byte) (int, error) }, b []byte) {
	var lb [4]byte
	binary.BigEndian.PutUint32(lb[:], uint32(len(b)))
	h.Write(lb[:])
	h.Write(b)
}
