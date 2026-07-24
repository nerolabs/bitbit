package manifest

import (
	"bytes"
	"crypto/sha256"
	"math/rand"
	"testing"

	"shardnet/ports"
)

func randomLeaves(rng *rand.Rand, n int) []ports.Hash {
	leaves := make([]ports.Hash, n)
	for i := range leaves {
		rng.Read(leaves[i][:])
	}
	return leaves
}

func TestMerkleEmptyTreeRoot(t *testing.T) {
	if MerkleRoot(nil) != sha256.Sum256(nil) {
		t.Fatal("empty tree root must be SHA-256 of empty string (RFC 6962)")
	}
}

func TestMerkleRootDependsOnOrder(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	leaves := randomLeaves(rng, 5)
	r1 := MerkleRoot(leaves)
	leaves[0], leaves[1] = leaves[1], leaves[0]
	if MerkleRoot(leaves) == r1 {
		t.Fatal("reordering leaves must change the root")
	}
}

func TestMerkleProofsAllLeafCounts(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for n := 1; n <= 33; n++ { // covers powers of two and every awkward in-between
		leaves := randomLeaves(rng, n)
		root := MerkleRoot(leaves)
		for i := 0; i < n; i++ {
			p, err := Prove(leaves, i)
			if err != nil {
				t.Fatalf("n=%d i=%d: %v", n, i, err)
			}
			if !VerifyProof(root, leaves[i], p) {
				t.Fatalf("n=%d i=%d: valid proof rejected", n, i)
			}
			// Wrong leaf, wrong index, and truncated path must all fail.
			var wrong ports.Hash
			rng.Read(wrong[:])
			if VerifyProof(root, wrong, p) {
				t.Fatalf("n=%d i=%d: proof accepted for wrong leaf", n, i)
			}
			bad := p
			bad.Index = (i + 1) % n
			if n > 1 && VerifyProof(root, leaves[i], bad) {
				t.Fatalf("n=%d i=%d: proof accepted at wrong index", n, i)
			}
			if len(p.Path) > 0 {
				short := p
				short.Path = p.Path[:len(p.Path)-1]
				if VerifyProof(root, leaves[i], short) {
					t.Fatalf("n=%d i=%d: truncated proof accepted", n, i)
				}
			}
		}
	}
}

func validConvergent(n int) *Manifest {
	rng := rand.New(rand.NewSource(3))
	m := &Manifest{Version: Version, Mode: "convergent", ChunkSize: 1024, FileSize: int64(n * 1000)}
	for i := 0; i < n; i++ {
		id := make([]byte, 32)
		secret := make([]byte, 32)
		rng.Read(id)
		rng.Read(secret)
		m.Chunks = append(m.Chunks, id)
		m.ChunkSecrets = append(m.ChunkSecrets, secret)
	}
	return m
}

func TestManifestRoundtripAndCanonicalBytes(t *testing.T) {
	m := validConvergent(7)
	b1, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	m2, err := Unmarshal(b1)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := m2.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatal("marshal→unmarshal→marshal must be byte-identical (the root depends on it)")
	}
	if m2.Root() != m.Root() {
		t.Fatal("root changed across serialization")
	}
}

func TestManifestValidation(t *testing.T) {
	cases := map[string]func(*Manifest){
		"bad version":         func(m *Manifest) { m.Version = 99 },
		"bad mode":            func(m *Manifest) { m.Mode = "rot13" },
		"zero chunk size":     func(m *Manifest) { m.ChunkSize = 0 },
		"negative file size":  func(m *Manifest) { m.FileSize = -1 },
		"short chunk id":      func(m *Manifest) { m.Chunks[0] = m.Chunks[0][:31] },
		"missing secret":      func(m *Manifest) { m.ChunkSecrets = m.ChunkSecrets[:len(m.ChunkSecrets)-1] },
		"stray file key":      func(m *Manifest) { m.FileKey = make([]byte, 32) },
		"manifest key set":    func(m *Manifest) { m.ManifestKey = []byte{1} },
	}
	for name, corrupt := range cases {
		m := validConvergent(3)
		corrupt(m)
		if err := m.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}

	priv := validConvergent(3)
	priv.Mode = "private"
	priv.ChunkSecrets = nil
	priv.FileKey = make([]byte, 32)
	if err := priv.Validate(); err != nil {
		t.Errorf("valid private manifest rejected: %v", err)
	}
	priv.FileKey = priv.FileKey[:16]
	if err := priv.Validate(); err == nil {
		t.Error("short file key: expected validation error")
	}
}

func BenchmarkMerkleRoot1024Leaves(b *testing.B) {
	leaves := randomLeaves(rand.New(rand.NewSource(1)), 1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		MerkleRoot(leaves)
	}
}
