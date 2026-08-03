package main

import (
	"flag"
	"fmt"
	"path/filepath"

	"github.com/nerolabs/silt/adapters/chainstore"
)

// cmdChainStatus prints a read-only summary of a validator's committed chain
// replica — head height, head hash, and block/entry counts — WITHOUT launching a
// daemon or mutating anything. It reads the same `chain.cbor` the daemon
// persists. This is how an operator confirms the two things flows 5–7 are about
// without hashing files by hand (acceptance new-F2):
//   - CONVERGENCE: run it on each replica; identical head height AND head hash
//     means they agree byte-for-byte on the committed history.
//   - RESTART CATCH-UP: the head height advances after a restarted validator
//     rejoins, instead of staying stuck at its pre-restart height.
func cmdChainStatus(args []string) error {
	fs := flag.NewFlagSet("chain-status", flag.ExitOnError)
	storeDir := fs.String("store", ".silt-daemon", "store directory holding chain.cbor (matches `silt daemon -store`)")
	fs.Parse(args)

	path := filepath.Join(*storeDir, "chain.cbor")
	blocks, err := chainstore.Load(path)
	if err != nil {
		return fmt.Errorf("chain-status: %w", err)
	}
	fmt.Printf("chain-status (%s):\n", path)
	if len(blocks) == 0 {
		fmt.Println("  no chain yet (0 blocks) — this node isn't a validator, or nothing has committed")
		return nil
	}
	head := blocks[len(blocks)-1]
	headHash := head.Hash()
	entries := 0
	for i := range blocks {
		entries += len(blocks[i].Entries)
	}
	fmt.Printf("  head height:  %d\n", head.Height)
	fmt.Printf("  head hash:    %s\n", headHash)
	fmt.Printf("  blocks:       %d (incl. genesis)\n", len(blocks))
	fmt.Printf("  entries:      %d committed\n", entries)
	fmt.Println("  → identical values across replicas mean they agree on the committed history")
	return nil
}
