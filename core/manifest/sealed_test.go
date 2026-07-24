package manifest

import (
	"crypto/sha256"
	"testing"
)

func keys() (layout, content [32]byte) {
	return sha256.Sum256([]byte("layout")), sha256.Sum256([]byte("content"))
}

func TestSealOpenFullRoundtrip(t *testing.T) {
	m := validConvergent(5)
	lk, ck := keys()
	blob, err := Seal(m, lk, ck)
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenFull(blob, lk, ck)
	if err != nil {
		t.Fatal(err)
	}
	if got.Root() != m.Root() || got.Mode != m.Mode || got.FileSize != m.FileSize ||
		len(got.ChunkSecrets) != len(m.ChunkSecrets) {
		t.Fatalf("full open mangled the manifest: %+v", got)
	}
}

// The privilege separation: the layout key alone yields structure but
// no secrets; the wrong key yields nothing at all.
func TestLayoutKeySeesStructureNotSecrets(t *testing.T) {
	m := validConvergent(5)
	lk, ck := keys()
	blob, err := Seal(m, lk, ck)
	if err != nil {
		t.Fatal(err)
	}
	l, err := OpenLayout(blob, lk)
	if err != nil {
		t.Fatal(err)
	}
	if l.Root() != m.Root() {
		t.Fatal("layout must reproduce the Merkle root (repair depends on it)")
	}
	if len(l.ChunkIDs()) != 5 || l.ChunkSize != m.ChunkSize {
		t.Fatal("layout missing structure")
	}
	// The box does not open with the layout key.
	if _, err := OpenFull(blob, lk, lk); err == nil {
		t.Fatal("layout key must not open the secrets box")
	}
	// And a wrong layout key opens nothing.
	wrong := sha256.Sum256([]byte("wrong"))
	if _, err := OpenLayout(blob, wrong); err == nil {
		t.Fatal("wrong layout key must fail")
	}
}

func TestSealIsDeterministic(t *testing.T) {
	m := validConvergent(3)
	lk, ck := keys()
	b1, err := Seal(m, lk, ck)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := Seal(m, lk, ck)
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != string(b2) {
		t.Fatal("sealing must be deterministic — convergent dedup of manifests depends on it")
	}
}

func TestSealedBlobTamperDetected(t *testing.T) {
	m := validConvergent(3)
	lk, ck := keys()
	blob, _ := Seal(m, lk, ck)
	blob[len(blob)/2] ^= 1
	if _, err := OpenLayout(blob, lk); err == nil {
		t.Fatal("tampered blob must fail authentication")
	}
}
