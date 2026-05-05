package cli

import (
	"os"
	"regexp"
	"strings"
)

// resolveBundleID derives a CFBundleIdentifier-style reverse-DNS string
// from go.mod's module path. github.com/owner/app → com.owner.app.
// Falls back to com.example.<appName> when go.mod is missing or the
// module path can't be parsed.
//
// Lowercased, dots only — anything outside [a-z0-9.-] gets replaced with
// "-" so Apple's bundle ID rules ([A-Za-z0-9-.]) hold.
func resolveBundleID(appName string) string {
	const fallback = "com.example."
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return sanitizeBundleID(fallback + appName)
	}
	re := regexp.MustCompile(`(?m)^module\s+(\S+)`)
	m := re.FindStringSubmatch(string(data))
	if len(m) < 2 {
		return sanitizeBundleID(fallback + appName)
	}
	parts := strings.Split(strings.ToLower(m[1]), "/")
	// github.com/owner/app → com.owner.app
	// gitlab.com/group/sub/app → com.group.sub.app
	// custom.example.dev/app → dev.example.custom.app (full reverse)
	if len(parts) >= 3 && (parts[0] == "github.com" || parts[0] == "gitlab.com" || parts[0] == "bitbucket.org") {
		return sanitizeBundleID("com." + strings.Join(parts[1:], "."))
	}
	if len(parts) >= 2 {
		host := strings.Split(parts[0], ".")
		reversed := make([]string, 0, len(host))
		for i := len(host) - 1; i >= 0; i-- {
			reversed = append(reversed, host[i])
		}
		return sanitizeBundleID(strings.Join(append(reversed, parts[1:]...), "."))
	}
	return sanitizeBundleID(fallback + appName)
}

func sanitizeBundleID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}
