package ports

import "testing"

func TestParseLevelRoundTrip(t *testing.T) {
	for _, lvl := range []LogLevel{LogError, LogWarn, LogInfo, LogDebug} {
		got, err := ParseLevel(lvl.String())
		if err != nil {
			t.Fatalf("ParseLevel(%q): %v", lvl.String(), err)
		}
		if got != lvl {
			t.Fatalf("ParseLevel(%q) = %v, want %v", lvl.String(), got, lvl)
		}
	}
}

func TestParseLevelRejectsUnknown(t *testing.T) {
	if _, err := ParseLevel("verbose"); err == nil {
		t.Fatal("ParseLevel(\"verbose\") should error")
	}
	if _, err := ParseLevel(""); err == nil {
		t.Fatal("ParseLevel(\"\") should error (the flag layer, not this, maps empty to off)")
	}
}

// TestInfoThreshold documents the level the -log info flag selects:
// info and above (warn, error) narrate, the debug firehose stays off.
func TestInfoThreshold(t *testing.T) {
	info, _ := ParseLevel("info")
	for _, on := range []LogLevel{LogError, LogWarn, LogInfo} {
		if on > info { // higher number = finer; on must be <= threshold
			t.Fatalf("%v should be enabled at info", on)
		}
	}
	if LogDebug <= info {
		t.Fatal("debug must be suppressed at info threshold")
	}
}
