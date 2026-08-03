package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nerolabs/silt/adapters/identity"
)

// cmdID prints the NodeID a daemon would use, WITHOUT launching one — so you can
// fill in `-attesters <ID>` (or `-bootstrap <ID>@ADDR`) before ever starting the
// peer it names. Role-4 validator setup was otherwise chicken-and-egg: to start
// A you needed B's ID, and to start B you bootstrapped to A (acceptance new-F1).
//
// It resolves the identity exactly as `silt daemon` does, so the printed ID is
// the one the daemon will actually use:
//   - `-id-seed N` derives a deterministic identity (nothing is written);
//   - otherwise the persistent keyfile under `-store` is loaded, minting one on
//     first use just as the daemon would (so `silt id` then `silt daemon` on the
//     same store share an identity).
//
// With `-listen ADDR` it also prints the `peer: ID@ADDR` bootstrap line.
func cmdID(args []string) error {
	fs := flag.NewFlagSet("id", flag.ExitOnError)
	idSeed := fs.Int64("id-seed", 0, "derive the identity from a seed (default: the -store keyfile) — matches `silt daemon -id-seed`")
	storeDir := fs.String("store", ".silt-daemon", "store directory holding identity.pem (matches `silt daemon -store`)")
	listen := fs.String("listen", "", "if set, also print the peer: ID@ADDR bootstrap line for this address")
	fs.Parse(args)

	var ident *identity.Identity
	if *idSeed != 0 {
		ident = identity.FromSeed(*idSeed)
	} else {
		// Mirror the daemon: the persistent identity lives in <store>/identity.pem,
		// minted on first use. Create the store dir so the mint can persist (the
		// daemon gets this for free via its chunk store).
		if err := os.MkdirAll(*storeDir, 0o755); err != nil {
			return fmt.Errorf("id: %w", err)
		}
		var err error
		ident, err = identity.LoadOrCreate(filepath.Join(*storeDir, "identity.pem"))
		if err != nil {
			return fmt.Errorf("id: %w", err)
		}
	}
	fmt.Println(ident.NodeID())
	if *listen != "" {
		fmt.Printf("peer: %s@%s\n", ident.NodeID(), *listen)
	}
	return nil
}
