package schedule

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

// Hardening for watch schedules: what a stored script may touch, what the
// history and the delivered result may reveal, and which ids may become a
// path.

// MaxLiveWatchesPerUser caps the live watches one (non-admin) user may hold.
const MaxLiveWatchesPerUser = 10

var (
	scheduleIDRe = regexp.MustCompile(`^sm_[0-9a-fA-F-]{8,64}$`)
	runIDRe      = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}-[0-9]{2}-[0-9]{2}\.[0-9]{9}Z$`)
)

// ValidScheduleID reports whether id is a schedule id wick minted — the
// only shape allowed to become part of a path.
func ValidScheduleID(id string) bool { return scheduleIDRe.MatchString(id) }

// ValidRunID reports whether id is a run id wick minted (see runID).
func ValidRunID(id string) bool { return runIDRe.MatchString(id) }

// watchExecDir is the directory a watch's bash steps run in: the target
// project's directory, or for a session without a project its own
// sessions/<id>/cwd. The real path (symlinks resolved) must sit inside that
// root — a cwd that resolves elsewhere is refused rather than "fixed", and
// there is no fallback to the daemon's own working directory.
func watchExecDir(layout agentconfig.Layout, m entity.ScheduledMessage) (string, error) {
	cwd := WatchCwd(layout, m)
	if cwd == "" {
		return "", errors.New("cannot resolve the project/session directory for bash steps")
	}
	// A session's own cwd may not exist before its first spawn; it is ours
	// to create. A project directory is never created here.
	if strings.HasPrefix(filepath.Clean(cwd), filepath.Clean(layout.SessionsDir())+string(os.PathSeparator)) {
		_ = os.MkdirAll(cwd, 0o755)
	}
	real, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", fmt.Errorf("bash cwd %s: %w", cwd, err)
	}
	var roots []string
	pid := m.ProjectID
	if pid == "" && m.SessionID != "" {
		if sp := sessionProjectID(layout, m.SessionID); sp != "" {
			pid = sp
		}
	}
	if pid != "" {
		if p, perr := project.ResolvePath(layout, pid); perr == nil {
			roots = append(roots, p)
		}
	}
	roots = append(roots, layout.SessionsDir())
	for _, root := range roots {
		rr, rerr := filepath.EvalSymlinks(root)
		if rerr != nil {
			continue
		}
		if real == rr || strings.HasPrefix(real, rr+string(os.PathSeparator)) {
			return real, nil
		}
	}
	return "", fmt.Errorf("bash cwd %s resolves outside the project", cwd)
}

// Redaction. Applied to everything written to the run history and to the
// result delivered to the session — never to the data piped between steps,
// which a check or jq needs verbatim.
var (
	secretKeyRe = regexp.MustCompile(`(?i)(token|secret|passw(or)?d|authorization|api[_-]?key|apikey|cookie|private[_-]?key|credential|session[_-]?key|access[_-]?key)`)
	jwtRe       = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}`)
	bearerRe    = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	kvSecretRe  = regexp.MustCompile(`(?i)\b([a-z0-9_-]*(token|secret|password|passwd|api[_-]?key|apikey|authorization|cookie)[a-z0-9_-]*)("?\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;&]+)`)
	// The optional `"` after a key lets both regexes catch JSON quoted
	// inside free text ({"api_key":"…"} in an error body).
	// kvURLRe catches env-style connection strings: DATABASE_URL=…,
	// REDIS_URL: …, *DSN=…. Upper-case _URL only, so an API's html_url or
	// avatar_url stays readable; DSN in any case.
	kvURLRe = regexp.MustCompile(`\b([A-Z0-9_]*_URL|[A-Za-z0-9_]*(?i:dsn))("?\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;&]+)`)
	// urlKeyRe is the same rule for a JSON key.
	urlKeyRe = regexp.MustCompile(`(^[A-Z0-9_]*_URL$|(?i)dsn)`)
	// userinfoRe is the user:password@ part of a URL (postgres://u:p@host).
	userinfoRe = regexp.MustCompile(`(://)[^:/@\s]+:[^@\s]+@`)
	// pemRe is a private key block; pemOpenRe one cut off before its END.
	pemRe     = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	pemOpenRe = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*$`)
	awsKeyRe  = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)
)

// errorMax caps a stored step / run error (runes): the full stderr is kept,
// redacted, in the step's Stderr.
const errorMax = 2000

// clipError redacts and caps an error message before it is stored.
func clipError(s string) string {
	s = Redact(s)
	if r := []rune(s); len(r) > errorMax {
		return string(r[:errorMax]) + "…"
	}
	return s
}

const redacted = "[redacted]"

// redactString masks private key blocks, URL passwords, AWS access key ids,
// bearer/basic credentials, JWTs and key=value secrets (connection strings
// included) inside free text. wick_enc_ tokens are already ciphertext and stay.
func redactString(s string) string {
	s = pemRe.ReplaceAllString(s, redacted)
	s = pemOpenRe.ReplaceAllString(s, redacted)
	s = userinfoRe.ReplaceAllString(s, "${1}"+redacted+"@")
	s = awsKeyRe.ReplaceAllString(s, redacted)
	s = jwtRe.ReplaceAllString(s, redacted)
	s = bearerRe.ReplaceAllString(s, "$1 "+redacted)
	s = kvSecretRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := kvSecretRe.FindStringSubmatch(m)
		if strings.Contains(sub[4], "wick_enc_") {
			return m
		}
		return sub[1] + sub[3] + redacted
	})
	s = kvURLRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := kvURLRe.FindStringSubmatch(m)
		if strings.Contains(sub[3], "wick_enc_") {
			return m
		}
		return sub[1] + sub[2] + redacted
	})
	return s
}

// redactValue walks decoded JSON, masking the value of any key that names a
// secret, and running redactString over every other string.
func redactValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if secretKeyRe.MatchString(k) || urlKeyRe.MatchString(k) {
				if s, ok := val.(string); ok && strings.HasPrefix(s, "wick_enc_") {
					out[k] = s
					continue
				}
				if val != nil {
					out[k] = redacted
					continue
				}
			}
			out[k] = redactValue(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = redactValue(val)
		}
		return out
	case string:
		if strings.HasPrefix(x, "wick_enc_") {
			return x
		}
		return redactString(x)
	default:
		return v
	}
}

// Redact masks secrets in a step output: structurally when it is JSON, as
// text otherwise.
func Redact(s string) string {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
		var v any
		dec := json.NewDecoder(strings.NewReader(t))
		dec.UseNumber()
		if dec.Decode(&v) == nil {
			if b, err := json.Marshal(redactValue(v)); err == nil {
				return string(b)
			}
		}
	}
	return redactString(s)
}

// redactParams is Redact for a connector step's params.
func redactParams(p map[string]any) map[string]any {
	if len(p) == 0 {
		return nil
	}
	out, _ := redactValue(map[string]any(p)).(map[string]any)
	return out
}

// sessionProjectID is the project a session belongs to, "" when none.
func sessionProjectID(layout agentconfig.Layout, sessionID string) string {
	if sess, err := session.Load(layout, sessionID); err == nil {
		return sess.Meta.ProjectID
	}
	return ""
}
