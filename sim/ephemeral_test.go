package sim

import (
	"testing"

	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/ports"
)

// #43 part 2: a short-lived publish/fetch client (SetEphemeral) messages
// storage nodes and then dies. Receivers must NOT route to it — otherwise
// every publish injects a ghost that later costs a full timeout to evict,
// and at scale that drowns lookups (the 300-file test: 327 routing entries,
// 2 live, ~75% query timeouts). A non-ephemeral peer that makes the same
// contact IS routed, so discovery is untouched.
func TestEphemeralSenderNotRouted(t *testing.T) {
	cl := NewCluster(11, 5, simnet.DefaultConfig(), node.DefaultConfig())
	server := cl.Nodes[0]

	inServerTable := func(id ports.NodeID) bool {
		for _, got := range server.Table().Closest(server.ID(), 1024) {
			if got == id {
				return true
			}
		}
		return false
	}

	// An ephemeral client joins the same network and queries the server
	// (as a publish would touch it), then would vanish.
	var eid ports.NodeID
	eid[0], eid[1] = 0xEE, 0x01
	ephem := node.New(eid, node.DefaultConfig(), cl.Sched, cl.Net.Endpoint(eid), memstore.New())
	ephem.SetEphemeral(true)
	ephem.Bootstrap([]ports.NodeID{server.ID()}, func() {})
	cl.Sched.Run()

	if inServerTable(eid) {
		t.Fatal("server routed to an ephemeral client — routing table poisoned (#43 pt2)")
	}

	// Control: a normal node making the identical contact MUST be routed,
	// proving we didn't break discovery for real peers.
	var nid ports.NodeID
	nid[0], nid[1] = 0x77, 0x01
	normal := node.New(nid, node.DefaultConfig(), cl.Sched, cl.Net.Endpoint(nid), memstore.New())
	normal.Bootstrap([]ports.NodeID{server.ID()}, func() {})
	cl.Sched.Run()

	if !inServerTable(nid) {
		t.Fatal("control: a normal peer that contacted the server should be routed")
	}
}
