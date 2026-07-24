package memstore_test

import (
	"testing"

	"github.com/nerolabs/bitbit/adapters/memstore"
	"github.com/nerolabs/bitbit/adapters/storetest"
	"github.com/nerolabs/bitbit/ports"
)

func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) ports.ChunkStore { return memstore.New() })
}
