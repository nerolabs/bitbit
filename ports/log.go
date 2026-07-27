package ports

import "fmt"

// LogLevel orders severities. Error is always the smallest so a sink's
// threshold check is a single comparison: a level is emitted when
// lvl <= min.
type LogLevel int8

const (
	LogError LogLevel = iota // something broke and was not recovered
	LogWarn                  // something broke but the system routed around it
	LogInfo                  // the normal path, narrated (placements, repairs, verdicts)
	LogDebug                 // per-event detail (timeouts, dial failures, drops)
)

func (l LogLevel) String() string {
	switch l {
	case LogError:
		return "error"
	case LogWarn:
		return "warn"
	case LogInfo:
		return "info"
	case LogDebug:
		return "debug"
	}
	return "unknown"
}

// ParseLevel is the inverse of String: it maps a level name to a
// LogLevel, so an operator can dial the log threshold from a flag
// (error → quietest, debug → firehose).
func ParseLevel(s string) (LogLevel, error) {
	switch s {
	case "error":
		return LogError, nil
	case "warn":
		return LogWarn, nil
	case "info":
		return LogInfo, nil
	case "debug":
		return LogDebug, nil
	}
	return 0, fmt.Errorf("ports: unknown log level %q (want error|warn|info|debug)", s)
}

// Logger is the observability port. Core code narrates through it and
// stays pure: where the lines go (a file, stderr, nowhere) is an
// adapter's concern. kv is alternating key, value pairs — structured
// enough to grep, nothing more.
//
// A nil Logger is valid everywhere one is accepted and means "off".
// Callers on hot paths should check Enabled before building arguments,
// which is what keeps disabled logging free.
type Logger interface {
	Enabled(LogLevel) bool
	Log(lvl LogLevel, event string, kv ...any)
}

// LogIf is the canonical nil-safe emit: every component's logf helper
// delegates here, so the "is logging on" contract lives in one place.
func LogIf(lg Logger, lvl LogLevel, event string, kv ...any) {
	if lg != nil && lg.Enabled(lvl) {
		lg.Log(lvl, event, kv...)
	}
}
