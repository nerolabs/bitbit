// The economy scenario — M5's observatory. Every node gets a starting
// grant worth exactly one publish. Everyone publishes once (the grant's
// worth). Then traffic happens: every node retrieves the content files,
// and the nodes hosting chunks earn a credit per byte served —
// freeloaders fetch like everyone else but refuse to store or serve
// anything. Finally everyone tries to publish a second file: nodes that
// served can afford it, the freeloaders discover they cannot. The
// registry has become a thing you pay for in bandwidth donated.
//
// The ledger is naive and gameable BY DESIGN (see core/credit); the
// point is watching the economics, not securing them.
package sim

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/nerolabs/bitbit/adapters/simnet"
	"github.com/nerolabs/bitbit/core/credit"
	"github.com/nerolabs/bitbit/core/crypto"
	linkpkg "github.com/nerolabs/bitbit/core/link"
	"github.com/nerolabs/bitbit/core/node"
	"github.com/nerolabs/bitbit/core/pipeline"
	"github.com/nerolabs/bitbit/core/registry"
	"github.com/nerolabs/bitbit/ports"
)

type EconomyOpts struct {
	Nodes            int
	Freeloaders      int
	ContentFiles     int
	ContentSize      int
	ChunkSize        int
	Fee              int64 // publish fee; grant equals it exactly
	FreeloaderRounds int   // extra retrieval rounds for freeloaders
	Net              simnet.Config
	NodeCfg          node.Config
	Report           func(string)
}

func DefaultEconomyOpts() EconomyOpts {
	return EconomyOpts{
		Nodes:            36,
		Freeloaders:      6,
		ContentFiles:     3,
		ContentSize:      64 << 10,
		ChunkSize:        4 << 10,
		Fee:              50_000,
		FreeloaderRounds: 1,
		Net:              simnet.DefaultConfig(),
		NodeCfg:          node.DefaultConfig(),
	}
}

type EconomyResult struct {
	Seed                int64
	Gini                float64
	SeedPublishOK       int // first publishes (paid by grant)
	SecondOK            int // second publishes that went through
	SecondRejected      int
	FreeloadersRejected int // of the freeloaders' second attempts
	TopBalance          int64
	Timeline            []string
	Net                 simnet.Stats
}

func (r EconomyResult) String() string {
	return fmt.Sprintf(
		"economy: seed %d | Gini %.2f | first publishes %d ok | second: %d ok, %d rejected (%d of them freeloaders)\n  top balance %d credits | net: %d sent, %d delivered",
		r.Seed, r.Gini, r.SeedPublishOK, r.SecondOK, r.SecondRejected, r.FreeloadersRejected,
		r.TopBalance, r.Net.Sent, r.Net.Delivered)
}

