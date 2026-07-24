package memstore_test

import (
	"testing"

	"shardnet/adapters/memstore"
	"shardnet/adapters/storetest"
	"shardnet/ports"
)

func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) ports.ChunkStore { return memstore.New() })
}
