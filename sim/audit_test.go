package sim

import (
	"reflect"
	"strings"
	"testing"
)

// The M7 acceptance test: liars — who hold valid Merkle proofs but no
// data — are caught by nonce-tag challenges and slashed; honest hosts
// pass every audit; the file survives regardless.
func TestAuditCatchesLiars(t *testing.T) {
	o := DefaultAuditOpts()
	res, err := Audit(31, o)
	if err != nil {
		t.Fatalf("seed %d: %v\n%s", res.Seed, err, strings.Join(res.Timeline, "\n"))
	}
	if res.LiarsCaught != o.Liars {
		t.Fatalf("seed %d: only %d of %d liars failed an audit\n%s",
			res.Seed, res.LiarsCaught, o.Liars, strings.Join(res.Timeline, "\n"))
	}
	if res.LiarsNegative != o.Liars {
		t.Fatalf("seed %d: %d of %d liars slashed into debt", res.Seed, res.LiarsNegative, o.Liars)
	}
	if res.HonestFailed != 0 {
		t.Fatalf("seed %d: %d honest nodes failed audits — false accusations are a bug",
			res.Seed, res.HonestFailed)
	}
	if res.Report.Passed == 0 {
		t.Fatalf("seed %d: no audits passed — honest hosts should earn", res.Seed)
	}
	if !res.Retrieved {
		t.Fatalf("seed %d: file should survive liars via honest replicas + parity", res.Seed)
	}
	t.Logf("\n%s\n%s", strings.Join(res.Timeline, "\n"), res)
}

func TestAuditIsDeterministic(t *testing.T) {
	o := DefaultAuditOpts()
	o.Nodes = 20
	o.Liars = 4
	o.FileSize = 64 << 10
	r1, err1 := Audit(6, o)
	r2, err2 := Audit(6, o)
	if err1 != nil || err2 != nil {
		t.Fatalf("errs: %v / %v", err1, err2)
	}
	if !reflect.DeepEqual(r1, r2) {
		t.Fatalf("same seed diverged:\n%+v\n%+v", r1, r2)
	}
}
