package tcpnet_test

// Regression tests from the first real cross-network run (issue #27):
// a NATed peer can dial out but can never be dialed, so a request/reply
// exchange only works if the reply rides the requester's own inbound
// conn — and a wildcard bind must never be advertised, or peers "learn"
// an address that dials their own loopback.

import (
	"testing"
	"time"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/tcpnet"
	"github.com/nerolabs/silt/adapters/walltime"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/ports"
)

// TestReplyOverInboundConn is the field failure in miniature: B binds a
// wildcard (so it advertises nothing, like a NATed home node), A is
// never told any address for B, and yet A's reply must arrive — on the
// conn B opened.
func TestReplyOverInboundConn(t *testing.T) {
	identA, identB := identity.FromSeed(51), identity.FromSeed(52)
	trA, err := tcpnet.New(newLoop(t), identA, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer trA.Close()
	trB, err := tcpnet.New(newLoop(t), identB, "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer trB.Close()

	trA.SetHandler(func(from ports.NodeID, msg ports.Message) {
		trA.Send(from, ports.Message{Kind: ports.MsgFindNodeReply, RID: msg.RID})
	})
	got := make(chan ports.Message, 1)
	trB.SetHandler(func(from ports.NodeID, msg ports.Message) {
		if from == identA.NodeID() {
			got <- msg
		}
	})

	trB.AddPeer(identA.NodeID(), trA.Addr())
	if err := trB.Send(identA.NodeID(), ports.Message{Kind: ports.MsgFindNode, RID: 9}); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-got:
		if msg.RID != 9 {
			t.Fatalf("reply RID = %d, want 9", msg.RID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reply never arrived — an undialable peer must be answered over its own conn")
	}
	// And the wildcard must not have been learned as an address: there
	// is nothing dialable to learn.
	if peers := trA.Peers(); len(peers) != 0 {
		t.Fatalf("A learned an address for the wildcard-bound B: %v", peers)
	}
}

// TestReachabilityNotFooledByConnReuse: with replies riding live conns,
// a NATed checker CAN converse — but the reachability dial-back must
// still report it NATed, because the dial-back is required to be a
// fresh inbound dial, and no address for the checker exists.
func TestReachabilityNotFooledByConnReuse(t *testing.T) {
	identA, identB := identity.FromSeed(53), identity.FromSeed(54)
	loopA, loopB := newLoop(t), newLoop(t)
	trA, err := tcpnet.New(loopA, identA, "0.0.0.0:0") // "NATed": advertises nothing
	if err != nil {
		t.Fatal(err)
	}
	defer trA.Close()
	trB, err := tcpnet.New(loopB, identB, "127.0.0.1:0") // the public helper
	if err != nil {
		t.Fatal(err)
	}
	defer trB.Close()

	cfg := node.DefaultConfig()
	cfg.RequestTimeout = ports.Duration(2 * time.Second)
	cfg.ReachabilityTimeout = ports.Duration(1 * time.Second)
	ndA := node.New(identA.NodeID(), cfg, walltime.New(loopA), trA, memstore.New())
	node.New(identB.NodeID(), cfg, walltime.New(loopB), trB, memstore.New())

	trA.AddPeer(identB.NodeID(), trB.Addr())
	type result struct {
		table     int
		reachable bool
	}
	got := make(chan result, 1)
	loopA.Post(func() {
		ndA.Bootstrap([]ports.NodeID{identB.NodeID()}, func() {
			table := ndA.Table().Size()
			ndA.CheckReachability([]ports.NodeID{identB.NodeID()}, func(ok bool) {
				got <- result{table: table, reachable: ok}
			})
		})
	})
	select {
	case r := <-got:
		// The join itself must work now — that's the reply-over-conn fix.
		if r.table == 0 {
			t.Fatal("bootstrap found nobody: replies to an undialable node were lost")
		}
		// But conversing is not the same as being dialable.
		if r.reachable {
			t.Fatal("an undialable node was declared public — the dial-back reused a conn")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("reachability check never settled")
	}
}
