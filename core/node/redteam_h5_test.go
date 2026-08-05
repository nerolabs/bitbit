package node

import (
	"crypto/ed25519"
	"testing"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/ports"
)

// M0 hardening H5 (Memo 08): DHT provider records must be SELF-CERTIFYING, so a
// node holding the k-closest slots to a key cannot fabricate provider records for
// identities that never announced ("provider records cannot be silently forged").
// These test the node-side accept/reject logic directly.

func h5node(t *testing.T, seed int64, strict bool) (*Node, *identity.Identity) {
	t.Helper()
	id := identity.FromSeed(seed)
	cfg := DefaultConfig()
	cfg.RequireSignedProviders = strict
	sched := simclock.New()
	net := simnet.New(sched, seed, simnet.DefaultConfig())
	n := New(id.NodeID(), cfg, sched, net.Endpoint(id.NodeID()), memstore.New())
	n.SetSigner(id.Signer())
	return n, id
}

// TestH5_SignedRecordVerifiesAndBindsToIdentity: a node's own record is signed,
// bound to its NodeID, and verifies; flipping any field breaks it.
func TestH5_SignedRecordVerifiesAndBindsToIdentity(t *testing.T) {
	n, id := h5node(t, 1, true)
	key := ports.HashBytes([]byte("content-key"))
	rec := n.providerRecord(key)

	if !rec.Signed() || !rec.Verify(0) {
		t.Fatal("a node's own provider record must be signed and verify")
	}
	if rec.ID != id.NodeID() {
		t.Fatal("the record must be bound to the node's own NodeID")
	}
	// Tamper: a different key, a swapped ID, or a mangled signature all fail.
	bad := rec
	bad.Key = ports.HashBytes([]byte("other-key"))
	if bad.Verify(0) {
		t.Fatal("a record re-pointed at another key must not verify")
	}
	bad = rec
	bad.ID = ports.HashBytes([]byte("someone else"))
	if bad.Verify(0) {
		t.Fatal("a record claiming another identity must not verify (pubkey no longer hashes to ID)")
	}
	bad = rec
	bad.Sig = append([]byte(nil), rec.Sig...)
	bad.Sig[0] ^= 0xff
	if bad.Verify(0) {
		t.Fatal("a record with a mangled signature must not verify")
	}
}

// TestH5_AcceptAnnounceRejectsForgeryAndThirdPartyClaims: the store path accepts a
// genuine self-announce but rejects a record that names a DIFFERENT provider than
// the authenticated sender (no registering third parties) or fails to verify.
func TestH5_AcceptAnnounceRejectsForgeryAndThirdPartyClaims(t *testing.T) {
	victimN, victim := h5node(t, 2, true)
	attackerN, attacker := h5node(t, 3, true)
	_ = attackerN
	key := ports.HashBytes([]byte("k"))

	// A genuine self-announce from the victim is accepted by a strict storer.
	storer, _ := h5node(t, 4, true)
	good := victimN.providerRecord(key)
	if _, ok := storer.acceptAnnounce(key, victim.NodeID(), &good); !ok {
		t.Fatal("a valid self-signed announce must be accepted")
	}

	// The attacker tries to register the VICTIM as a provider (a forged record it
	// cannot sign for): even though it's a real signed record of the victim, it
	// arrives from the attacker, so rec.ID != from and it is rejected.
	if _, ok := storer.acceptAnnounce(key, attacker.NodeID(), &good); ok {
		t.Fatal("a record announcing a THIRD party (rec.ID != authenticated sender) must be rejected")
	}

	// The attacker forges a record CLAIMING to be the victim but signs with its own
	// key — the pubkey no longer hashes to the claimed ID, so verify fails.
	forged := ports.ProviderRecord{Key: key, ID: victim.NodeID()}
	forged.PubKey = []byte(attacker.Signer().Public().(ed25519.PublicKey))
	forged.Sig = ed25519.Sign(attacker.Signer(), ports.ProviderSigningBytes(key, victim.NodeID(), 0))
	if _, ok := storer.acceptAnnounce(key, victim.NodeID(), &forged); ok {
		t.Fatal("a record whose pubkey doesn't hash to its claimed ID must be rejected")
	}

	// In strict mode a nil (legacy unsigned) announce is refused.
	if _, ok := storer.acceptAnnounce(key, victim.NodeID(), nil); ok {
		t.Fatal("a strict node must refuse an unsigned legacy announce")
	}
}

// TestH5_FetcherDropsInjectedForgedRecords is the inverted PoC: a lookup response
// (which an eclipsing responder controls) carrying a forged record is dropped by a
// strict fetcher, while the genuine signed record survives.
func TestH5_FetcherDropsInjectedForgedRecords(t *testing.T) {
	fetcher, _ := h5node(t, 5, true)
	honest, _ := h5node(t, 6, true)
	attacker, _ := h5node(t, 7, true)
	key := ports.HashBytes([]byte("chunk"))

	genuine := honest.providerRecord(key)                     // real, signed
	forged := ports.ProviderRecord{Key: key, ID: attacker.id} // unsigned fabrication an eclipser injects
	// Also a signed-but-for-another-key replay (wrong key → verify fails).
	replay := attacker.providerRecord(ports.HashBytes([]byte("different")))

	served := []ports.ProviderRecord{genuine, forged, replay}
	got := fetcher.acceptedProviderIDs(key, served)

	if len(got) != 1 || got[0] != honest.id {
		t.Fatalf("a strict fetcher must keep ONLY the genuine record, got %v", got)
	}
}

// TestH5_NonStrictAcceptsLegacyRecords: with signing off, unsigned records still
// flow (backward compatibility), but a forged SIGNED record is still dropped.
func TestH5_NonStrictAcceptsLegacyRecords(t *testing.T) {
	fetcher, _ := h5node(t, 8, false) // not strict
	honest, _ := h5node(t, 9, false)
	key := ports.HashBytes([]byte("k"))

	unsigned := ports.ProviderRecord{Key: key, ID: honest.id} // legacy, no sig
	badSigned := honest.providerRecord(key)
	badSigned.Sig[0] ^= 0xff // a signed record that fails to verify is dropped even in non-strict mode

	got := fetcher.acceptedProviderIDs(key, []ports.ProviderRecord{unsigned, badSigned})
	if len(got) != 1 || got[0] != honest.id {
		t.Fatalf("non-strict must keep the unsigned legacy record and drop the broken-signature one, got %v", got)
	}
}
