package event

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	// Reset times name their zone ("Asia/Jakarta"); without the embedded
	// database a host lacking /usr/share/zoneinfo would read them as its
	// own zone, hours off.
	_ "time/tzdata"
)

// ErrKind sorts a provider error by what it says about the ACCOUNT behind
// the instance, which is what decides whether another instance could take
// the turn: a spent quota or a dead login is the instance's problem, a
// bug in the request is not.
type ErrKind int

const (
	// ErrNone is any error that is not about the account: a bad request,
	// a tool failure, a transient frame the CLI retries by itself.
	ErrNone ErrKind = iota
	// ErrLimit is a rate limit, plan quota or usage cap that ran out.
	ErrLimit
	// ErrAuth is a login that no longer works: expired, revoked, missing.
	ErrAuth
	// ErrUnavailable is the vendor itself failing: overloaded, 5xx.
	ErrUnavailable
)

// String names the kind for logs and notices. Not used for serialization.
func (k ErrKind) String() string {
	switch k {
	case ErrLimit:
		return "limit"
	case ErrAuth:
		return "auth"
	case ErrUnavailable:
		return "unavailable"
	default:
		return "none"
	}
}

// limitMarkers are substrings vendors use when an ACCOUNT (not a request)
// has run out: rate limits, plan quotas, usage caps. Lower-case.
var limitMarkers = []string{
	"rate limit", "rate_limit", "ratelimit", "usage limit", "usage_limit",
	"quota", "too many requests", "insufficient_quota",
	"limit reached", "limit exceeded", "exceeded your", "credit balance",
	// Claude Code's own wording: "You've hit your session limit · resets
	// 4:40pm (Asia/Jakarta)", "You've hit your weekly limit", "Claude AI
	// usage limit reached".
	"session limit", "weekly limit", "hit your limit", "hit your session",
	"hit your weekly", "hit your opus", "usage limit reached", "spend limit",
}

// authMarkers say the login itself is gone. Lower-case. The bare status
// code is matched as a word (authCode) so "4010 tokens" does not count.
var authMarkers = []string{
	"403 forbidden", "invalid api key", "invalid_api_key", "invalid x-api-key",
	"authentication_error", "authentication failed", "oauth token has expired",
	"oauth token expired", "please run /login", "not logged in",
	"token revoked", "token has been revoked", "failed to authenticate",
	"oauth session expired", "disabled claude subscription access",
}

// unavailableMarkers say the vendor is down or shedding load. Lower-case.
var unavailableMarkers = []string{
	"overloaded", "service unavailable", "bad gateway", "gateway timeout",
	"internal server error", "server_error", "api_error",
}

var (
	limitCode       = regexp.MustCompile(`\b429\b`)
	authCode        = statusCode(`401`)
	unavailableCode = statusCode(`500|502|503|504|529`)
)

