package link

import (
	"strings"
	"testing"

	"github.com/nerolabs/silt/ports"
)

func handle() Handle {
	return Handle{Root: ports.HashBytes([]byte("root")), Key: ports.HashBytes([]byte("key"))}
}

func TestFormatParseRoundtrip(t *testing.T) {
	h := handle()
	got, err := Parse(h.String())
	if err != nil {
		t.Fatal(err)
	}
	if got != h {
		t.Fatalf("full link mangled: %+v", got)
	}
	c := h.Care()
	gotC, err := ParseCare(c.String())
	if err != nil {
		t.Fatal(err)
	}
	if gotC != c {
		t.Fatalf("care link mangled: %+v", gotC)
	}
}

func TestParseAnyCare(t *testing.T) {
	h := handle()
	fromFull, err := ParseAnyCare(h.String())
	if err != nil {
		t.Fatal(err)
	}
	fromCare, err := ParseAnyCare(h.Care().String())
	if err != nil {
		t.Fatal(err)
	}
	if fromFull != fromCare || fromFull != h.Care() {
		t.Fatal("both link forms must degrade to the same care capability")
	}
}

// Parsing a care link as a FULL link fails with a message that names the care
// link (not a bare "not a link", which reads like a typo — acceptance F6).
func TestParseFullRejectsCareLinkClearly(t *testing.T) {
	h := handle()
	_, err := Parse(h.Care().String())
	if err == nil {
		t.Fatal("a care link must not parse as a full (fetch) link")
	}
	if !strings.Contains(err.Error(), "care link") {
		t.Fatalf("error should name the care link, got: %v", err)
	}
}

func TestKeyHierarchyIsOneWay(t *testing.T) {
	h := handle()
	if h.LayoutKey() == h.Key || h.ContentKey() == h.Key || h.LayoutKey() == h.ContentKey() {
		t.Fatal("derived keys must all differ from each other and the parent")
	}
	// Determinism: same link, same derivations, forever.
	if h.LayoutKey() != handle().LayoutKey() {
		t.Fatal("derivation must be deterministic")
	}
}

func TestRejectsJunk(t *testing.T) {
	for _, s := range []string{"", "silt:v1:tooshort", "notalink", "silt:v1:xx:yy",
		"siltcare:v1:" + handle().Root.String()} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) should fail", s)
		}
	}
}
