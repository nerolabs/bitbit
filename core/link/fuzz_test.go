package link

import "testing"

// FuzzParse drives arbitrary strings through every link parser. A link is
// user-supplied text (CLI arg, API field, pasted share handle) — untrusted
// input — so each parser must return an error on anything malformed and
// never panic (Gate 1 / anti-persona #14). ParseAnyCare is included
// because it composes Parse and ParseCare.
func FuzzParse(f *testing.F) {
	// Seed with real links so the fuzzer mutates outward from valid
	// structure (prefix, base64url body, ':' separator).
	var h Handle
	for i := range h.Root {
		h.Root[i] = byte(i)
	}
	for i := range h.Key {
		h.Key[i] = byte(i + 32)
	}
	f.Add(h.String())
	f.Add(h.Care().String())
	f.Add("silt:v1:")
	f.Add("siltcare:v1:not-base64:also-not")
	f.Add("")
	f.Add(":::")

	f.Fuzz(func(t *testing.T, s string) {
		// We assert only the anti-crash contract: a parser may accept or
		// reject, but must never panic on hostile input.
		_, _ = Parse(s)
		_, _ = ParseCare(s)
		_, _ = ParseAnyCare(s)
	})
}
