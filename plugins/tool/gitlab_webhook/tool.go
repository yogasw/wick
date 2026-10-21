// Command gitlab_webhook receives GitLab webhooks and turns each one into a
// wick event, so a schedule watch with wake_on "gitlab_webhook:<group/repo>"
// runs the moment an MR is merged or a pipeline finishes — no polling.
//
// It is a tool plugin: wick spawns it when a webhook arrives and idle-kills
// it afterwards. It holds no user credentials: events.publish is a
// plugin-level Host API call, and the woken watches run as their owners.
//
// GitLab side: Settings → Webhooks, URL <wick>/tools/gitlab_webhook/webhook/gitlab,
// Secret token = this tool's webhook_secret.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"

	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
	"github.com/yogasw/wick/pkg/plugin/toolplugin"
	"github.com/yogasw/wick/pkg/tool"
)

const Key = "gitlab_webhook"

// Config is the receiver's settings.
type Config struct {
	WebhookSecret string `wick:"secret;required;desc=Secret token GitLab sends in X-Gitlab-Token, put the same value in the GitLab webhook's Secret token field. Empty = every webhook is refused."`
}

func Module() tool.Module {
	return tool.Module{
		Meta: tool.Tool{
			Key:         Key,
			Name:        "GitLab Webhook",
			Description: "Receives GitLab webhooks and wakes the schedule watches subscribed to that project (wake_on gitlab_webhook:<group/repo>).",
			Icon:        "🦊",
			Category:    "Development",
		},
		Configs:  entity.StructToConfigs(Config{}),
		Register: register,
	}
}

// Event is the payload a woken watch's first step reads.
type Event struct {
	Kind         string `json:"kind"`
	Project      string `json:"project"`
	Action       string `json:"action,omitempty"`
	State        string `json:"state,omitempty"`
	Status       string `json:"status,omitempty"`
	MRIID        int    `json:"mr_iid,omitempty"`
	PipelineID   int    `json:"pipeline_id,omitempty"`
	Branch       string `json:"branch,omitempty"`
	SourceBranch string `json:"source_branch,omitempty"`
	TargetBranch string `json:"target_branch,omitempty"`
	Title        string `json:"title,omitempty"`
	URL          string `json:"url,omitempty"`
	User         string `json:"user,omitempty"`
	SHA          string `json:"sha,omitempty"`
	Commits      int    `json:"commits,omitempty"`
	Note         string `json:"note,omitempty"`
}

