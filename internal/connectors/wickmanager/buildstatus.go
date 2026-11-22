package wickmanager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/appname"
	"github.com/yogasw/wick/pkg/connector"
)

// deployStateEnv names the file a deploy script writes its progress to
// (one JSON object, rewritten atomically on every phase). wick only READS
// it: the build_status op never runs a script.
const deployStateEnv = "WICK_DEPLOY_STATE_FILE"

// deployState is what a deploy script writes. Unknown fields are ignored,
// so a script may add more without breaking the op.
type deployState struct {
	Old       string `json:"old"`
	New       string `json:"new"`
	Next      string `json:"next"`
	Title     string `json:"title"`
	Phase     string `json:"phase"`
	Result    string `json:"result"`
	UpdatedAt string `json:"updated_at"`
}

// deployStatePath is the state file: $WICK_DEPLOY_STATE_FILE, else
// <user cache dir>/wick-deploy/state.json.
func deployStatePath() string {
	if p := strings.TrimSpace(os.Getenv(deployStateEnv)); p != "" {
		return p
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "wick-deploy", "state.json")
}

// nextPatch is v with its last numeric segment raised by one ("0.1.692" →
// "0.1.693"); "" when v is not a dotted number (e.g. "dev").
func nextPatch(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	i := strings.LastIndex(v, ".")
	if i < 0 {
		return ""
	}
	n, err := strconv.Atoi(v[i+1:])
	if err != nil || n < 0 {
		return ""
	}
	return v[:i+1] + strconv.Itoa(n+1)
}

// deployTerminal reports whether a phase means the deploy is over.
func deployTerminal(phase string) bool {
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case "live", "failed", "done", "stopped":
		return true
	}
	return false
}

// buildStatus is the pure part of the op: what is live, what ships next,
// and the deploy in flight (if any) read from raw state JSON.
func buildStatus(version string, raw []byte, now time.Time) map[string]any {
	out := map[string]any{
		"version":      version,
		"next_version": nextPatch(version),
	}
	if len(raw) == 0 {
		out["deploy"] = nil
		return out
	}
	var st deployState
	if err := json.Unmarshal(raw, &st); err != nil {
		out["deploy"] = map[string]any{"error": "state file unreadable: " + err.Error()}
		return out
	}
	d := map[string]any{
		"from":    st.Old,
		"to":      st.New,
		"next":    st.Next,
		"title":   st.Title,
		"phase":   st.Phase,
		"result":  st.Result,
		"running": !deployTerminal(st.Phase),
	}
	if t, err := time.Parse(time.RFC3339, st.UpdatedAt); err == nil {
		d["updated_at"] = st.UpdatedAt
		d["updated_ago"] = now.Sub(t).Round(time.Second).String()
	}
	// A deploy that ran to the end and is now live: the next release is the
	// one after it, whatever the script guessed before the swap.
	if st.New != "" && st.New == version && !deployTerminal(st.Phase) {
		d["note"] = "the target version is already serving; the script has not reported its last phase yet"
	}
	out["deploy"] = d
	return out
}

func (h *handlers) buildStatus(c *connector.Ctx) (any, error) {
	start := time.Now()
	user, err := requireUser(c.Context())
	defer func() { logOp(user, "build_status", nil, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	var raw []byte
	if p := deployStatePath(); p != "" {
		if b, rerr := os.ReadFile(p); rerr == nil && len(b) <= 64<<10 {
			raw = b
		}
	}
	return buildStatus(appname.BuildAppVersion, raw, time.Now()), nil
}
