package vdf

import (
	"math/big"
	"testing"
)

// testParams builds a deterministic ~1200-bit modulus N = p·q from two fixed
// primes. NOTE: the test knows the factorisation, which is fine for testing
// CORRECTNESS of the arithmetic and proofs — the VDF's *security* rests on
// the factorisation being unknown in deployment, a trust-anchor property, not
// something a unit test exercises.
func testParams(t *testing.T) Params {
	t.Helper()
	p := nextPrime(new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 600), big.NewInt(12345)))
	q := nextPrime(new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 601), big.NewInt(67890)))
	return Params{Modulus: new(big.Int).Mul(p, q)}
}

func nextPrime(n *big.Int) *big.Int {
	c := new(big.Int).Set(n)
	c.SetBit(c, 0, 1) // odd
	for !c.ProbablyPrime(20) {
		c.Add(c, two)
	}
	return c
}

// The promise: an honest evaluation always verifies, and verify is not a
// replay of the work — it just checks the short proof.
func TestEvalVerifyRoundTrip(t *testing.T) {
	p := testParams(t)
	seed := []byte("bond-root ‖ epoch-42 challenge")
	proof, err := Eval(p, seed, 2000)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if !Verify(p, seed, proof) {
		t.Fatal("honest proof did not verify")
	}
}

// The T sequential squarings must equal the direct exponentiation x^(2^T):
// pins the delay loop against a reference computation for a small T.
func TestEvalMatchesDirectExponentiation(t *testing.T) {
	p := testParams(t)
	seed := []byte("reference")
	const T = 64
	proof, err := Eval(p, seed, T)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	x := normalizeInput(p, seed)
	exp := new(big.Int).Lsh(big.NewInt(1), T) // 2^T
	want := new(big.Int).Exp(x, exp, p.Modulus)
	if new(big.Int).SetBytes(proof.Y).Cmp(want) != 0 {
		t.Fatal("y != x^(2^T): the sequential squaring loop is wrong")
	}
}

// Determinism: the VDF and its proof are a pure function of (params, seed, T).
func TestDeterministic(t *testing.T) {
	p := testParams(t)
	seed := []byte("same-seed")
	a, _ := Eval(p, seed, 1500)
	b, _ := Eval(p, seed, 1500)
	if string(a.Y) != string(b.Y) || string(a.Pi) != string(b.Pi) {
		t.Fatal("same inputs produced different output/proof")
	}
}

// A verifier keyed to a different challenge (seed) rejects the proof — the
// output is bound to the exact input element, so a proof can't be replayed
// onto another epoch's challenge.
func TestWrongSeedFails(t *testing.T) {
	p := testParams(t)
	proof, _ := Eval(p, []byte("challenge-A"), 1200)
	if Verify(p, []byte("challenge-B"), proof) {
		t.Fatal("a proof verified against the wrong challenge")
	}
}

// Claiming a different delay T than the proof was made for fails: T is bound
// into the Fiat–Shamir prime and the r = 2^T mod ℓ check.
func TestWrongTFails(t *testing.T) {
	p := testParams(t)
	seed := []byte("delay")
	proof, _ := Eval(p, seed, 1000)
	proof.T = 1001
	if Verify(p, seed, proof) {
		t.Fatal("a proof verified under a T it was not produced for")
	}
}

// Tampering with the output y or the proof π is caught.
func TestTamperedFails(t *testing.T) {
	p := testParams(t)
	seed := []byte("tamper")
	base, _ := Eval(p, seed, 1000)

	bumpY := base
	y := new(big.Int).SetBytes(base.Y)
	y.Add(y, one)
	bumpY.Y = y.Bytes()
	if Verify(p, seed, bumpY) {
		t.Fatal("a tampered output y verified")
	}

	bumpPi := base
	pi := new(big.Int).SetBytes(base.Pi)
	pi.Add(pi, one)
	bumpPi.Pi = pi.Bytes()
	if Verify(p, seed, bumpPi) {
		t.Fatal("a tampered proof π verified")
	}
}

// A cheater who did NOT do the work — it fabricates an output and offers a
// bogus proof — cannot pass. Here the attacker takes the true output but
// supplies π = 1 (the trivial "I did nothing" proof), and separately claims a
// plausible-looking output with the real proof for a shorter computation.
func TestForgedProofFails(t *testing.T) {
	p := testParams(t)
	seed := []byte("forge")
	honest, _ := Eval(p, seed, 3000)

	// (a) real y, trivial proof.
	forged := Proof{Y: honest.Y, Pi: one.Bytes(), T: 3000}
	if Verify(p, seed, forged) {
		t.Fatal("a trivial proof π=1 verified for a nontrivial delay")
	}

	// (b) the attacker only did 1500 squarings but claims 3000. Its (y, π) are
	// internally consistent for T=1500, so relabelling them 3000 must fail.
	short, _ := Eval(p, seed, 1500)
	relabelled := Proof{Y: short.Y, Pi: short.Pi, T: 3000}
	if Verify(p, seed, relabelled) {
		t.Fatal("a shorter computation passed as a longer one")
	}
}

// Non-canonical group elements (out of [1, N)) are rejected rather than
// wrapped, closing a malleability seam.
func TestNonCanonicalRejected(t *testing.T) {
	p := testParams(t)
	seed := []byte("canon")
	base, _ := Eval(p, seed, 500)

	oversize := base
	oversize.Y = new(big.Int).Add(p.Modulus, big.NewInt(5)).Bytes() // y >= N
	if Verify(p, seed, oversize) {
		t.Fatal("an out-of-range y verified")
	}
	zero := base
	zero.Pi = new(big.Int).SetInt64(0).Bytes()
	if Verify(p, seed, zero) {
		t.Fatal("a zero π verified")
	}
}

func TestParamsAndInputValidation(t *testing.T) {
	small := Params{Modulus: big.NewInt(1_000_003)} // way under 1024 bits
	if err := small.Validate(); err == nil {
		t.Fatal("a tiny modulus should be rejected")
	}
	if _, err := Eval(small, []byte("x"), 10); err == nil {
		t.Fatal("Eval should reject bad params")
	}
	p := testParams(t)
	if _, err := Eval(p, []byte("x"), 0); err == nil {
		t.Fatal("Eval should reject T=0")
	}
	if Verify(p, []byte("x"), Proof{T: 0}) {
		t.Fatal("Verify should reject T=0")
	}
}
