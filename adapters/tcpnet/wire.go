package tcpnet

import (
	"github.com/fxamacker/cbor/v2"

	"github.com/nerolabs/silt/ports"
)

// envelope is the on-the-wire frame: who sent it, where to reach them,
// any addresses they can vouch for, the relay service they offer (if
// any), and the message itself.
type envelope struct {
	From     []byte            `cbor:"1,keyasint"`
	Addr     string            `cbor:"2,keyasint"`
	Contacts map[string]string `cbor:"3,keyasint,omitempty"`
	Msg      wireMsg           `cbor:"4,keyasint"`
	// Relay is the sender's own -relay service address (host:port),
	// present only while it offers one — relay discovery is first-hand
	// gossip, a node never vouches for another's relay.
	Relay string `cbor:"5,keyasint,omitempty"`
}

// wireMsg mirrors ports.Message with CBOR-friendly []byte fields. An
// adapter mirroring the port type by hand is the honest cost of keeping
// serialization concerns out of ports; a production transport would
// version this.
type wireMsg struct {
	Kind      uint8      `cbor:"1,keyasint"`
	RID       uint64     `cbor:"2,keyasint"`
	Target    []byte     `cbor:"3,keyasint,omitempty"`
	Nodes     [][]byte   `cbor:"4,keyasint,omitempty"`
	Providers [][]byte   `cbor:"5,keyasint,omitempty"`
	ChunkID   []byte     `cbor:"6,keyasint,omitempty"`
	Data      []byte     `cbor:"7,keyasint,omitempty"`
	Found     bool       `cbor:"8,keyasint,omitempty"`
	OK        bool       `cbor:"9,keyasint,omitempty"`
	Nonce     uint64     `cbor:"10,keyasint,omitempty"`
	Tag       []byte     `cbor:"11,keyasint,omitempty"`
	Proof     *wireProof `cbor:"12,keyasint,omitempty"`
	CapUsed   int64      `cbor:"13,keyasint,omitempty"`
	CapTotal  int64      `cbor:"14,keyasint,omitempty"`
	Height    uint64     `cbor:"15,keyasint,omitempty"`
	Domain    uint64     `cbor:"16,keyasint,omitempty"`
	Lease     bool       `cbor:"17,keyasint,omitempty"`
	Ephemeral bool       `cbor:"18,keyasint,omitempty"`
	BondRoot  []byte     `cbor:"19,keyasint,omitempty"`
	BondSize  int64      `cbor:"20,keyasint,omitempty"`
}

type wireProof struct {
	Root   []byte   `cbor:"1,keyasint"`
	Index  int      `cbor:"2,keyasint"`
	Total  int      `cbor:"3,keyasint"`
	Path   [][]byte `cbor:"4,keyasint,omitempty"`
	Column int      `cbor:"5,keyasint,omitempty"`
}

var encMode cbor.EncMode

func init() {
	var err error
	encMode, err = cbor.CanonicalEncOptions().EncMode()
	if err != nil {
		panic(err)
	}
}

func toWire(m ports.Message) wireMsg {
	w := wireMsg{
		Kind:  uint8(m.Kind),
		RID:   m.RID,
		Found: m.Found,
		OK:    m.OK,
	}
	if m.Target != (ports.Hash{}) {
		w.Target = append([]byte(nil), m.Target[:]...)
	}
	if m.ChunkID != (ports.ChunkID{}) {
		w.ChunkID = append([]byte(nil), m.ChunkID[:]...)
	}
	w.Nodes = idsToBytes(m.Nodes)
	w.Providers = idsToBytes(m.Providers)
	if len(m.Data) > 0 {
		w.Data = append([]byte(nil), m.Data...)
	}
	w.Nonce = m.Nonce
	w.CapUsed, w.CapTotal = m.CapUsed, m.CapTotal
	w.Height = m.Height
	w.Domain = m.Domain
	w.Lease = m.Lease
	w.Ephemeral = m.Ephemeral
	w.BondSize = m.BondSize
	if m.BondRoot != (ports.Hash{}) {
		w.BondRoot = append([]byte(nil), m.BondRoot[:]...)
	}
	if len(m.Tag) > 0 {
		w.Tag = append([]byte(nil), m.Tag...)
	}
	if m.Proof != nil {
		w.Proof = &wireProof{
			Root:   append([]byte(nil), m.Proof.Root[:]...),
			Index:  m.Proof.Index,
			Total:  m.Proof.Total,
			Path:   idsToBytes(m.Proof.Path),
			Column: m.Proof.Column,
		}
	}
	return w
}

func fromWire(w wireMsg) ports.Message {
	m := ports.Message{
		Kind:  ports.MsgKind(w.Kind),
		RID:   w.RID,
		Found: w.Found,
		OK:    w.OK,
		Data:  w.Data,
	}
	copy(m.Target[:], w.Target)
	copy(m.ChunkID[:], w.ChunkID)
	m.Nodes = bytesToIDs(w.Nodes)
	m.Providers = bytesToIDs(w.Providers)
	m.Nonce = w.Nonce
	m.CapUsed, m.CapTotal = w.CapUsed, w.CapTotal
	m.Height = w.Height
	m.Domain = w.Domain
	m.Lease = w.Lease
	m.Ephemeral = w.Ephemeral
	m.BondSize = w.BondSize
	copy(m.BondRoot[:], w.BondRoot)
	m.Tag = w.Tag
	if w.Proof != nil {
		p := ports.StorageProof{Index: w.Proof.Index, Total: w.Proof.Total, Path: bytesToIDs(w.Proof.Path), Column: w.Proof.Column}
		copy(p.Root[:], w.Proof.Root)
		m.Proof = &p
	}
	return m
}

func idsToBytes(ids []ports.NodeID) [][]byte {
	if len(ids) == 0 {
		return nil
	}
	out := make([][]byte, len(ids))
	for i, id := range ids {
		out[i] = append([]byte(nil), id[:]...)
	}
	return out
}

func bytesToIDs(raw [][]byte) []ports.NodeID {
	if len(raw) == 0 {
		return nil
	}
	out := make([]ports.NodeID, len(raw))
	for i, b := range raw {
		copy(out[i][:], b)
	}
	return out
}
