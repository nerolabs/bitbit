package sim

import (
	"testing"

	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/ports"
)

// #43: peers.json must persist only peers we have actually reached, so a
// warm restart re-seeds from live peers instead of reloading a graveyard of
// dead (e.g. ephemeral publisher) identities that then drown lookups in
// timeouts. ReachablePeers backs that filter: a peer enters the set on a
// successful reply and leaves it on a timeout.
func TestReachablePeersTracksLiveRoundTrips(t *testing.T) {
	cl := NewCluster(7, 6, simnet.DefaultConfig(), node.DefaultConfig())
	// A late joiner: it bootstrapped against earlier nodes, so its table is
	// populated (Nodes[0] is the genesis node and has queried no one).
	a := cl.Nodes[len(cl.Nodes)-1]

	// The live cluster members, excluding A itself.
	live := map[ports.NodeID]bool{}
	for _, nd := range cl.Nodes {
		if nd.ID() != a.ID() {
			live[nd.ID()] = true
		}
	}

	// Drive a lookup so A round-trips FindNode with real peers; each reply
	// marks that peer reachable. Every reachable peer must then be a live
	// member (never self, never a ghost).
	a.IterativeFindNode(a.ID(), func([]ports.NodeID) {})
	cl.Sched.Run()
	reached := a.ReachablePeers()
	if len(reached) == 0 {
		t.Fatal("after joining, node should have reached at least one peer")
	}
	for id := range reached {
		if id == a.ID() {
			t.Fatal("a node must never mark itself reachable")
		}
		if !live[id] {
			t.Fatalf("reachable peer %x is not a live cluster member", id)
		}
	}

	// Kill a reached peer and force A to query it: the timeout must drop it
	// from the reachable set, so it is never persisted as a dead seed.
	var victim ports.NodeID
	for id := range reached {
		victim = id
		break
	}
	cl.Net.Kill(victim)
	a.IterativeFindNode(victim, func([]ports.NodeID) {})
	cl.Sched.Run() // drains events, including the request timeout

	if a.ReachablePeers()[victim] {
		t.Fatalf("a peer that timed out must be dropped from reachable, %x still present", victim)
	}
}
