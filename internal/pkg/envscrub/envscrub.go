// Package envscrub strips infrastructure credentials from the environment
// handed to agent subprocesses.
//
// The daemon needs DATABASE_URL to reach its own Postgres. Every spawner
// builds its child's environment from os.Environ() verbatim, so that DSN
// reached the AI CLI, the shell it runs, and every program that shell
// started. A `go test` in an unrelated repo read it through
// os.Getenv("DATABASE_URL"), ran GORM AutoMigrate against it, and altered
// the live `users` table: five foreign columns, eighteen foreign tables,
// three rows written into the production auth table. Logins then failed
// with SQLSTATE 0A000 ("cached plan must not change result type") because
// the schema moved under connections that had already planned against it.
//
// Nothing about that required a mistake by the operator. The DSN was
// correctly configured on the unit; inheritance did the rest, silently.
// So the fix belongs at the spawn boundary, not in any one repo's tests.
//
// Scope: this covers what an agent inherits. It deliberately does NOT
// touch opt.ExtraEnv or a provider's own credentials — those are chosen
// per spawn and are how the AI CLI authenticates.
package envscrub

import (
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
)

// DenyEnvVar names the environment variable that overrides the default
// deny list. Comma-separated. A trailing "*" makes an entry a prefix
// glob; a leading "*" makes it a suffix glob. Set it to "-" to disable
// scrubbing entirely — an escape hatch for debugging, not for normal use.
const DenyEnvVar = "WICK_AGENT_ENV_DENY"

// defaultDeny lists what no AI CLI has any business reading.
//
// Kept deliberately narrow. Patterns like "*_TOKEN" or "*_KEY" are NOT
// here: ANTHROPIC_API_KEY, OPENAI_API_KEY and friends are exactly how the
// providers authenticate, and stripping them would break every spawn. The
// entries below are infrastructure credentials with no legitimate reader
// on the child side.
var defaultDeny = []string{
	"DATABASE_URL",
	"*_DSN",
	"*_PASSWORD",
	"*_PASSWD",
}

// Scrub returns a copy of env with denied keys removed. Entries that are
// not "KEY=VALUE" are passed through untouched — that shape is what
// os/exec expects, and reshaping it is not this function's job.
func Scrub(env []string) []string {
	rules := loadRules()
	if len(rules) == 0 {
		return env
	}
	out := make([]string, 0, len(env))
	var dropped []string
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			out = append(out, kv)
			continue
		}
		key := kv[:i]
		if matchAny(rules, key) {
			dropped = append(dropped, key)
			continue
		}
		out = append(out, kv)
	}
	logDropped(dropped)
	return out
}

// ScrubOSEnv is the common case: os.Environ() with the deny list applied.
func ScrubOSEnv() []string { return Scrub(os.Environ()) }

func matchAny(rules []string, key string) bool {
	for _, r := range rules {
		switch {
		case strings.HasPrefix(r, "*"):
			if strings.HasSuffix(key, r[1:]) {
				return true
			}
		case strings.HasSuffix(r, "*"):
			if strings.HasPrefix(key, r[:len(r)-1]) {
				return true
			}
		default:
			if key == r {
				return true
			}
		}
	}
	return false
}

// loadRules resolves the deny list once per process. Reading it on every
// spawn would let a mid-flight change silently alter behaviour, and every
// spawner calls this on a hot path.
var (
	rulesOnce sync.Once
	rules     []string
)

func loadRules() []string {
	rulesOnce.Do(func() {
		raw := strings.TrimSpace(os.Getenv(DenyEnvVar))
		if raw == "-" {
			log.Warn().Str("component", "envscrub").
				Msgf("%s=- : agent subprocesses inherit the full environment, including DATABASE_URL", DenyEnvVar)
			rules = nil
			return
		}
		if raw == "" {
			rules = append([]string{}, defaultDeny...)
			return
		}
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				rules = append(rules, strings.ToUpper(p))
			}
		}
	})
	return rules
}

// logDropped reports which keys were withheld — names only, never values.
// Logged once per distinct set so a busy daemon does not repeat itself on
// every spawn, while a change in what gets dropped still surfaces.
var (
	seenMu sync.Mutex
	seen   = map[string]bool{}
)

func logDropped(keys []string) {
	if len(keys) == 0 {
		return
	}
	sort.Strings(keys)
	sig := strings.Join(keys, ",")
	seenMu.Lock()
	first := !seen[sig]
	seen[sig] = true
	seenMu.Unlock()
	if first {
		log.Info().Str("component", "envscrub").Str("keys", sig).
			Msg("withheld from agent subprocess environment")
	}
}