// statusCode matches one of codes only where it reads as the response's
// status, not any number in the body ("API Error: 400 … max_tokens: 500"):
// at the start of the message, after "status" / "code" / "error" / "HTTP"
// ("unexpected status 502", "API Error: 529", `"statusCode":503`,
// "HTTP/1.1 500"), or followed by its reason ("503 Service Unavailable",
// "500 error").
func statusCode(codes string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|(?:status|code|error)(?:[ _]?code)?"?\s*[:=]?\s*"?|\bhttp(?:/[\d.]+)?\s+)(?:` + codes +
		`)\b|\b(?:` + codes + `)\s+(?:error|unauthori[sz]ed|internal|bad gateway|service|gateway|server|overloaded)`)
}

// IsAccountLimit reports whether msg reads like an account hitting its
// rate limit / quota.
func IsAccountLimit(msg string) bool {
	return isLimit(strings.ToLower(msg))
}

// isLimit is IsAccountLimit on an already lower-cased message. The bare
// status code is matched as a word so "14290 tokens" does not count.
func isLimit(m string) bool {
	return containsAny(m, limitMarkers) || limitCode.MatchString(m)
}

// ClassifyError sorts an error message into an ErrKind. Only feed it text the
// CLI reported AS an error (an error frame, a synthetic limit message):
// an ordinary reply that happens to mention "429" is not a limit.
//
// A limit wins over the rest — "429 … overloaded" is the account's quota,
// not the vendor's health — and auth wins over unavailable, since a 5xx
// page that also says "not logged in" will not heal by waiting.
func ClassifyError(msg string) ErrKind {
	m := strings.ToLower(strings.TrimSpace(msg))
	switch {
	case m == "":
		return ErrNone
	case isLimit(m):
		return ErrLimit
	case containsAny(m, authMarkers) || authCode.MatchString(m):
		return ErrAuth
	case containsAny(m, unavailableMarkers) || unavailableCode.MatchString(m):
		return ErrUnavailable
	}
	return ErrNone
}

func containsAny(m string, markers []string) bool {
	for _, k := range markers {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
}

// resetPattern matches Claude Code's "resets 4:40pm (Asia/Jakarta)" and
// the weekly "resets Oct 14, 9am (Asia/Jakarta)". Groups: month, day,
// hour, minute, am/pm, zone — every one but hour optional.
var resetPattern = regexp.MustCompile(`(?i)\bresets\s+(?:at\s+)?(?:([a-z]{3,9})\.?\s+(\d{1,2}),?\s+(?:at\s+)?)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\b\s*(?:\(([^)]+)\))?`)

// maxResetAhead caps a parsed reset: no vendor window is longer than a
// week, so anything further out is a misread, not a plan.
const maxResetAhead = 7 * 24 * time.Hour

// ParseResetAt reads when a limit lifts out of the message that reported
// it. The clock is in the named zone, or the server's local zone when the
// message names none (or one this host does not know). A time of day with
// no date is the next one after now; a dated reset already past is next
// year's — unless it is under a week behind, which is a reset that already
// happened and comes back as that (past) time. Without a "resets" clock, codex's relative "try again in 2 days
// 3 hours" counts from now. The result is clamped to a week ahead. ok is
// false when msg carries no reset time.
func ParseResetAt(msg string, now time.Time) (time.Time, bool) {
	m := resetPattern.FindStringSubmatch(msg)
	if m == nil {
		return parseRetryIn(msg, now)
	}
	monthStr, dayStr, hourStr, minStr, ampm, zone := m[1], m[2], m[3], m[4], strings.ToLower(m[5]), strings.TrimSpace(m[6])
	// A bare number ("resets 5") is too loose to trust.
	if ampm == "" && minStr == "" {
		return time.Time{}, false
	}
	hour, _ := strconv.Atoi(hourStr)
	minute := 0
	if minStr != "" {
		minute, _ = strconv.Atoi(minStr)
	}
	if minute > 59 {
		return time.Time{}, false
	}
	switch ampm {
	case "":
		if hour > 23 {
			return time.Time{}, false
		}
	default:
		if hour < 1 || hour > 12 {
			return time.Time{}, false
		}
		if hour == 12 {
			hour = 0
		}
		if ampm == "pm" {
			hour += 12
		}
	}
	loc := time.Local
	if zone != "" {
		if l, err := time.LoadLocation(zone); err == nil {
			loc = l
		}
	}
	ref := now.In(loc)
	var at time.Time
	if monthStr != "" {
		month, ok := parseMonth(monthStr)
		if !ok {
			return time.Time{}, false
		}
		day, _ := strconv.Atoi(dayStr)
		if day < 1 || day > 31 {
			return time.Time{}, false
		}
		at = time.Date(ref.Year(), month, day, hour, minute, 0, 0, loc)
		// A date just behind now is a reset that already happened (the
		// message is a little stale, or the clocks disagree): report it
		// as passed, not as next year's. Only a date far behind is the
		// Dec → Jan rollover.
		if !at.After(now) && now.Sub(at) > maxResetAhead {
			at = at.AddDate(1, 0, 0)
		}
	} else {
		at = time.Date(ref.Year(), ref.Month(), ref.Day(), hour, minute, 0, 0, loc)
		if !at.After(now) {
			at = at.AddDate(0, 0, 1)
		}
	}
	if at.Sub(now) > maxResetAhead {
		at = now.Add(maxResetAhead)
	}
	return at, true
}

// retryInPattern matches codex's relative wait, "try again in 2 days 3
// hours" / "try again in 45 minutes"; retryUnit reads each part of it.
var (
	retryInPattern = regexp.MustCompile(`(?i)try again in\s+((?:\d+\s*(?:days?|hours?|hrs?|minutes?|mins?|seconds?|secs?)[\s,]*(?:and\s+)?)+)`)
	retryUnit      = regexp.MustCompile(`(?i)(\d+)\s*(day|hour|hr|min|sec)`)
)

// parseRetryIn reads a relative wait as now plus that span, clamped like
// a clock reset.
func parseRetryIn(msg string, now time.Time) (time.Time, bool) {
	m := retryInPattern.FindStringSubmatch(msg)
	if m == nil {
		return time.Time{}, false
	}
	var d time.Duration
	for _, u := range retryUnit.FindAllStringSubmatch(m[1], -1) {
		n, _ := strconv.Atoi(u[1])
		switch strings.ToLower(u[2]) {
		case "day":
			d += time.Duration(n) * 24 * time.Hour
		case "hour", "hr":
			d += time.Duration(n) * time.Hour
		case "min":
			d += time.Duration(n) * time.Minute
		case "sec":
			d += time.Duration(n) * time.Second
		}
	}
	if d <= 0 {
		return time.Time{}, false
	}
	if d > maxResetAhead {
		d = maxResetAhead
	}
	return now.Add(d), true
}

// parseMonth reads "Oct", "Oct." or "October" — any prefix of an English
// month name at least three letters long.
func parseMonth(s string) (time.Month, bool) {
	s = strings.ToLower(s)
	for mo := time.January; mo <= time.December; mo++ {
		if strings.HasPrefix(strings.ToLower(mo.String()), s) {
			return mo, true
		}
	}
	return 0, false
}

// nowFunc is the clock error classification reads reset times against;
// tests pin it.
var nowFunc = time.Now

// classifyErr stamps ev with what its error message says about the
// account, and when a limit lifts if the message says.
func classifyErr(ev AgentEvent, msg string) AgentEvent {
	ev.ErrKind = ClassifyError(msg)
	if ev.ErrKind == ErrLimit {
		if at, ok := ParseResetAt(msg, nowFunc()); ok {
			ev.ResetAt = at
		}
	}
	return ev
}

// accountErrorMsg names the instance on an account-limit error, so the
// operator knows WHICH login ran dry — the basis for failing over to
// another instance later. Other errors pass through unchanged.
func accountErrorMsg(cli, instance, msg string) string {
	if !IsAccountLimit(msg) {
		return msg
	}
	if instance == "" {
		instance = cli
	}
	return fmt.Sprintf("akun instance %s/%s kena limit/kuota: %s", cli, instance, msg)
}
