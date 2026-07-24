// Package erasure wraps github.com/klauspost/reedsolomon with shardnet's
// stripe conventions. See docs/math/03-reed-solomon.md for why this
// works.
//
// Geometry (confirmed at M1 review): a stripe is k consecutive
// ciphertext chunks of one file. Reed-Solomon extends each stripe with
// n-k parity shards of the same size. ANY k of the stripe's n shards
// reconstruct all of it — so up to n-k shards per stripe can vanish
// before the file loses a single bit. Every shard, data or parity, is
// content-addressed and stored like any other chunk.
//
// The final stripe of a file usually has fewer than k real chunks. The
// missing data positions are treated as implicit all-zero shards: they
// participate in parity math but are never stored, never hashed, and are
// "always available" at reconstruction time. This keeps one (k, n) code
// for the whole file with no stored padding.
package erasure

import (
	"bytes"
	"fmt"

	"github.com/klauspost/reedsolomon"
)

// Params is the (k, n) code: k data shards, n total, n-k parity.
type Params struct {
	K int // data shards per stripe
	N int // total shards per stripe
}

// DefaultParams tolerates the loss of any 6 of every 16 shards at 1.6x
// storage overhead.
var DefaultParams = Params{K: 10, N: 16}

func (p Params) Validate() error {
	if p.K < 1 || p.N <= p.K {
		return fmt.Errorf("erasure: need 1 <= k < n, got k=%d n=%d", p.K, p.N)
	}
	if p.N > 256 {
		return fmt.Errorf("erasure: n=%d exceeds 256 (GF(2^8) shard limit)", p.N)
	}
	return nil
}

// ParityShards returns the number of parity shards per stripe.
func (p Params) ParityShards() int { return p.N - p.K }

// Stripes returns how many stripes cover m data chunks.
func (p Params) Stripes(m int) int { return (m + p.K - 1) / p.K }

// EncodeStripe computes the n-k parity shards for one stripe. data holds
// the stripe's real chunks (1 to k of them, equal length); a short count
// means this is the final stripe and the remainder is implicit zeros.
func EncodeStripe(p Params, data [][]byte) ([][]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > p.K {
		return nil, fmt.Errorf("erasure: stripe has %d data shards, want 1..%d", len(data), p.K)
	}
	size := len(data[0])
	if size == 0 {
		return nil, fmt.Errorf("erasure: empty shards")
	}
	for i, d := range data {
		if len(d) != size {
			return nil, fmt.Errorf("erasure: shard %d has size %d, want %d", i, len(d), size)
		}
	}
	enc, err := reedsolomon.New(p.K, p.ParityShards())
	if err != nil {
		return nil, err
	}
	shards := make([][]byte, p.N)
	for i := 0; i < p.K; i++ {
		if i < len(data) {
			shards[i] = data[i]
		} else {
			shards[i] = make([]byte, size) // implicit zero shard
		}
	}
	for i := p.K; i < p.N; i++ {
		shards[i] = make([]byte, size)
	}
	if err := enc.Encode(shards); err != nil {
		return nil, err
	}
	return shards[p.K:], nil
}

// ReconstructStripe recovers the missing data shards of one stripe.
// shards must have length n in stripe order (data first, parity after),
// with nil marking a missing shard; realData says how many leading data
// positions are real chunks (the rest are implicit zeros and may be
// passed as nil — they are filled in for free). On success the first
// realData entries of shards are all non-nil and verified against the
// parity math. Below k available shards it fails loudly.
func ReconstructStripe(p Params, shards [][]byte, realData int) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if len(shards) != p.N {
		return fmt.Errorf("erasure: got %d shard slots, want n=%d", len(shards), p.N)
	}
	if realData < 1 || realData > p.K {
		return fmt.Errorf("erasure: realData=%d, want 1..%d", realData, p.K)
	}
	size := 0
	for _, s := range shards {
		if s != nil {
			size = len(s)
			break
		}
	}
	if size == 0 {
		return fmt.Errorf("erasure: no shards available at all")
	}
	// Implicit zero shards are always "available".
	for i := realData; i < p.K; i++ {
		if shards[i] == nil {
			shards[i] = make([]byte, size)
		} else if !bytes.Equal(shards[i], make([]byte, size)) {
			return fmt.Errorf("erasure: padding shard %d is non-zero", i)
		}
	}
	available := 0
	for _, s := range shards {
		if s != nil {
			available++
		}
	}
	if available < p.K {
		return fmt.Errorf("erasure: only %d of %d shards available, need at least k=%d — data is unrecoverable", available, p.N, p.K)
	}
	enc, err := reedsolomon.New(p.K, p.ParityShards())
	if err != nil {
		return err
	}
	if err := enc.Reconstruct(shards); err != nil {
		return fmt.Errorf("erasure: reconstruct: %w", err)
	}
	// Belt and braces: check the full stripe is internally consistent.
	ok, err := enc.Verify(shards)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("erasure: reconstructed stripe fails parity verification")
	}
	return nil
}
