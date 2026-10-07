package event

import (
	"fmt"
	"strings"
)

// limitMarkers are substrings vendors use when an ACCOUNT (not a request)
// has run out: rate limits, plan quotas, usage caps. Lower-case.
var limitMarkers = []string{
	"rate limit", "rate_limit", "ratelimit", "usage limit", "usage_limit",
	"quota", "too many requests", "429", "insufficient_quota",
	"limit reached", "limit exceeded", "exceeded your", "credit balance",
}

// IsAccountLimit reports whether msg reads like an account hitting its
// rate limit / quota.
func IsAccountLimit(msg string) bool {
	m := strings.ToLower(msg)
	for _, k := range limitMarkers {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
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
