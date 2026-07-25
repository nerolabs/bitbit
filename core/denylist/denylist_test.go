package denylist_test

import (
	"strings"
	"testing"

	"github.com/nerolabs/silt/core/denylist"
	"github.com/nerolabs/silt/ports"
)

func TestAddDeniedNilSafe(t *testing.T) {
	var nilSet *denylist.Set
	if nilSet.Denied(ports.HashBytes([]byte("x"))) {
		t.Fatal("nil set must deny nothing")
	}
	s := denylist.New()
	r := ports.HashBytes([]byte("bad"))
	if s.Denied(r) {
		t.Fatal("empty set denies nothing")
	}
	s.Add(r)
	if !s.Denied(r) || s.Len() != 1 {
		t.Fatal("Add/Denied broken")
	}
	if s.Denied(ports.HashBytes([]byte("good"))) {
		t.Fatal("only added roots are denied")
	}
}

func TestLoadInto(t *testing.T) {
	r1 := ports.HashBytes([]byte("one"))
	r2 := ports.HashBytes([]byte("two"))
	text := "# a comment\n" + r1.String() + "\n\n  " + r2.String() + "  \nnot-hex-garbage\n"
	s := denylist.New()
	if err := denylist.LoadInto(s, strings.NewReader(text)); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 2 || !s.Denied(r1) || !s.Denied(r2) {
		t.Fatalf("loaded %d roots, want 2 (comments/blanks/garbage skipped)", s.Len())
	}
}
