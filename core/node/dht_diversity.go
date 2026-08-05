// Failure-domain diversity for eclipse resistance (M0 H5-B / Memo 08). The
// forgery half (signed records, H5-A) stops a node fabricating provider records;
// this stops the SUPPRESSION half — an adversary owning the NodeIDs closest to a
// key but sitting in a single failure domain (a cheap /24 key-surround). Provider
// records are announced to, and resolved from, a set of near peers SPREAD across
// domains, so an honest node in a domain the adversary doesn't control both holds
// the record and answers for it. Paired with the routing table's per-bucket domain
// cap (dht.Table.SetDiversity), which keeps those honest peers routable.
package node

import "github.com/nerolabs/silt/ports"

// diverseNear returns up to count peers near key from the routing table, admitting
// at most DHTDomainCap peers per failure domain — so a single-domain cluster near
// the key can't monopolise the set. With the cap off (≤ 0) it is just the count
// nearest peers. Domain 0 (unknown) is never capped (assumed independent).
func (n *Node) diverseNear(key ports.Hash, count int) []ports.NodeID {
	all := n.table.Closest(key, n.table.Size()) // everything, distance-sorted
	cap := n.cfg.DHTDomainCap
	if cap <= 0 {
		if len(all) > count {
			all = all[:count]
		}
		return all
	}
	perDomain := make(map[uint64]int)
	out := make([]ports.NodeID, 0, count)
	for _, id := range all {
		if d := n.domainOf(id); d != 0 {
			if perDomain[d] >= cap {
				continue
			}
			perDomain[d]++
		}
		out = append(out, id)
		if len(out) == count {
			break
		}
	}
	return out
}

// sweepProviders asks each target directly for the providers of key and feeds
// every reply's records to onRecs, then calls done. It is how a fetcher reaches
// honest cross-domain record-holders that a pure distance walk — which converges
// onto the adversary's surrounding NodeIDs — would never query (H5-B).
func (n *Node) sweepProviders(key ports.Hash, targets []ports.NodeID, onRecs func([]ports.ProviderRecord), done func()) {
	var next func(i int)
	next = func(i int) {
		if i == len(targets) {
			done()
			return
		}
		t := targets[i]
		if t == n.id {
			next(i + 1)
			return
		}
		n.request(t, ports.Message{Kind: ports.MsgGetProviders, Target: key},
			func(resp ports.Message, err error) {
				if err == nil && len(resp.ProviderRecs) > 0 {
					onRecs(resp.ProviderRecs)
				}
				next(i + 1)
			})
	}
	next(0)
}
