package dht

import "shardnet/ports"

// Lookup is the iterative Kademlia lookup as a pure state machine. The
// caller (core/node) owns all I/O: it asks NextQueries() who to contact,
// reports answers with OnReply/OnFailure, and reads Result() when Done.
// No network, no clock, no goroutines — a for-loop over a fake topology
// tests it exhaustively.
//
// The algorithm: keep a candidate list sorted by XOR distance to the
// target. Repeatedly query the closest not-yet-queried candidates (at
// most alpha in flight), merging every reply's "closer nodes" into the
// list. Stop when the k closest candidates have all been queried and
// nothing is in flight. Each round at least halves the distance to the
// target (see docs/math/04-kademlia.md), so this takes O(log N) rounds.
type Lookup struct {
	target   ports.Hash
	k, alpha int
	state    map[ports.NodeID]int // candidate → stateUnqueried/Inflight/Replied/Failed
	order    []ports.NodeID       // candidates sorted by distance to target
}

const (
	stateUnqueried = iota
	stateInflight
	stateReplied
	stateFailed
)

// NewLookup starts a lookup for target from an initial candidate set
// (typically Table.Closest(target, k)).
func NewLookup(target ports.Hash, k, alpha int, seeds []ports.NodeID) *Lookup {
	l := &Lookup{target: target, k: k, alpha: alpha, state: make(map[ports.NodeID]int)}
	l.add(seeds)
	return l
}

func (l *Lookup) add(ids []ports.NodeID) {
	for _, id := range ids {
		if _, seen := l.state[id]; seen {
			continue
		}
		l.state[id] = stateUnqueried
		l.order = append(l.order, id)
	}
	SortByDistance(l.target, l.order)
}

func (l *Lookup) inflight() int {
	n := 0
	for _, id := range l.order {
		if l.state[id] == stateInflight {
			n++
		}
	}
	return n
}

// NextQueries returns peers to contact now (marking them in-flight):
// the closest unqueried candidates among the current top k, capped so
// at most alpha requests are in flight at once.
func (l *Lookup) NextQueries() []ports.NodeID {
	budget := l.alpha - l.inflight()
	if budget <= 0 {
		return nil
	}
	var out []ports.NodeID
	for _, id := range l.topK() {
		if l.state[id] == stateUnqueried {
			l.state[id] = stateInflight
			out = append(out, id)
			if len(out) == budget {
				break
			}
		}
	}
	return out
}

// topK returns the k closest candidates that haven't failed.
func (l *Lookup) topK() []ports.NodeID {
	out := make([]ports.NodeID, 0, l.k)
	for _, id := range l.order {
		if l.state[id] != stateFailed {
			out = append(out, id)
			if len(out) == l.k {
				break
			}
		}
	}
	return out
}

// OnReply records a response and merges the peer's closer-node
// suggestions into the candidate set.
func (l *Lookup) OnReply(from ports.NodeID, closer []ports.NodeID) {
	if l.state[from] == stateInflight {
		l.state[from] = stateReplied
	}
	l.add(closer)
}

// OnFailure records a timeout or refusal; the peer stops counting
// toward the k closest.
func (l *Lookup) OnFailure(from ports.NodeID) {
	if l.state[from] == stateInflight {
		l.state[from] = stateFailed
	}
}

// Done reports whether the lookup has converged: nothing in flight and
// every one of the current k closest candidates has been queried.
func (l *Lookup) Done() bool {
	if l.inflight() > 0 {
		return false
	}
	for _, id := range l.topK() {
		if l.state[id] == stateUnqueried {
			return false
		}
	}
	return true
}

// Result returns the k closest peers that actually replied, best first.
func (l *Lookup) Result() []ports.NodeID {
	out := make([]ports.NodeID, 0, l.k)
	for _, id := range l.order {
		if l.state[id] == stateReplied {
			out = append(out, id)
			if len(out) == l.k {
				break
			}
		}
	}
	return out
}
