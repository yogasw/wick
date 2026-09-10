// Package agents — routing the Source panel's network git through a Git
// CLI connector.
//
// The panel used to shell out to plain `git` for push and pull, with no
// credential helper: against a private HTTPS remote that always fails,
// so the buttons were decorative for exactly the repos people care
// about. The credentials already exist — in the Git CLI connector, which
// is how the agent pushes — along with its policy (protected branches,
// no force push) and its per-run audit trail.
//
// So the panel borrows the connector. Which one is NEVER guessed
// silently: a push under an identity nobody chose is the failure mode
// worth avoiding, so the first push in a repo asks, and the answer is
// remembered on the session per repo.

package agents

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/yogasw/wick/internal/agents/scm"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// gitConnectorKey is the connector module key the Git CLI plugin
// registers under.
const gitConnectorKey = "git"

// gitChoiceUserID identifies whose choice is being read or written.
// Empty for the App Owner, matching how the rest of the per-user state
// in this package is keyed.
func gitChoiceUserID(c *tool.Ctx) string {
	u := login.GetUser(c.Context())
	if u == nil || u.IsOwner {
		return ""
	}
	return u.ID
}

// gitConnKey is how a choice is filed: by USER, inside the session.
//
// Per session, not per repo: a session's repos are one body of work and
// answering the same question for each of 58 checkouts is a chore
// nobody asked for. Per user because a session can be shared — a Slack
// thread is open to everyone in the channel — and a credential is the
// one thing that must never be inherited from whoever pushed first.
func gitConnKey(userID string) string { return userID }

// gitNativeChoice is the stored value for "run plain git, deliberately".
// Distinct from an empty string, which means nobody has decided yet: one
// pushes with the machine's own credentials because that is what the
// user asked for, the other must stop and ask.
const gitNativeChoice = "native"

// gitConnectorVM is one row of the picker.
type gitConnectorVM struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Author string `json:"author,omitempty"`
	// Suggested marks the instance whose label names the remote's host.
	// A guess, and labelled as one in the UI: the connector stores a
	// username and a token, not a host, so nothing here can prove which
	// credential belongs to which server.
	Suggested bool `json:"suggested,omitempty"`
}

// gitConnectorsResponse backs GET /git/connectors.
type gitConnectorsResponse struct {
	Repo       string           `json:"repo"`
	RemoteURL  string           `json:"remote_url,omitempty"`
	RemoteHost string           `json:"remote_host,omitempty"`
	Selected   string           `json:"selected,omitempty"`
	Candidates []gitConnectorVM `json:"candidates"`
}

// gitConnectors lists the Git CLI instances this caller may use for a
// repo, plus the one already remembered for it.
func gitConnectors(c *tool.Ctx) {
	sess, cwd, ok := sessionAndCwd(c)
	if !ok {
		return
	}
	rel := strings.TrimSpace(c.Query("repo"))
	if rel == "" {
		sel, err := scm.ResolveSelection(cwd, sess.Meta.ScmRepo)
		if err == nil {
			rel = sel.Rel
		}
	}
	out := gitConnectorsResponse{Repo: rel, Candidates: []gitConnectorVM{}}
	if dir, err := scm.ResolveRepoDir(cwd, rel); err == nil {
		url, _ := scm.RemoteURL(c.Context(), dir, "origin")
		out.RemoteURL, out.RemoteHost = scrubRemoteURL(url), scm.RemoteHost(url)
	}
	out.Candidates = gitConnectorCandidates(c, out.RemoteHost)
	out.Selected = sess.Meta.ScmGitConnectors[gitConnKey(gitChoiceUserID(c))]
	// A remembered instance that has since been deleted or revoked must
	// not be reported as the current choice — it would push nowhere and
	// the panel would keep claiming it is set up. The native choice has
	// no instance to disappear.
	if out.Selected != "" && out.Selected != gitNativeChoice && !containsConnector(out.Candidates, out.Selected) {
		out.Selected = ""
	}
	c.JSON(http.StatusOK, out)
}

// setGitConnector remembers (or clears, with an empty id) which Git CLI
// instance a repo pushes through.
func setGitConnector(c *tool.Ctx) {
	sess, cwd, ok := sessionAndCwd(c)
	if !ok {
		return
	}
	var body struct {
		Repo        string `json:"repo"`
		ConnectorID string `json:"connector_id"`
	}
	if err := json.NewDecoder(c.R.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	rel := strings.TrimSpace(body.Repo)
	if rel == "" {
		sel, err := scm.ResolveSelection(cwd, sess.Meta.ScmRepo)
		if err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		rel = sel.Rel
	}
	id := strings.TrimSpace(body.ConnectorID)
	if id != "" && id != gitNativeChoice && !containsConnector(gitConnectorCandidates(c, ""), id) {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "unknown or inaccessible git connector"})
		return
	}
	meta := sess.Meta
	if meta.ScmGitConnectors == nil {
		meta.ScmGitConnectors = map[string]string{}
	}
	key := gitConnKey(gitChoiceUserID(c))
	if id == "" {
		delete(meta.ScmGitConnectors, key)
	} else {
		meta.ScmGitConnectors[key] = id
	}
	if err := session.SaveMeta(globalLayout, sess.ID, meta); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	globalMgr.Register(sessionWithMeta(sess, meta))
	c.JSON(http.StatusOK, map[string]any{"ok": true, "repo": rel, "connector_id": id})
}

