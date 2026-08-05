// Self-certifying provider records (M0 H5 / Memo 08). A provider record is a
// signed "I hold content under key K" claim bound to the provider's identity, so
// a node that holds the k-closest slots to K cannot fabricate records for
// identities that never announced, and a fetcher a record is re-served to can
// verify it rather than trust the responder. Off by default (unsigned records
// flow, as before); RequireSignedProviders makes a node produce, store, and
// accept only signed records — the untrusted-swarm posture.
package node

import (
	"crypto/ed25519"

	"github.com/nerolabs/silt/ports"
)

// SetSigner gives the node its identity signing key for uses that are not the
// validator role — currently self-certifying provider records (H5). It is the
// SAME key whose hash is the NodeID, so a record it signs verifies against this
// node's ID. EnableChain sets the same field for validators; calling both is
// fine. No-op behavior when never called: records are left unsigned (legacy).
func (n *Node) SetSigner(priv ed25519.PrivateKey) { n.signer = priv }

func (n *Node) requireSignedProviders() bool { return n.cfg.RequireSignedProviders }

// providerRecord builds this node's provider record for key: signed and bound to
// its identity when a signer is set, otherwise an unsigned legacy record carrying
// just the NodeID. Expiry is stamped only when ProviderRecordTTL > 0 (a real
// clock); the sim/tests leave it 0 for determinism, and the signature still binds
// identity+key, which is what closes the forgery vector.
func (n *Node) providerRecord(key ports.Hash) ports.ProviderRecord {
	rec := ports.ProviderRecord{Key: key, ID: n.id}
	if n.signer != nil {
		rec.PubKey = append([]byte(nil), n.signer.Public().(ed25519.PublicKey)...)
		if n.cfg.ProviderRecordTTL > 0 {
			rec.Expiry = int64(n.clock.Now().Add(n.cfg.ProviderRecordTTL))
		}
		rec.Sig = ed25519.Sign(n.signer, ports.ProviderSigningBytes(key, n.id, rec.Expiry))
	}
	return rec
}

// acceptAnnounce validates a provider record arriving on MsgAddProvider from the
// authenticated sender `from` for `target`, returning the record to store. A
// record must announce the SENDER ITSELF under the queried key (no registering
// third parties), and — in strict mode — must be signed and verify. A nil record
// is the legacy self-vouch: accepted (as an unsigned record) only when not strict.
func (n *Node) acceptAnnounce(target ports.Hash, from ports.NodeID, rec *ports.ProviderRecord) (ports.ProviderRecord, bool) {
	if rec == nil {
		if n.requireSignedProviders() {
			return ports.ProviderRecord{}, false
		}
		return ports.ProviderRecord{Key: target, ID: from}, true
	}
	if rec.Key != target || rec.ID != from {
		return ports.ProviderRecord{}, false // must be a self-announce for this key
	}
	if rec.Signed() {
		if !rec.Verify(int64(n.clock.Now())) {
			return ports.ProviderRecord{}, false
		}
	} else if n.requireSignedProviders() {
		return ports.ProviderRecord{}, false
	}
	return *rec, true
}

// acceptedProviderIDs filters records served back for `key` down to the provider
// IDs this node will trust: every signed record that is FOR THIS KEY and verifies
// now, plus (only when not strict) unsigned legacy records for this key. A forged
// record, an expired one, or one validly signed for a DIFFERENT key replayed here
// — the kinds an eclipsing responder would inject — are all silently dropped.
func (n *Node) acceptedProviderIDs(key ports.Hash, recs []ports.ProviderRecord) []ports.NodeID {
	now := int64(n.clock.Now())
	out := make([]ports.NodeID, 0, len(recs))
	for _, r := range recs {
		if r.Key != key {
			continue // a record for another key cannot vouch for this one
		}
		switch {
		case r.Signed():
			if r.Verify(now) {
				out = append(out, r.ID)
			}
		case !n.requireSignedProviders():
			out = append(out, r.ID)
		}
	}
	return out
}
