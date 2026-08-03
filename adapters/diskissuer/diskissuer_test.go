package diskissuer

import (
	"crypto/rand"
	"os"
	"testing"
)

// The restart property: a validator that generated an issuer key on first run
// gets the SAME key back on the next start, so the tokens it blind-signed stay
// verifiable and peers' cached issuer keys don't go stale.
func TestLoadOrCreateIsStableAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	first, err := s1.LoadOrCreate(rand.Reader)
	if err != nil {
		t.Fatalf("first LoadOrCreate: %v", err)
	}

	// A fresh Store on the same dir (a restart) must reload, not regenerate.
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	second, err := s2.LoadOrCreate(rand.Reader)
	if err != nil {
		t.Fatalf("second LoadOrCreate: %v", err)
	}
	if first.N.Cmp(second.N) != 0 || first.D.Cmp(second.D) != 0 {
		t.Fatal("a restart minted a NEW issuer key — outstanding tokens and cached peer keys would break")
	}
}

// Save then Load round-trips the exact key.
func TestSaveLoadRoundTrip(t *testing.T) {
	s, _ := Open(t.TempDir())
	k, err := s.LoadOrCreate(rand.Reader)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, ok, err := s.Load()
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if got.N.Cmp(k.N) != 0 || got.D.Cmp(k.D) != 0 || got.PublicKey.E != k.PublicKey.E {
		t.Fatal("loaded key differs from the saved one")
	}
}

// No key yet loads cleanly as absent (not an error), so first run mints one.
func TestLoadAbsent(t *testing.T) {
	s, _ := Open(t.TempDir())
	if _, ok, err := s.Load(); ok || err != nil {
		t.Fatalf("absent key: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

// A corrupt key file is a real error — the daemon must not silently mint a new
// issuer identity over a damaged one.
func TestLoadCorrupt(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	if _, err := s.LoadOrCreate(rand.Reader); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := os.WriteFile(s.path, []byte("not a key"), 0o600); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	if _, ok, err := s.Load(); ok || err == nil {
		t.Fatalf("corrupt key should error, got ok=%v err=%v", ok, err)
	}
}
