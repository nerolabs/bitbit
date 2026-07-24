package crypto

import (
	"bytes"
	"math/rand"
	"testing"
	"testing/quick"
)

func TestConvergentRoundtripProperty(t *testing.T) {
	f := func(pt []byte) bool {
		ct, secret, err := ConvergentEncrypt(pt)
		if err != nil {
			return false
		}
		got, err := ConvergentDecrypt(ct, secret)
		if err != nil {
			return false
		}
		return bytes.Equal(got, pt)
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 300}); err != nil {
		t.Fatal(err)
	}
}

func TestConvergentIsDeterministic(t *testing.T) {
	pt := []byte("the same plaintext, encrypted twice")
	ct1, s1, err := ConvergentEncrypt(pt)
	if err != nil {
		t.Fatal(err)
	}
	ct2, s2, err := ConvergentEncrypt(pt)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ct1, ct2) || s1 != s2 {
		t.Fatal("identical plaintext must produce identical ciphertext (dedup depends on it)")
	}
}

func TestConvergentDistinctPlaintextsDiffer(t *testing.T) {
	ct1, _, _ := ConvergentEncrypt([]byte("aaaa"))
	ct2, _, _ := ConvergentEncrypt([]byte("aaab"))
	if bytes.Equal(ct1, ct2) {
		t.Fatal("different plaintexts produced identical ciphertext")
	}
}

func TestConvergentRejectsTamper(t *testing.T) {
	ct, secret, _ := ConvergentEncrypt([]byte("hello world"))
	ct[0] ^= 1
	if _, err := ConvergentDecrypt(ct, secret); err == nil {
		t.Fatal("tampered ciphertext must fail authentication")
	}
}

func TestConvergentRejectsWrongSecret(t *testing.T) {
	ct, secret, _ := ConvergentEncrypt([]byte("hello world"))
	secret[0] ^= 1
	if _, err := ConvergentDecrypt(ct, secret); err == nil {
		t.Fatal("wrong secret must fail")
	}
}

func TestPrivateRoundtripProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	key, err := NewFileKey(rng)
	if err != nil {
		t.Fatal(err)
	}
	f := func(pt []byte, index uint64) bool {
		ct, err := PrivateEncrypt(key, index, pt)
		if err != nil {
			return false
		}
		got, err := PrivateDecrypt(key, index, ct)
		if err != nil {
			return false
		}
		return bytes.Equal(got, pt)
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 300}); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateBindsChunkIndex(t *testing.T) {
	key, _ := NewFileKey(rand.New(rand.NewSource(1)))
	ct, _ := PrivateEncrypt(key, 3, []byte("chunk three"))
	if _, err := PrivateDecrypt(key, 4, ct); err == nil {
		t.Fatal("chunk encrypted at index 3 must not decrypt at index 4 (reordering attack)")
	}
}

func TestPrivateIsRandomizedAcrossFiles(t *testing.T) {
	k1, _ := NewFileKey(rand.New(rand.NewSource(1)))
	k2, _ := NewFileKey(rand.New(rand.NewSource(2)))
	pt := []byte("same content, different files")
	ct1, _ := PrivateEncrypt(k1, 0, pt)
	ct2, _ := PrivateEncrypt(k2, 0, pt)
	if bytes.Equal(ct1, ct2) {
		t.Fatal("private mode must not produce equal ciphertext under different file keys")
	}
}

func TestFileKeyIsDeterministicGivenRNG(t *testing.T) {
	k1, _ := NewFileKey(rand.New(rand.NewSource(7)))
	k2, _ := NewFileKey(rand.New(rand.NewSource(7)))
	if k1 != k2 {
		t.Fatal("same injected RNG seed must yield same key — sim determinism depends on injection")
	}
}

func BenchmarkConvergentEncrypt64KiB(b *testing.B) {
	pt := make([]byte, 64<<10)
	rand.New(rand.NewSource(1)).Read(pt)
	b.SetBytes(int64(len(pt)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := ConvergentEncrypt(pt); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPrivateEncrypt64KiB(b *testing.B) {
	key, _ := NewFileKey(rand.New(rand.NewSource(1)))
	pt := make([]byte, 64<<10)
	b.SetBytes(int64(len(pt)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := PrivateEncrypt(key, uint64(i), pt); err != nil {
			b.Fatal(err)
		}
	}
}
