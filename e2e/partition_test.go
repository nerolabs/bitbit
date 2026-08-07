package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/nerolabs/silt/adapters/identity"
)

// TestPartitionHealsToHeavierForkOverTCP is the #184 partition→heal case over the REAL
// WIRE: the network splits, each side commits its OWN history (a fork), then the
// partition heals and the LIGHTER side reorgs onto the HEAVIER fork — consensus
// reconverges on one history, over real TCP. This exercises the M0 consensus denial in
// OBJECTIVE mode (the default), where fork-choice weight is a function of the on-chain
// bond registrations carried IN the fork itself — so every replica agrees which fork is
// heavier without any external reputation view. Proven against real daemons, not only
// the in-process sim (sim/reorg_test.go).
//
// Topology (objective, quorum 1; every node is a launch anchor so a 2-node partition
// can bootstrap and commit):
//   - Group 1 = {A, B}: A serves a registry; two publishes commit a TWO-block fork
//     [g, a1, a2] whose cumulative bonded weight is heavier.
//   - Group 2 = {C, D}: C serves a registry; one publish commits a ONE-block fork
//     [g, c1]. C and D carry -block-peers A,B, so even pointed at group 1 they stay
//     partitioned and form their own fork.
//   - Heal: restart C WITHOUT -block-peers, bootstrapped to A. It reloads its persisted
//     [g, c1], syncs the heavier fork, and REORGS — dropping c1, adopting a1,a2. It
//     prints the reorg, which we assert. Group 1 (unblocked) never learned group 2, so
//     it does not budge.
func TestPartitionHealsToHeavierForkOverTCP(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e spawns processes; skipped under -short")
	}
	idA := identity.FromSeed(7001).NodeID().String()
	idB := identity.FromSeed(7002).NodeID().String()
	idC := identity.FromSeed(7003).NodeID().String()
	idD := identity.FromSeed(7004).NodeID().String()
	anchors := idA + "," + idB + "," + idC + "," + idD // shared launch set → identical genesis config

	storeC := t.TempDir() // C's persistent store — reused across its heal-restart

	// Objective consensus with every node an anchor (so a 2-node partition can commit);
	// no anchor-quorum requirement, so each side commits its own fork. Fast bond audit.
	val := func(extra ...string) []string {
		return append([]string{
			"-listen", "127.0.0.1:0", "-validator",
			"-min-bond", "1M", "-mature-validators", "99", "-anchors", anchors, "-quorum", "1",
			"-min-bond-floor", "0", "-bond", "8M", "-bond-audit", "1s",
			"-capacity", "1G", "-mdns=false",
		}, extra...)
	}

	// Group 1 (heavier): A registry + validator, B attests. NOT blocking anyone — so a
	// healed C can reconnect to A.
	a := startDaemon(t, "A", val("-store", t.TempDir(), "-serve-registry", "127.0.0.1:0",
		"-attesters", idB, "-id-seed", "7001")...)
	pa := a.waitFor(t, rePeer, 20*time.Second)
	bootA := pa[1] + "@" + pa[2]
	regA := a.waitFor(t, reRegistry, 20*time.Second)[1]
	b := startDaemon(t, "B", val("-store", t.TempDir(), "-bootstrap", bootA,
		"-attesters", idA, "-id-seed", "7002")...)
	b.waitFor(t, reBootstrap, 20*time.Second)

	// Group 2 (lighter): C registry + validator, D attests. Both PARTITIONED from A,B —
	// they drop all group-1 traffic, so they form their own fork.
	c := startDaemon(t, "C", val("-store", storeC, "-serve-registry", "127.0.0.1:0",
		"-attesters", idD, "-block-peers", idA+","+idB, "-id-seed", "7003")...)
	pc := c.waitFor(t, rePeer, 20*time.Second)
	bootC := pc[1] + "@" + pc[2]
	regC := c.waitFor(t, reRegistry, 20*time.Second)[1]
	d := startDaemon(t, "D", val("-store", t.TempDir(), "-bootstrap", bootC,
		"-attesters", idC, "-block-peers", idA+","+idB, "-id-seed", "7004")...)
	d.waitFor(t, reBootstrap, 20*time.Second)

	// During the partition, group 1 commits TWO blocks (heavier), group 2 commits ONE.
	reAnyLink := regexp.MustCompile(`silt:v1:\S+`)
	publish := func(who *daemon, name, boot, reg string) {
		src := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(src, []byte("partition-payload-"+name), 0o644); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(60 * time.Second)
		for {
			out, err := runClientAllowErr(t, "swarm", "add", src,
				"-peers", boot, "-registry", reg, "-chunk-size", "65536", "-mode", "convergent")
			if err == nil && reAnyLink.FindString(out) != "" {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: publish %q never committed:\n%s", who.name, name, out)
			}
			time.Sleep(1 * time.Second)
		}
	}
	publish(a, "a1", bootA, regA)
	publish(a, "a2", bootA, regA)
	a.waitFor(t, regexp.MustCompile(`chain: committed block 2`), 20*time.Second) // [g,a1,a2]
	t.Logf("group 1 committed its 2-block fork [g,a1,a2]")
	publish(c, "c1", bootC, regC)
	c.waitFor(t, regexp.MustCompile(`chain: committed block 1`), 20*time.Second) // [g,c1]
	t.Logf("group 2 committed its 1-block fork [g,c1] (partitioned from group 1)")

	// HEAL group 2: stop C and restart it WITHOUT the partition, bootstrapped to A and
	// syncing from it. It reloads its persisted [g, c1], sees group 1's heavier fork,
	// and reorgs onto it.
	c.cmd.Process.Kill()
	c.cmd.Wait()
	cHealed := startDaemon(t, "C(healed)", val("-store", storeC, "-bootstrap", bootA,
		"-attesters", idA, "-id-seed", "7003")...)
	cHealed.waitFor(t, reBootstrap, 20*time.Second)
	t.Logf("C restarted without the partition, bootstrapped to A — healing")

	// The lighter side reorgs onto the heavier fork — one block dropped (c1), new head at
	// height 2 (a1,a2). Consensus reconverged over the wire.
	cHealed.waitFor(t, regexp.MustCompile(`chain: reorged onto a heavier fork \(dropped 1 block\(s\), new head height 2\)`), 60*time.Second)
	t.Logf("C reorged onto group 1's heavier fork — consensus reconverged over TCP")
	_ = d
}
