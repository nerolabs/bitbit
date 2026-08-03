// Package chainstore persists a chain replica to disk (canonical CBOR,
// atomic replace) so a restarted validator daemon rejoins with its
// history. On load it re-verifies every block's cryptographic integrity —
// hashes and signatures — because trusting your own disk is still trusting;
// but it does NOT re-gate our own committed history on the live reputation
// view (which is empty at boot), or a restart would be stranded at genesis
// (F1). Reputation is re-earned live via bond audits; catch-up on blocks
// missed while down is a separate, peer-facing path (Node.SyncChain).
package chainstore

import (
	"os"

	"github.com/nerolabs/silt/core/chain"
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

// Replay loads path into c and reloads our own persisted history via
// chain.Reload — re-verifying each block's structure and signatures, but not
// the live reputation gate (see the package doc and chain.appendStructural).
// Returns how many blocks were restored.
func Replay(path string, c *chain.Chain) (int, error) {
	blocks, err := Load(path)
	if err != nil {
		return 0, err
	}
	return c.Reload(blocks)
}
