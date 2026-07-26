package sim

import (
	"testing"

	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/ports"
)

// Reachability check (our AutoNAT): a node learns whether it is publicly
// dialable by asking a helper to dial it back. A landed dial-back is the
// proof of reachability; silence is read, conservatively, as "behind NAT."

func TestReachabilityConfirmedByHelper(t *testing.T) {
	cl := NewCluster(7, 6, simnet.DefaultConfig(), node.DefaultConfig())
	a, helper := cl.Nodes[0], cl.Nodes[1]

	var got, fired bool
	a.CheckReachability([]ports.NodeID{helper.ID()}, func(reachable bool) {
		got, fired = reachable, true
	})
	cl.Sched.Run()

	if !fired {
		t.Fatal("reachability callback never fired")
	}
	if !got {
		t.Fatal("a live helper's dial-back should confirm reachability")
	}
}

func TestReachabilityTimesOutWhenUnconfirmed(t *testing.T) {
	cl := NewCluster(7, 6, simnet.DefaultConfig(), node.DefaultConfig())
	a, helper := cl.Nodes[0], cl.Nodes[1]
	cl.Net.Kill(helper.ID()) // no one can dial us back

	var got, fired bool
	a.CheckReachability([]ports.NodeID{helper.ID()}, func(reachable bool) {
		got, fired = reachable, true
	})
	cl.Sched.Run() // drains events, including the reachability timeout

	if !fired {
		t.Fatal("reachability callback never fired")
	}
	if got {
		t.Fatal("with no dial-back, the node must conclude it is NATed")
	}
}

func TestReachabilityNoHelpers(t *testing.T) {
	cl := NewCluster(7, 6, simnet.DefaultConfig(), node.DefaultConfig())

	got, fired := true, false
	cl.Nodes[0].CheckReachability(nil, func(reachable bool) { got, fired = reachable, true })

	if !fired {
		t.Fatal("callback should fire synchronously when there are no helpers")
	}
	if got {
		t.Fatal("no helpers means unconfirmed, which must read as NATed")
	}
}
