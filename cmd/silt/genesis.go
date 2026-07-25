package main

import (
	"flag"
	"fmt"

	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/core/genesis"
)

// cmdGenesis prints the founding block that every fresh Silt network
// carries: the manifesto, its deterministic link (the same on every
// machine), and the genesis block hash. Swap core/genesis/manifesto.txt
// and all three change together — nothing here is a magic constant.
func cmdGenesis(args []string) error {
	fs := flag.NewFlagSet("genesis", flag.ExitOnError)
	full := fs.Bool("text", false, "also print the full manifesto")
	fs.Parse(args)

	block, h, entry, err := genesis.Build(memstore.New())
	if err != nil {
		return err
	}
	bh := block.Hash()
	fmt.Printf("genesis block:  %s (height 0, %d entry)\n", bh, len(block.Entries))
	fmt.Printf("genesis link:   %s\n", h)
	fmt.Printf("manifesto root: %s (%d bytes)\n", entry.Root, entry.FileSize)
	if *full {
		fmt.Printf("\n%s\n", genesis.Manifesto)
	}
	return nil
}
