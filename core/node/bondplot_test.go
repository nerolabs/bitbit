package node

import (
	"testing"

	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/bond"
	"github.com/nerolabs/silt/core/vdf"
	"github.com/nerolabs/silt/ports"
)

// countingPlotStore is an in-memory ports.PlotStore that records how often it
// plots (Save) versus reloads (Load hit), so a test can prove a restart
// RELOADS instead of re-plotting (#93).
type countingPlotStore struct {
	root   ports.Hash
	blocks [][]byte
	saved  bool
	saves  int
	loads  int
}

func (p *countingPlotStore) Save(_ ports.NodeID, root ports.Hash, blocks [][]byte) error {
	p.root, p.blocks, p.saved = root, blocks, true
	p.saves++
	return nil
}

func (p *countingPlotStore) Load(_ ports.NodeID) (ports.Hash, [][]byte, bool, error) {
	if !p.saved {
		return ports.Hash{}, nil, false, nil
	}
	p.loads++
	return p.root, p.blocks, true, nil
}

func newBondNode(t *testing.T, id ports.NodeID, store ports.PlotStore) *Node {
	t.Helper()
	net := simnet.New(simclock.New(), 1, simnet.DefaultConfig())
	n := New(id, DefaultConfig(), simclock.New(), net.Endpoint(id), nil)
	n.SetPlotStore(store)
	return n
}

// The restart outcome: a node with a persisted plot reloads it on the next
// start instead of re-plotting the deliberately-expensive dataset, keeps the
// same committed root, and can still answer a space-time challenge from the
// reloaded plot.
func TestEnableBondReloadsInsteadOfReplotting(t *testing.T) {
	id := ports.HashBytes([]byte("validator-A"))
	store := &countingPlotStore{}
	const size = 1 << 20

	first := newBondNode(t, id, store)
	first.EnableBond(size)
	if store.saves != 1 {
		t.Fatalf("first EnableBond should plot once and save; saves=%d", store.saves)
	}
	firstRoot := first.bond.Root

	// Simulate a restart: a fresh node, same identity, same store.
	second := newBondNode(t, id, store)
	second.EnableBond(size)

	if store.saves != 1 {
		t.Fatalf("restart re-plotted (saves=%d) instead of reloading — #93 regressed", store.saves)
	}
	if store.loads != 1 {
		t.Fatalf("restart did not load the persisted plot; loads=%d", store.loads)
	}
	if second.bond.Root != firstRoot {
		t.Fatal("reloaded bond advertises a different root than it plotted")
	}
	// The reloaded plot is fully functional: it answers a space-time challenge.
	ans, ok := second.bond.AnswerSpaceTime(7, vdf.Default(), 200)
	if !ok || !bond.VerifySpaceTime(second.bond.Root, size, 7, ans, vdf.Default(), 200) {
		t.Fatal("reloaded bond cannot answer a space-time challenge")
	}
}

// If the persisted plot is corrupt (its bytes no longer hash to the stored
// root), EnableBond re-plots rather than trusting it (B7).
func TestEnableBondReplotsOnCorruptPlot(t *testing.T) {
	id := ports.HashBytes([]byte("validator-B"))
	store := &countingPlotStore{}
	const size = 1 << 20

	n1 := newBondNode(t, id, store)
	n1.EnableBond(size)
	good := n1.bond.Root

	// Corrupt a stored block so it no longer matches the persisted root.
	store.blocks[0][0] ^= 0xff

	n2 := newBondNode(t, id, store)
	n2.EnableBond(size)
	if store.saves != 2 {
		t.Fatalf("a corrupt plot should trigger a re-plot (saves=%d, want 2)", store.saves)
	}
	if n2.bond.Root != good {
		t.Fatal("re-plot should reproduce the identity's correct root")
	}
}