func Economy(seed int64, o EconomyOpts) (EconomyResult, error) {
	res := EconomyResult{Seed: seed}
	cl := NewCluster(seed, o.Nodes, o.Net, o.NodeCfg)
	ledger := credit.New(o.Fee, o.Fee) // grant = exactly one publish
	reg := registry.NewGated(ledger)

	say := func(line string) {
		res.Timeline = append(res.Timeline, line)
		if o.Report != nil {
			o.Report(line)
		}
	}

	freeloader := make(map[ports.NodeID]bool)
	for i, nd := range cl.Nodes {
		nd.SetLedger(ledger)
		ledger.Register(nd.ID())
		// The LAST o.Freeloaders nodes leech; content publishers come
		// from the front, so the sets never overlap.
		if i >= o.Nodes-o.Freeloaders {
			nd.SetFreeload(true)
			freeloader[nd.ID()] = true
		}
	}
	say(fmt.Sprintf("phase grants   | %d nodes (%d freeloaders) | everyone granted %d credits (= 1 publish fee)",
		o.Nodes, o.Freeloaders, o.Fee))

	// Phase 1: everyone publishes once on the grant. The first
	// o.ContentFiles nodes publish real, distributed content; the rest
	// publish small local-only files.
	var contentHandles []linkpkg.Handle
	for i, nd := range cl.Nodes {
		var data []byte
		if i < o.ContentFiles {
			data = make([]byte, o.ContentSize)
			cl.rng.Read(data)
		} else {
			data = []byte(fmt.Sprintf("node %s says hello", nd.ID()))
		}
		h, err := pipeline.Add(bgCtx, nd.Store(), reg, bytes.NewReader(data),
			pipeline.Options{ChunkSize: o.ChunkSize, Mode: crypto.Convergent, Publisher: nd.ID()})
		if err != nil {
			return res, fmt.Errorf("economy(seed=%d): seed publish by node %d: %w", seed, i, err)
		}
		res.SeedPublishOK++
		if i < o.ContentFiles {
			contentHandles = append(contentHandles, h)
			entry, _, _ := reg.Lookup(bgCtx, h.Root)
			m, err := pipeline.LoadFull(bgCtx, nd.Store(), entry, h)
			if err != nil {
				return res, err
			}
			nd.Distribute(entry, m, false, func(int) {})
			cl.Sched.Run()
		}
	}
	say(fmt.Sprintf("phase seed-pub | %d/%d publishes ok | grants spent, all balances at 0 | Gini %.2f",
		res.SeedPublishOK, o.Nodes, ledger.Gini()))

	// Phase 2: traffic. Every node retrieves every content file (except
	// its own publisher); freeloaders do extra rounds. Retrieved chunks
	// are discarded afterward — consumers consume, they don't become
	// mirrors — so repeat fetches keep paying the hosts.
	gets := 0
	fetchRound := func(nd *node.Node) error {
		for fi, ch := range contentHandles {
			if cl.Nodes[fi].ID() == nd.ID() {
				continue
			}
			before := make(map[ports.ChunkID]bool)
			ids, _ := nd.Store().List(bgCtx)
			for _, id := range ids {
				before[id] = true
			}
			var out bytes.Buffer
			var gerr error
			done := false
			nd.NetGet(reg, ch, &out, func(err error) { gerr = err; done = true })
			cl.Sched.Run()
			if !done || gerr != nil {
				return fmt.Errorf("get of file %d: done=%v err=%v", fi, done, gerr)
			}
			gets++
			after, _ := nd.Store().List(bgCtx)
			for _, id := range after {
				if !before[id] {
					nd.Store().Delete(bgCtx, id)
				}
			}
		}
		return nil
	}
	for _, nd := range cl.Nodes {
		if err := fetchRound(nd); err != nil {
			return res, fmt.Errorf("economy(seed=%d): %w", seed, err)
		}
	}
	for r := 0; r < o.FreeloaderRounds; r++ {
		for _, nd := range cl.Nodes {
			if freeloader[nd.ID()] {
				if err := fetchRound(nd); err != nil {
					return res, fmt.Errorf("economy(seed=%d): freeloader round: %w", seed, err)
				}
			}
		}
	}
	res.Gini = ledger.Gini()
	for _, b := range ledger.Balances() {
		if b > res.TopBalance {
			res.TopBalance = b
		}
	}
	say(fmt.Sprintf("phase traffic  | %d retrievals | credits now flow to hosts | Gini %.2f | top balance %d",
		gets, res.Gini, res.TopBalance))

	// Phase 3: the gate. Everyone attempts a second publish.
	for i, nd := range cl.Nodes {
		data := []byte(fmt.Sprintf("second file from %s", nd.ID()))
		_, err := pipeline.Add(bgCtx, nd.Store(), reg, bytes.NewReader(data),
			pipeline.Options{ChunkSize: o.ChunkSize, Mode: crypto.Convergent, Publisher: nd.ID()})
		switch {
		case err == nil:
			res.SecondOK++
		case errors.Is(err, ports.ErrInsufficientCredit):
			res.SecondRejected++
			if freeloader[nd.ID()] {
				res.FreeloadersRejected++
			}
		default:
			return res, fmt.Errorf("economy(seed=%d): second publish by node %d: %w", seed, i, err)
		}
	}
	say(fmt.Sprintf("phase gate     | second publishes: %d ok, %d rejected | all %d freeloaders among the rejected: %v",
		res.SecondOK, res.SecondRejected, o.Freeloaders, res.FreeloadersRejected == o.Freeloaders))

	// The observatory's close-up: extremes of the wealth distribution.
	type row struct {
		nd  *node.Node
		bal int64
	}
	var top, bottomFree row
	for _, nd := range cl.Nodes {
		b := ledger.Balance(nd.ID())
		if top.nd == nil || b > top.bal {
			top = row{nd, b}
		}
		if freeloader[nd.ID()] && (bottomFree.nd == nil || b < bottomFree.bal) {
			bottomFree = row{nd, b}
		}
	}
	say(fmt.Sprintf("  top earner   %s… served %7d B, fetched %7d B → %7d credits, can publish: %v",
		top.nd.ID().String()[:8], ledger.ServedBytes(top.nd.ID()), ledger.FetchedBytes(top.nd.ID()),
		top.bal, ledger.CanPublish(top.nd.ID())))
	if bottomFree.nd != nil {
		say(fmt.Sprintf("  freeloader    %s… served %7d B, fetched %7d B → %7d credits, can publish: %v",
			bottomFree.nd.ID().String()[:8], ledger.ServedBytes(bottomFree.nd.ID()),
			ledger.FetchedBytes(bottomFree.nd.ID()), bottomFree.bal, ledger.CanPublish(bottomFree.nd.ID())))
	}
	res.Net = cl.Net.Stats
	return res, nil
}