// ParseEvent reads the fields of a GitLab webhook body a watch filters on.
func ParseEvent(body []byte) (Event, error) {
	var p struct {
		ObjectKind  string `json:"object_kind"`
		EventName   string `json:"event_name"`
		Ref         string `json:"ref"`
		UserName    string `json:"user_username"`
		TotalCount  int    `json:"total_commits_count"`
		CheckoutSHA string `json:"checkout_sha"`
		User        struct {
			Username string `json:"username"`
		} `json:"user"`
		Project struct {
			PathWithNamespace string `json:"path_with_namespace"`
			WebURL            string `json:"web_url"`
		} `json:"project"`
		Attrs struct {
			IID            int    `json:"iid"`
			ID             int    `json:"id"`
			Title          string `json:"title"`
			URL            string `json:"url"`
			Action         string `json:"action"`
			State          string `json:"state"`
			Status         string `json:"status"`
			Ref            string `json:"ref"`
			SHA            string `json:"sha"`
			MergeCommitSHA string `json:"merge_commit_sha"`
			SourceBranch   string `json:"source_branch"`
			TargetBranch   string `json:"target_branch"`
			Note           string `json:"note"`
		} `json:"object_attributes"`
		MergeRequest struct {
			IID          int    `json:"iid"`
			Title        string `json:"title"`
			TargetBranch string `json:"target_branch"`
		} `json:"merge_request"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return Event{}, fmt.Errorf("decode webhook body: %w", err)
	}
	a := p.Attrs
	ev := Event{
		Kind:    firstNonEmpty(p.ObjectKind, p.EventName),
		Project: p.Project.PathWithNamespace,
		User:    firstNonEmpty(p.User.Username, p.UserName),
	}
	switch ev.Kind {
	case "merge_request":
		ev.MRIID, ev.Title, ev.URL = a.IID, a.Title, a.URL
		ev.Action, ev.State = a.Action, a.State
		ev.Branch, ev.SourceBranch, ev.TargetBranch = a.TargetBranch, a.SourceBranch, a.TargetBranch
		ev.SHA = firstNonEmpty(a.MergeCommitSHA, a.SHA)
	case "pipeline":
		ev.PipelineID, ev.Status, ev.Branch, ev.SHA = a.ID, a.Status, a.Ref, a.SHA
		ev.Action = a.Status
		ev.MRIID = p.MergeRequest.IID
		if p.Project.WebURL != "" && a.ID > 0 {
			ev.URL = fmt.Sprintf("%s/-/pipelines/%d", p.Project.WebURL, a.ID)
		}
	case "push", "tag_push":
		ev.Branch = strings.TrimPrefix(strings.TrimPrefix(p.Ref, "refs/heads/"), "refs/tags/")
		ev.Action, ev.SHA, ev.Commits = ev.Kind, p.CheckoutSHA, p.TotalCount
		if p.Project.WebURL != "" && p.CheckoutSHA != "" {
			ev.URL = p.Project.WebURL + "/-/commit/" + p.CheckoutSHA
		}
	case "note":
		ev.Note, ev.URL = truncate(a.Note, 2000), a.URL
		ev.MRIID, ev.Title, ev.Branch = p.MergeRequest.IID, p.MergeRequest.Title, p.MergeRequest.TargetBranch
		ev.Action = "comment"
	default:
		ev.Action = firstNonEmpty(a.Action, a.Status, a.State)
		ev.Title, ev.URL = a.Title, a.URL
	}
	if ev.Project == "" {
		return Event{}, fmt.Errorf("webhook body has no project.path_with_namespace")
	}
	return ev, nil
}

// recent keeps the last deliveries for the status page (this process only).
var recent = struct {
	sync.Mutex
	rows []string
	seen map[string]time.Time
}{seen: map[string]time.Time{}}

func remember(line string) {
	recent.Lock()
	defer recent.Unlock()
	recent.rows = append([]string{time.Now().Format(time.RFC3339) + " " + line}, recent.rows...)
	if len(recent.rows) > 50 {
		recent.rows = recent.rows[:50]
	}
}

// duplicate reports whether GitLab already delivered this event (it retries
// on timeouts); ids are kept for an hour.
func duplicate(id string) bool {
	if id == "" {
		return false
	}
	recent.Lock()
	defer recent.Unlock()
	now := time.Now()
	for k, t := range recent.seen {
		if now.Sub(t) > time.Hour {
			delete(recent.seen, k)
		}
	}
	if _, ok := recent.seen[id]; ok {
		return true
	}
	recent.seen[id] = now
	return false
}

// publish is the Host API call, swappable in tests.
var publish = func(ctx context.Context, topic string, ev Event) (int, error) {
	h, err := toolplugin.Host(ctx)
	if err != nil {
		return 0, err
	}
	args, err := json.Marshal(map[string]any{"topic": topic, "payload": ev})
	if err != nil {
		return 0, err
	}
	resp, err := h.Invoke(ctx, &hostpb.InvokeRequest{Method: "events.publish", ArgsJson: args})
	if err != nil {
		return 0, err
	}
	var out struct {
		Woken int `json:"woken"`
	}
	_ = json.Unmarshal(resp.GetResultJson(), &out)
	return out.Woken, nil
}

func register(r tool.Router) {
	r.GET("/", func(c *tool.Ctx) { c.HTML(page(c)) })
	wh := r.WebhookGroup("/webhook")
	wh.POST("/gitlab", receive)
}

func receive(c *tool.WebhookCtx) {
	secret := c.Cfg("webhook_secret")
	if secret == "" || subtle.ConstantTimeCompare([]byte(c.Header("X-Gitlab-Token")), []byte(secret)) != 1 {
		c.JSON(http.StatusUnauthorized, map[string]string{"error": "bad or missing X-Gitlab-Token"})
		return
	}
	body, err := c.Body()
	if err != nil {
		c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": err.Error()})
		return
	}
	if duplicate(c.Header("X-Gitlab-Event-UUID")) {
		c.JSON(http.StatusOK, map[string]any{"ok": true, "duplicate": true})
		return
	}
	ev, err := ParseEvent(body)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	n, err := publish(c.Context(), ev.Project, ev)
	if err != nil {
		log.Printf("gitlab_webhook: publish %s: %v", ev.Project, err)
		remember(fmt.Sprintf("%s %s %s → error: %v", ev.Kind, ev.Project, ev.Action, err))
		c.JSON(http.StatusBadGateway, map[string]string{"error": "publish to wick failed"})
		return
	}
	remember(fmt.Sprintf("%s %s %s → %d watch(es) woken", ev.Kind, ev.Project, ev.Action, n))
	c.JSON(http.StatusOK, map[string]any{"ok": true, "woken": n})
}

func page(c *tool.Ctx) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		var b strings.Builder
		b.WriteString(`<div class="mx-auto max-w-container px-4 py-6 text-sm text-black-900 dark:text-white-100">`)
		fmt.Fprintf(&b, `<p class="mb-2">GitLab webhook URL: <code>%s/webhook/gitlab</code>, Secret token = this tool's <code>webhook_secret</code>.</p>`, html.EscapeString(c.Base()))
		b.WriteString(`<p class="mb-2">Watch it from a session: <code>wick_schedule_message</code> type=watch, <code>wake_on</code>=<code>gitlab_webhook:&lt;group/repo&gt;</code>, first step a check on the event (e.g. <code>action equals merge</code>, <code>mr_iid equals 12</code>).</p>`)
		if m := c.Missing(); len(m) > 0 {
			fmt.Fprintf(&b, `<p class="mb-2 text-red-500">Missing config: %s</p>`, html.EscapeString(strings.Join(m, ", ")))
		}
		b.WriteString(`<h3 class="mt-4 mb-2 font-semibold">Recent webhooks (this process)</h3><ul class="list-disc pl-5">`)
		recent.Lock()
		for _, row := range recent.rows {
			fmt.Fprintf(&b, `<li>%s</li>`, html.EscapeString(row))
		}
		recent.Unlock()
		b.WriteString(`</ul></div>`)
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
