package logfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerolabs/silt/ports"
)

func fixedNow() time.Time { return time.Date(2026, 7, 26, 17, 0, 0, 412e6, time.UTC) }

func TestLineShape(t *testing.T) {
	var out strings.Builder
	s := New(&out, ports.LogDebug)
	s.now = fixedNow
	s.Log(ports.LogWarn, "dial failed", "addr", "1.2.3.4:4001", "err", "connection refused")
	got := out.String()
	want := `2026-07-26T17:00:00.412Z warn dial failed addr=1.2.3.4:4001 err="connection refused"` + "\n"
	if got != want {
		t.Fatalf("line:\n got %q\nwant %q", got, want)
	}
}

func TestLevelThreshold(t *testing.T) {
	var out strings.Builder
	s := New(&out, ports.LogWarn) // warn and error only
	s.now = fixedNow
	s.Log(ports.LogInfo, "narration")
	s.Log(ports.LogDebug, "detail")
	if out.Len() != 0 {
		t.Fatalf("info/debug leaked past a warn threshold: %q", out.String())
	}
	s.Log(ports.LogError, "boom")
	if !strings.Contains(out.String(), "error boom") {
		t.Fatalf("error line missing: %q", out.String())
	}
	if !s.Enabled(ports.LogError) || s.Enabled(ports.LogInfo) {
		t.Fatal("Enabled disagrees with the threshold")
	}
}

func TestOddKVAndUnpairedKeyDropped(t *testing.T) {
	var out strings.Builder
	s := New(&out, ports.LogDebug)
	s.now = fixedNow
	s.Log(ports.LogInfo, "odd", "k1", 1, "dangling")
	if strings.Contains(out.String(), "dangling") {
		t.Fatalf("unpaired key should be dropped: %q", out.String())
	}
	if !strings.Contains(out.String(), "k1=1") {
		t.Fatalf("paired kv missing: %q", out.String())
	}
}

func TestOpenAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.log")
	for i := 0; i < 2; i++ {
		s, err := Open(path, ports.LogDebug)
		if err != nil {
			t.Fatal(err)
		}
		s.Log(ports.LogInfo, "run", "n", i)
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(b), "\n"); got != 2 {
		t.Fatalf("want 2 appended lines across reopens, got %d:\n%s", got, b)
	}
}
