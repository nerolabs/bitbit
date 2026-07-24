package sim

import (
	"reflect"
	"testing"
)

// The M5 acceptance test: after real traffic, freeloaders — who fetch
// but never store or serve — cannot afford to publish again, while
// nodes that hosted and served content can.
func TestEconomyFreeloadersLosePublishing(t *testing.T) {
	o := DefaultEconomyOpts()
	res, err := Economy(21, o)
	if err != nil {
		t.Fatalf("seed %d: %v\n%s", res.Seed, err, timeline(res))
	}
	if res.SeedPublishOK != o.Nodes {
		t.Fatalf("seed %d: grant should cover everyone's first publish (%d/%d ok)",
			res.Seed, res.SeedPublishOK, o.Nodes)
	}
	if res.FreeloadersRejected != o.Freeloaders {
		t.Fatalf("seed %d: %d of %d freeloaders rejected — all should be broke\n%s",
			res.Seed, res.FreeloadersRejected, o.Freeloaders, timeline(res))
	}
	if res.SecondOK == 0 {
		t.Fatalf("seed %d: nobody could afford a second publish — servers should have earned enough\n%s",
			res.Seed, timeline(res))
	}
	if res.Gini <= 0 || res.Gini >= 1 {
		t.Fatalf("seed %d: Gini %.3f out of the plausible open interval", res.Seed, res.Gini)
	}
	t.Logf("\n%s\n%s", timeline(res), res)
}

func TestEconomyIsDeterministic(t *testing.T) {
	o := DefaultEconomyOpts()
	o.Nodes = 24
	o.Freeloaders = 4
	r1, err1 := Economy(8, o)
	r2, err2 := Economy(8, o)
	if err1 != nil || err2 != nil {
		t.Fatalf("errs: %v / %v", err1, err2)
	}
	if !reflect.DeepEqual(r1, r2) {
		t.Fatalf("same seed diverged:\n%+v\n%+v", r1, r2)
	}
}

func timeline(r EconomyResult) string {
	s := ""
	for _, line := range r.Timeline {
		s += line + "\n"
	}
	return s
}
