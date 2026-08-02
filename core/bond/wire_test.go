package bond

import (
	"testing"
)

// A valid answer must still verify after crossing the wire (it rides
// MsgBondReply.Data as CBOR) — the property the bond auditor depends on.
func TestAnswerSurvivesWireRoundTrip(t *testing.T) {
	c := Seal(secret(3), 1<<20)
	ans, ok := c.Answer(77)
	if !ok {
		t.Fatal("holder should answer its own challenge")
	}
	raw, err := EncodeAnswer(ans)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeAnswer(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !Verify(c.Root, c.Size, 77, got) {
		t.Fatal("a valid answer must still verify after an encode/decode round trip")
	}
}
