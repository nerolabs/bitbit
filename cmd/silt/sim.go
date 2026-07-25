package main

import (
	"flag"
	"fmt"

	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/ports"
	"github.com/nerolabs/silt/sim"
)

func cmdSim(args []string) error {
	if len(args) < 1 || args[0] != "run" {
		return fmt.Errorf("usage: silt sim run <scenario> [flags]  (scenarios: scatter)")
	}
	args = args[1:]
	if len(args) < 1 {
		return fmt.Errorf("usage: silt sim run <scenario> [flags]  (scenarios: scatter)")
	}
	scenario := args[0]
	fs := flag.NewFlagSet("sim run "+scenario, flag.ExitOnError)
	seed := fs.Int64("seed", 1, "sim seed — same seed, same run, byte for byte")
	nodes := fs.Int("nodes", 0, "number of nodes (0 = scenario default)")
	size := fs.Int("size", 0, "file size in bytes (0 = scenario default)")
	chunkSize := fs.Int("chunk-size", 0, "chunk size in bytes (0 = scenario default)")
	loss := fs.Float64("loss", 0.0, "per-message drop probability (0..1)")
	kill := fs.Int("kill", 0, "scatter: nodes to kill between add and get")
	killFrac := fs.Float64("kill-frac", 0.15, "churn: fraction of nodes killed per wave")
	waves := fs.Int("waves", 2, "churn: number of kill waves")
	caretakers := fs.Int("caretakers", 3, "churn: nodes running the repair loop")
	freeloaders := fs.Int("freeloaders", 6, "economy: nodes that fetch but never store or serve")
	fee := fs.Int64("fee", 50_000, "economy: publish fee in credits (1 credit = 1 byte served)")
	liars := fs.Int("liars", 6, "audit: nodes that keep proofs but throw away chunk data")
	latMin := fs.Int("lat-min", 5, "min link latency, ms")
	latMax := fs.Int("lat-max", 50, "max link latency, ms")
	fs.Parse(args[1:])

	netCfg := simnet.Config{
		LatencyMin: ports.Duration(*latMin) * ports.Millisecond,
		LatencyMax: ports.Duration(*latMax) * ports.Millisecond,
		Loss:       *loss,
	}

	switch scenario {
	case "scatter":
		o := sim.DefaultScatterOpts()
		o.Net = netCfg
		o.Kill = *kill
		if *nodes > 0 {
			o.Nodes = *nodes
		}
		if *size > 0 {
			o.FileSize = *size
		}
		if *chunkSize > 0 {
			o.ChunkSize = *chunkSize
		}
		fmt.Printf("scatter: %d nodes, %d-byte file, loss %.1f%%, kill %d, seed %d\n",
			o.Nodes, o.FileSize, *loss*100, o.Kill, *seed)
		res, err := sim.Scatter(*seed, o)
		if err != nil {
			fmt.Println(res) // partial stats still worth seeing
			return err
		}
		fmt.Println(res)
		if !res.Match {
			return fmt.Errorf("retrieved bytes differ (seed %d reproduces this)", *seed)
		}
		return nil

	case "churn":
		o := sim.DefaultChurnOpts()
		o.Net = netCfg
		o.KillFrac = *killFrac
		o.Waves = *waves
		o.Caretakers = *caretakers
		if *nodes > 0 {
			o.Nodes = *nodes
		}
		if *size > 0 {
			o.FileSize = *size
		}
		if *chunkSize > 0 {
			o.ChunkSize = *chunkSize
		}
		o.Report = func(line string) { fmt.Println(line) }
		fmt.Printf("churn: %d nodes, %d caretakers, %d wave(s) × %.0f%% killed, loss %.1f%%, seed %d\n\n",
			o.Nodes, o.Caretakers, o.Waves, o.KillFrac*100, *loss*100, *seed)
		res, err := sim.Churn(*seed, o)
		fmt.Println()
		fmt.Println(res)
		if err != nil {
			return err
		}
		if !res.Match {
			return fmt.Errorf("file did not survive (seed %d reproduces this)", *seed)
		}
		return nil

	case "economy":
		o := sim.DefaultEconomyOpts()
		o.Net = netCfg
		o.Freeloaders = *freeloaders
		o.Fee = *fee
		if *nodes > 0 {
			o.Nodes = *nodes
		}
		if *chunkSize > 0 {
			o.ChunkSize = *chunkSize
		}
		o.Report = func(line string) { fmt.Println(line) }
		fmt.Printf("economy: %d nodes, %d freeloaders, fee %d credits, seed %d\n\n",
			o.Nodes, o.Freeloaders, o.Fee, *seed)
		res, err := sim.Economy(*seed, o)
		fmt.Println()
		fmt.Println(res)
		return err

	case "capacity":
		o := sim.DefaultCapacityOpts()
		o.Net = netCfg
		if *nodes > 0 {
			o.Nodes = *nodes
		}
		if *chunkSize > 0 {
			o.ChunkSize = *chunkSize
		}
		o.Report = func(line string) { fmt.Println(line) }
		fmt.Printf("capacity: %d nodes, %d bytes pledged each, seed %d\n\n", o.Nodes, o.NodePledge, *seed)
		res, err := sim.Capacity(*seed, o)
		fmt.Println()
		fmt.Println(res)
		return err

	case "consensus":
		o := sim.DefaultConsensusOpts()
		o.Net = netCfg
		if *nodes > 0 {
			o.Nodes = *nodes
		}
		o.Report = func(line string) { fmt.Println(line) }
		fmt.Printf("consensus: %d nodes, %d established validators, quorum %d, seed %d\n\n",
			o.Nodes, o.Established, o.Chain.Quorum, *seed)
		res, err := sim.Consensus(*seed, o)
		fmt.Println()
		fmt.Println(res)
		return err

	case "audit":
		o := sim.DefaultAuditOpts()
		o.Net = netCfg
		o.Liars = *liars
		if *nodes > 0 {
			o.Nodes = *nodes
		}
		if *size > 0 {
			o.FileSize = *size
		}
		if *chunkSize > 0 {
			o.ChunkSize = *chunkSize
		}
		o.Report = func(line string) { fmt.Println(line) }
		fmt.Printf("audit: %d nodes, %d liars, seed %d\n\n", o.Nodes, o.Liars, *seed)
		res, err := sim.Audit(*seed, o)
		fmt.Println()
		fmt.Println(res)
		if err != nil {
			return err
		}
		if res.LiarsCaught < o.Liars {
			return fmt.Errorf("only %d of %d liars caught (seed %d reproduces this)", res.LiarsCaught, o.Liars, *seed)
		}
		return nil

	default:
		return fmt.Errorf("unknown scenario %q (scenarios: scatter, churn, economy, audit, capacity, consensus)", scenario)
	}
}
