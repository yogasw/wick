package provider

import "regexp"

// Agent argv can carry a live credential: the claude provider passes its
// wick MCP config inline, Authorization header and all. The argv is worth
// logging (it is how a bad spawn gets diagnosed), so the secret is masked
// instead of dropping the field.
var (
	bearerRe  = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`)
	wickTokRe = regexp.MustCompile(`\b(wick_(?:sub|cli|enc|cenc|cred)_)[A-Za-z0-9._~+/=-]+`)
)

// RedactSecret masks bearer tokens and wick-issued tokens inside s.
func RedactSecret(s string) string {
	s = bearerRe.ReplaceAllString(s, "${1}[REDACTED]")
	return wickTokRe.ReplaceAllString(s, "${1}[REDACTED]")
}

// RedactArgv returns a copy of args safe to write to a log. The input is
// never modified: the caller still execs the real argv.
func RedactArgv(args []string) []string {
	if args == nil {
		return nil
	}
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = RedactSecret(a)
	}
	return out
}