// gitConnectorCandidates returns the Git CLI instances the caller can
// see, newest label order as the manager lists them. Access is the same
// tag filter the connector manager applies — the panel must not hand out
// a credential the user cannot otherwise reach.
func gitConnectorCandidates(c *tool.Ctx, remoteHost string) []gitConnectorVM {
	out := []gitConnectorVM{}
	if globalConnectors == nil {
		return out
	}
	user := login.GetUser(c.Context())
	if user == nil {
		return out
	}
	var tagIDs []string
	if globalAuth != nil {
		tagIDs = globalAuth.GetUserFilterTagIDs(c.Context(), user.ID)
	}
	rows, err := globalConnectors.ListForManager(c.Context(), tagIDs, user.IsAdmin())
	if err != nil {
		return out
	}
	host := strings.ToLower(strings.TrimSpace(remoteHost))
	for _, row := range rows {
		if row.Key != gitConnectorKey || row.Disabled {
			continue
		}
		if globalConnectors.Status(row) != "ready" {
			continue
		}
		vm := gitConnectorVM{ID: row.ID, Label: row.Label}
		cfg := globalConnectors.LoadConfigs(row)
		vm.Author = strings.TrimSpace(cfg["author_name"])
		if host != "" && strings.Contains(strings.ToLower(row.Label), host) {
			vm.Suggested = true
		}
		out = append(out, vm)
	}
	return out
}

func containsConnector(rows []gitConnectorVM, id string) bool {
	for _, r := range rows {
		if r.ID == id {
			return true
		}
	}
	return false
}

// scrubRemoteURL removes any credentials embedded in a remote URL before
// it is shown. A URL of the form https://user:token@host/… is not rare
// in a machine's checkout, and the panel has no business displaying the
// token back.
func scrubRemoteURL(raw string) string {
	i := strings.Index(raw, "://")
	if i < 0 {
		return raw
	}
	rest := raw[i+3:]
	at := strings.Index(rest, "@")
	if at < 0 {
		return raw
	}
	return raw[:i+3] + rest[at+1:]
}

// runGitConnectorOp executes one op of a Git CLI instance on behalf of
// the signed-in user, so the run row records who asked. Returns the
// connector's own output, or its refusal — a policy denial is an answer,
// not a transport failure, and must reach the user verbatim.
func runGitConnectorOp(c *tool.Ctx, connectorID, op string, input map[string]string) (string, error) {
	if globalConnectors == nil {
		return "", fmt.Errorf("connectors not initialised")
	}
	user := login.GetUser(c.Context())
	if user == nil {
		return "", fmt.Errorf("not signed in")
	}
	res, err := globalConnectors.Execute(c.Context(), connectors.ExecuteParams{
		ConnectorID:  connectorID,
		OperationKey: op,
		Input:        input,
		Source:       entity.ConnectorRunSourceApp,
		UserID:       user.ID,
		IsAdmin:      user.IsAdmin(),
	})
	if err != nil {
		return "", err
	}
	if res == nil {
		return "", fmt.Errorf("git %s: empty response", op)
	}
	if res.ErrorMessage != "" {
		return "", fmt.Errorf("%s", res.ErrorMessage)
	}
	return gitConnectorOutput(res.ResponseJSON)
}

// gitConnectorOutput turns the connector's JSON reply into either the
// terminal output or an error.
//
// A REFUSAL IS NOT AN ERROR TO THE TRANSPORT: the connector answers
// normally with ok:false and a policy verdict of "deny" — git never
// ran — so a caller that only checks the transport error reports a
// blocked pull as a success. The panel did exactly that, showing
// "Pulled" while the branch stayed 65 commits behind.
func gitConnectorOutput(raw string) (string, error) {
	var payload struct {
		OK       bool   `json:"ok"`
		ExitCode int    `json:"exit_code"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		Policy   struct {
			Verdict  string `json:"verdict"`
			Reason   string `json:"reason"`
			NextStep string `json:"next_step"`
		} `json:"policy"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return raw, nil
	}
	out := strings.TrimSpace(payload.Stdout + "\n" + payload.Stderr)
	if payload.Policy.Verdict == "deny" {
		reason := payload.Policy.Reason
		if reason == "" {
			reason = "refused by the connector's policy"
		}
		return "", fmt.Errorf("%s", reason)
	}
	if !payload.OK {
		if out == "" {
			out = fmt.Sprintf("git exited %d", payload.ExitCode)
		}
		return "", fmt.Errorf("%s", out)
	}
	if out == "" {
		return "done", nil
	}
	return out, nil
}

// resolveGitConnector picks the instance a push/pull runs through: the
// explicit one from the request, else this user's remembered choice for
// the session. Empty means plain git — either because the user chose it
// (the native option) or because there was nothing to choose from.
func resolveGitConnector(c *tool.Ctx, sess session.Session, requested string) (string, error) {
	id := strings.TrimSpace(requested)
	if id == "" {
		id = sess.Meta.ScmGitConnectors[gitConnKey(gitChoiceUserID(c))]
	}
	if id == "" || id == gitNativeChoice {
		return "", nil
	}
	// The id travels in a request body, and a stored one can outlive the
	// access that put it there — a colleague's pick, or a connector whose
	// tag was revoked. Either way it is refused rather than run.
	if !containsConnector(gitConnectorCandidates(c, ""), id) {
		return "", fmt.Errorf("that git connector is not available to you — pick another in the Source panel")
	}
	return id, nil
}
