package providersync

import (
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/yogasw/wick/internal/entity"
)

// collectExcludePatterns returns the SyncPath of every enabled exclude-mode
// source as a glob pattern. Exclude sources live as ordinary rows now —
// each one contributes a single pattern — so this is just a filtered map.
func collectExcludePatterns(sources []entity.ProviderStorageSource) []string {
	out := make([]string, 0, len(sources))
	for _, s := range sources {
		if !s.Enabled || s.Mode != "exclude" {
			continue
		}
		p := strings.TrimSpace(s.SyncPath)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// matchesAnyExclude returns true if abs matches any of the glob patterns.
// abs and patterns are normalised to forward-slash before matching.
func matchesAnyExclude(abs string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	a := filepath.ToSlash(abs)
	for _, p := range patterns {
		if globMatch(p, a) {
			return true
		}
	}
	return false
}

// globMatch supports `*` (any chars except '/'), `?` (single non-slash char),
// and `**` (any chars including '/' across multiple segments).
// Conveniences for the UX where a user clicks "Ignore" on a folder:
//   - a slashless pattern (e.g. "node_modules", "*.log") matches any path
//     segment, not just the basename — gitignore-style.
//   - a wildcard-free pattern with slashes (e.g. "C:/Users/x/logs") matches
//     the dir AND every descendant. Folder ignores work without making the
//     user type "/**" manually.
func globMatch(pattern, path string) bool {
	// Normalise to forward slashes on all platforms — filepath.ToSlash only
	// converts the OS separator, so Windows backslashes pass through unchanged
	// on Linux/macOS. Use strings.ReplaceAll for cross-platform correctness.
	pattern = strings.ReplaceAll(pattern, "\\", "/")
	path = strings.ReplaceAll(path, "\\", "/")
	// Drop a trailing slash on the pattern — both Clean and human input
	// can produce it, and "/foo/" should match the same set as "/foo".
	if len(pattern) > 1 {
		pattern = strings.TrimRight(pattern, "/")
	}
	if !strings.Contains(pattern, "/") {
		re := globRegex(pattern)
		for _, seg := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
			if seg != "" && re.MatchString(seg) {
				return true
			}
		}
		return false
	}
	if !strings.ContainsAny(pattern, "*?") {
		return path == pattern || strings.HasPrefix(path, pattern+"/")
	}
	return globRegex(pattern).MatchString(path)
}

var (
	globCache   = make(map[string]*regexp.Regexp)
	globCacheMu sync.RWMutex
)

func globRegex(pattern string) *regexp.Regexp {
	globCacheMu.RLock()
	if r, ok := globCache[pattern]; ok {
		globCacheMu.RUnlock()
		return r
	}
	globCacheMu.RUnlock()
	var b strings.Builder
	b.WriteString("^")
	i := 0
	for i < len(pattern) {
		c := pattern[i]
		switch {
		case c == '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i += 2
				// swallow a trailing slash so "**/" matches zero segments too
				if i < len(pattern) && pattern[i] == '/' {
					i++
				}
			} else {
				b.WriteString("[^/]*")
				i++
			}
		case c == '?':
			b.WriteString("[^/]")
			i++
		case c == '.', c == '+', c == '(', c == ')', c == '|', c == '^', c == '$', c == '{', c == '}', c == '\\', c == '[', c == ']':
			b.WriteByte('\\')
			b.WriteByte(c)
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	b.WriteString("$")
	r := regexp.MustCompile(b.String())
	globCacheMu.Lock()
	globCache[pattern] = r
	globCacheMu.Unlock()
	return r
}
