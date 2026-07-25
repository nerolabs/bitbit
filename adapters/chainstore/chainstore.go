// Package chainstore persists a chain replica to disk (canonical CBOR,
// atomic replace) so a restarted validator daemon rejoins with its
// history — and re-validates every block on load, because trusting your
// own disk is still trusting.
package chainstore

import (
	"os"

	"github.com/nerolabs/bitbit/core/chain"
)

// Load reads blocks from path; a missing file is an empty history.
func Load(path string) ([]chain.Block, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return chain.DecodeBlocks(raw)
}

// Save writes the full chain atomically. Chains of registry entries
// are small (that's the design); rewriting whole is simpler than
// appending safely.
func Save(path string, blocks []chain.Block) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, chain.EncodeBlocks(blocks), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Replay loads path into c, re-validating every block. Returns how many
// blocks were restored.
func Replay(path string, c *chain.Chain) (int, error) {
	blocks, err := Load(path)
	if err != nil {
		return 0, err
	}
	for i, b := range blocks {
		if err := c.Append(b); err != nil {
			return i, err
		}
	}
	return len(blocks), nil
}
