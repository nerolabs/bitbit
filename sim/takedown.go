// The takedown scenario — proving the safety model. Two files are
// scattered across the swarm. One root is then denied network-wide (as a
// quorum on-chain revocation or an operator list would do), and every
// compliant host purges and no-ops on it. The denied file becomes
// unreachable — its chunks are gone, no host serves or proves them —
// while the other file is untouched. Nothing is ever decrypted; denial
// is by opaque hash. This is what "the record persists but the content
// dies" looks like end to end.
package sim

import (
	"bytes"
	"fmt"

	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/denylist"
	"github.com/nerolabs/silt/core/erasure"
	"github.com/nerolabs/silt/core/link"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/core/pipeline"
)

type TakedownResult struct {
	Seed            int64
	Purged          int  // chunks dropped by hosts for the denied root
	DeniedBlocked   bool // the denied file could not be retrieved
	ControlSurvives bool // the other file still comes back bit-perfect
}

func (r TakedownResult) String() string {
	return fmt.Sprintf(
		"takedown: seed %d | %d chunks purged | denied file unreachable: %v | control file survives: %v",
		r.Seed, r.Purged, r.DeniedBlocked, r.ControlSurvives)
}

func Takedown(seed int64) (TakedownResult, error) {
	res := TakedownResult{Seed: seed}
	cl := NewCluster(seed, 40, simnet.DefaultConfig(), node.DefaultConfig())
	pub := cl.Nodes[0]
	z := cl.Nodes[len(cl.Nodes)-1]
	opts := pipeline.Options{ChunkSize: 8 << 10, Mode: crypto.Convergent, Erasure: erasure.DefaultParams}

	publish := func(tag byte) (link.Handle, []byte) {
		data := make([]byte, 200<<10)
		cl.rng.Read(data)
		data[0] = tag
		h, err := pipeline.Add(bgCtx, pub.Store(), cl.Registry, bytes.NewReader(data), opts)
		if err != nil {
			panic(err)
		}
		entry, _, _ := cl.Registry.Lookup(bgCtx, h.Root)
		m, _ := pipeline.LoadFull(bgCtx, pub.Store(), entry, h)
		done := false
		pub.Distribute(entry, m, false, func(int) { done = true })
		cl.Sched.Run()
		if !done {
			panic("distribute never completed")
		}
		return h, data
	}

	denied, _ := publish(1)
	control, controlData := publish(2)

	// Take the first root down everywhere: give every node a denylist
	// naming it, then let each purge what it holds.
	dl := denylist.New()
	dl.Add(denied.Root)
	for _, nd := range cl.Nodes {
		nd.SetDenylist(dl)
		res.Purged += nd.EnforceDenylist()
	}

	// The denied file must now be unreachable — chunks gone, hosts no-op.
	var out bytes.Buffer
	var gerr error
	got := false
	z.NetGet(cl.Registry, denied, &out, func(err error) { gerr = err; got = true })
	cl.Sched.Run()
	res.DeniedBlocked = got && gerr != nil

	// The control file is untouched.
	var cout bytes.Buffer
	var cerr error
	cgot := false
	z.NetGet(cl.Registry, control, &cout, func(err error) { cerr = err; cgot = true })
	cl.Sched.Run()
	res.ControlSurvives = cgot && cerr == nil && bytes.Equal(cout.Bytes(), controlData)

	return res, nil
}
