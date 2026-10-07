package source

import (
	"fmt"
	"strings"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/scm"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/pkg/connector"
)

type handlers struct {
	layout agentconfig.Layout
}

// resolveSession picks which session's repos an op looks at: an explicit
// session_id, else the caller's own. Returns the session and its working
// directory together — every op needs both.
func (h *handlers) resolveSession(c *connector.Ctx) (session.Session, string, error) {
	id := strings.TrimSpace(c.Input("session_id"))
	if id == "" {
		id = c.SessionID()
	}
	if id == "" {
		return session.Session{}, "", fmt.Errorf("pass session_id (there is no calling session to infer it from)")
	}
	sess, err := session.Load(h.layout, id)
	if err != nil {
		return session.Session{}, "", fmt.Errorf("load session: %w", err)
	}
	cwd, err := session.Cwd(h.layout, sess)
	if err != nil {
		return session.Session{}, "", fmt.Errorf("resolve session cwd: %w", err)
	}
	return sess, cwd, nil
}

// repoView is the shape every op returns for one repo. Status fields are
// omitted rather than zeroed when git cannot answer — a repo mid-rebase
// still deserves to be listed, and "0 changes" would be a lie.
type repoView struct {
	Rel     string `json:"rel"`
	Name    string `json:"name"`
	Dir     string `json:"dir,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Changed *int   `json:"changed,omitempty"`
	Ahead   *int   `json:"ahead,omitempty"`
	Behind  *int   `json:"behind,omitempty"`
	Active  bool   `json:"active,omitempty"`
}

func (h *handlers) statusInto(c *connector.Ctx, cwd, rel string, v *repoView) {
	dir, err := scm.ResolveRepoDir(cwd, rel)
	if err != nil {
		return
	}
	v.Dir = dir
	st, err := scm.Status(c.Context(), dir)
	if err != nil {
		return
	}
	changed, ahead, behind := len(st.Changes), st.Branch.Ahead, st.Branch.Behind
	v.Branch, v.Changed, v.Ahead, v.Behind = st.Branch.Name, &changed, &ahead, &behind
}

func (h *handlers) active(c *connector.Ctx) (any, error) {
	sess, cwd, err := h.resolveSession(c)
	if err != nil {
		return nil, err
	}
	sel, err := scm.ResolveSelection(cwd, sess.Meta.ScmRepo)
	if err != nil {
		return nil, fmt.Errorf("resolve active repo: %w", err)
	}
	if sel.Rel == "" {
		return map[string]any{
			"cwd":         cwd,
			"repos_total": 0,
			"note":        "no git repository under this session's working directory",
		}, nil
	}
	v := repoView{Rel: sel.Rel, Name: sel.Name, Active: true}
	h.statusInto(c, cwd, sel.Rel, &v)
	out := map[string]any{
		"cwd":         cwd,
		"repo":        v,
		"explicit":    sel.Explicit,
		"repos_total": sel.Total,
	}
	if !sel.Explicit && sel.Total > 1 {
		out["note"] = "nobody has picked a repo in the Source panel — this is the first of " +
			fmt.Sprint(sel.Total) + " found"
	}
	return out, nil
}

func (h *handlers) list(c *connector.Ctx) (any, error) {
	sess, cwd, err := h.resolveSession(c)
	if err != nil {
		return nil, err
	}
	repos, err := scm.DiscoverRepos(cwd)
	if err != nil {
		return nil, fmt.Errorf("list repos: %w", err)
	}
	sel, _ := scm.ResolveSelection(cwd, sess.Meta.ScmRepo)
	out := make([]repoView, 0, len(repos))
	for _, r := range repos {
		v := repoView{Rel: r.Rel, Name: r.Name, Active: r.Rel == sel.Rel}
		h.statusInto(c, cwd, r.Rel, &v)
		out = append(out, v)
	}
	return map[string]any{
		"cwd":    cwd,
		"repos":  out,
		"total":  len(out),
		"active": sel.Rel,
	}, nil
}

// changes lists what is currently modified in a repo. Kept as an op
// rather than a prompt line because the answer has a shelf life of
// seconds — see the op description.
func (h *handlers) changes(c *connector.Ctx) (any, error) {
	sess, cwd, err := h.resolveSession(c)
	if err != nil {
		return nil, err
	}
	rel := strings.TrimSpace(c.Input("repo"))
	if rel == "" {
		sel, serr := scm.ResolveSelection(cwd, sess.Meta.ScmRepo)
		if serr != nil {
			return nil, fmt.Errorf("resolve active repo: %w", serr)
		}
		if sel.Rel == "" {
			return nil, fmt.Errorf("no git repository under this session's working directory")
		}
		rel = sel.Rel
	} else {
		repo, verr := scm.ValidateRepo(cwd, rel)
		if verr != nil {
			return nil, verr
		}
		rel = repo.Rel
	}
	dir, err := scm.ResolveRepoDir(cwd, rel)
	if err != nil {
		return nil, err
	}
	st, err := scm.Status(c.Context(), dir)
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}
	return map[string]any{
		"repo":    rel,
		"dir":     dir,
		"branch":  st.Branch.Name,
		"changes": st.Changes,
		"total":   len(st.Changes),
	}, nil
}

func (h *handlers) selectRepo(c *connector.Ctx) (any, error) {
	sess, cwd, err := h.resolveSession(c)
	if err != nil {
		return nil, err
	}
	rel := strings.TrimSpace(c.Input("repo"))
	if rel != "" {
		repo, verr := scm.ValidateRepo(cwd, rel)
		if verr != nil {
			return nil, verr
		}
		rel = repo.Rel
	}
	meta := sess.Meta
	meta.ScmRepo = rel
	if err := session.SaveMeta(h.layout, sess.ID, meta); err != nil {
		return nil, fmt.Errorf("save session: %w", err)
	}
	sel, err := scm.ResolveSelection(cwd, rel)
	if err != nil {
		return nil, fmt.Errorf("resolve active repo: %w", err)
	}
	v := repoView{Rel: sel.Rel, Name: sel.Name, Active: true}
	h.statusInto(c, cwd, sel.Rel, &v)
	return map[string]any{
		"ok":       true,
		"cwd":      cwd,
		"repo":     v,
		"explicit": sel.Explicit,
	}, nil
}
