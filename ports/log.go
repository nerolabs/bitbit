package ports

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
