package codemode

import (
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Settings keys (owner "agents") an admin sets the limits with, in seconds.
const (
	KeyDefaultTimeout = "codemode_default_timeout_sec"
	KeyMaxTimeout     = "codemode_max_timeout_sec"
	KeyCallTimeout    = "codemode_call_timeout_sec"
)

// HardMaxTimeout bounds every setting: a typo of 30000 must not keep a run
// (and its runner slot) for days.
const HardMaxTimeout = time.Hour

// Limits are the run and call deadlines Code Mode applies. A zero field means
// the built-in constant.
type Limits struct {
	// Default is a run's deadline when the caller asks for none.
	Default time.Duration
	// Max caps the deadline a caller asks for.
	Max time.Duration
	// Call is one connector call's default deadline.
	Call time.Duration
}

// DefaultLimits are the built-in constants.
func DefaultLimits() Limits {
	return Limits{Default: DefaultTimeout, Max: MaxTimeout, Call: CallTimeout}
}

// Normalize fills unset or nonsense fields with the constants and keeps the
// three consistent: Default never above Max, Call never above Max.
func (l Limits) Normalize() Limits {
	if l.Max <= 0 || l.Max > HardMaxTimeout {
		l.Max = MaxTimeout
	}
	if l.Default <= 0 || l.Default > HardMaxTimeout {
		l.Default = DefaultTimeout
	}
	if l.Call <= 0 || l.Call > HardMaxTimeout {
		l.Call = CallTimeout
	}
	l.Default = min(l.Default, l.Max)
	l.Call = min(l.Call, l.Max)
	return l
}

// Clamp is the deadline of a run that asked for d (0 = none).
func (l Limits) Clamp(d time.Duration) time.Duration {
	l = l.Normalize()
	if d <= 0 {
		return l.Default
	}
	return min(d, l.Max)
}

// ParseLimits reads the three settings through get (key → raw value). Empty,
// non-numeric and <=0 values fall back to the constants.
func ParseLimits(get func(key string) string) Limits {
	sec := func(key string) time.Duration {
		n, err := strconv.Atoi(strings.TrimSpace(get(key)))
		if err != nil || n <= 0 {
			return 0
		}
		return time.Duration(n) * time.Second
	}
	return Limits{Default: sec(KeyDefaultTimeout), Max: sec(KeyMaxTimeout), Call: sec(KeyCallTimeout)}.Normalize()
}

var limitsSource atomic.Pointer[func() Limits]

// SetLimitsSource makes fn the source of CurrentLimits. It is read on every
// run, so a settings change applies to the next run without a restart. nil =
// the built-in constants.
func SetLimitsSource(fn func() Limits) {
	if fn == nil {
		limitsSource.Store(nil)
		return
	}
	limitsSource.Store(&fn)
}

// CurrentLimits are the limits in force now, normalized.
func CurrentLimits() Limits {
	if fn := limitsSource.Load(); fn != nil {
		return (*fn)().Normalize()
	}
	return DefaultLimits()
}
