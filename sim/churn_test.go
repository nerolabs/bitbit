package sim

import (
	"reflect"
	"testing"
)

// The M4 acceptance test: kill 30% of the swarm in waves; the repair
// loop must restore redundancy and the file must survive bit-perfectly.
func TestChurnRepairKeepsFileAlive(t *testing.T) {
	o := DefaultChurnOpts()
	res, err := Churn(11, o)
	if err != nil {
		t.Fatalf("seed %d: %v\ntimeline:\n%s", res.Seed, err, res.TimelineString())
	}
	if !res.Match {
		t.Fatalf("seed %d: bytes differ\ntimeline:\n%s", res.Seed, res.TimelineString())
	}
	if res.Repairs == 0 {
		t.Fatalf("seed %d: churn never triggered a repair — scenario too gentle to prove anything", res.Seed)
	}
	if res.MinFinal <= res.MinAfterKill {
		t.Fatalf("seed %d: repair did not raise redundancy (after kills %d, final %d)",
			res.Seed, res.MinAfterKill, res.MinFinal)
	}
	t.Logf("\n%s\n%s", res.TimelineString(), res)
}

// Control: with no caretakers, nothing repairs and redundancy stays down.
func TestChurnWithoutRepairDegrades(t *testing.T) {
	o := DefaultChurnOpts()
	o.Caretakers = 0
	res, err := Churn(11, o)
	if res.Repairs != 0 {
		t.Fatalf("no caretakers but %d repairs happened", res.Repairs)
	}
	// The file may or may not survive on parity alone — but redundancy
	// must NOT have recovered.
	withRepair, err2 := Churn(11, DefaultChurnOpts())
	if err2 != nil {
		t.Fatal(err2)
	}
	if res.MinFinal >= withRepair.MinFinal {
		t.Fatalf("redundancy without repair (%d) should be worse than with (%d); err=%v",
			res.MinFinal, withRepair.MinFinal, err)
	}
}

func TestChurnIsDeterministic(t *testing.T) {
	o := DefaultChurnOpts()
	o.Nodes = 40
	o.FileSize = 128 << 10
	r1, err1 := Churn(5, o)
	r2, err2 := Churn(5, o)
	if err1 != nil || err2 != nil {
		t.Fatalf("errs: %v / %v", err1, err2)
	}
	if !reflect.DeepEqual(r1, r2) {
		t.Fatalf("same seed diverged:\n%+v\n%+v", r1, r2)
	}
}
