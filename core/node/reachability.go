package node

import "github.com/nerolabs/silt/ports"

// Reachability is a node's most recent self-assessment of whether it is
// publicly dialable, from CheckReachability. The relay decision keys off
// it: a public node advertises a direct address, a NATed one leans on a
// relay.
type Reachability int

const (
	ReachUnknown Reachability = iota // never checked
	ReachPublic                      // a helper dialed us back
	ReachNATed                       // no dial-back landed in time
)

func (r Reachability) String() string {
	switch r {
	case ReachPublic:
		return "public"
	case ReachNATed:
		return "natted"
	default:
		return "unknown"
	}
}

// Reachability reports the last verdict (ReachUnknown until a check runs).
func (n *Node) Reachability() Reachability { return n.reach }

// reachProbe is one outstanding reachability check: a nonce sent to one or
// more helpers, waiting for any of them to dial us back.
type reachProbe struct {
	cancel func()
	done   func(bool)
}

// CheckReachability asks helpers whether this node is publicly dialable —
// our AutoNAT. It sends each helper a nonce; a helper answers by dialing us
// back at the address it saw us advertise (see the MsgCheckReachability
// handler). If any dial-back lands before ReachabilityTimeout, the reply
// arrives and we are reachable; silence means no helper could open an
// inbound connection to us, i.e. we are behind NAT and should lean on a
// relay. done fires exactly once, with true (reachable) or false (NATed).
//
// The verdict is deliberately conservative: a lost dial-back reads as
// "NATed," which only costs us a relay we might not have needed — never a
// false claim of reachability that would leave us undialable.
func (n *Node) CheckReachability(helpers []ports.NodeID, done func(reachable bool)) {
	// record the verdict for Reachability() before handing it on.
	settle := func(reachable bool) {
		if reachable {
			n.reach = ReachPublic
		} else {
			n.reach = ReachNATed
		}
		n.logf(ports.LogInfo, "reachability verdict", "reach", n.reach, "helpers", len(helpers))
		done(reachable)
	}
	if len(helpers) == 0 {
		settle(false)
		return
	}
	n.reachSeq++
	nonce := n.reachSeq
	p := &reachProbe{done: settle}
	p.cancel = n.clock.AfterFunc(n.cfg.ReachabilityTimeout, func() {
		if _, ok := n.reachProbes[nonce]; !ok {
			return // already answered
		}
		delete(n.reachProbes, nonce)
		p.done(false)
	})
	n.reachProbes[nonce] = p
	for _, h := range helpers {
		n.send(h, ports.Message{Kind: ports.MsgCheckReachability, Nonce: nonce})
	}
}
