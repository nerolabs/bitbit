package sim

import "testing"

func TestTakedownMakesContentUnreachable(t *testing.T) {
	res, err := Takedown(7)
	if err != nil {
		t.Fatal(err)
	}
	if res.Purged == 0 {
		t.Fatal("no chunks were purged — hosts weren't holding the denied root?")
	}
	if !res.DeniedBlocked {
		t.Fatalf("denied file was still retrievable after takedown: %s", res)
	}
	if !res.ControlSurvives {
		t.Fatalf("takedown damaged an unrelated file: %s", res)
	}
	t.Logf("\n%s", res)
}
