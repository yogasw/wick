package guard

import (
	"regexp"
	"strings"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// DestructiveShellRule flags rm -rf, dd, mkfs, etc.
type DestructiveShellRule struct{}

var destructivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\brm\s+-rf\s+/`),
	regexp.MustCompile(`(?i)\brm\s+-rf\s+~`),
	regexp.MustCompile(`(?i)\bdd\s+if=.*\bof=/dev/`),
	regexp.MustCompile(`(?i)\bmkfs(\.|\s)`),
	regexp.MustCompile(`(?i)\bshutdown\b`),
	regexp.MustCompile(`(?i)>\s*/dev/sd`),
	regexp.MustCompile(`(?i)\bdrop\s+database\b`),
}

func (r *DestructiveShellRule) Name() string { return "destructive_shell" }
func (r *DestructiveShellRule) Check(w workflow.Workflow) []Violation {
	out := []Violation{}
	for _, n := range w.Graph.Nodes {
		if n.Type != workflow.NodeShell {
			continue
		}
		joined := strings.Join(n.Command, " ")
		for _, p := range destructivePatterns {
			if p.MatchString(joined) {
				out = append(out, Violation{Rule: r.Name(), Node: n.ID, Severity: SevCritical,
					Message: "shell command matches destructive pattern: " + p.String()})
				break
			}
		}
	}
	return out
}

// PromptInjectionRule flags raw `{{.Event.X}}` interpolation into
// shell command args without sanitization.
type PromptInjectionRule struct{}

func (r *PromptInjectionRule) Name() string { return "prompt_injection" }
func (r *PromptInjectionRule) Check(w workflow.Workflow) []Violation {
	out := []Violation{}
	for _, n := range w.Graph.Nodes {
		if n.Type != workflow.NodeShell {
			continue
		}
		for _, arg := range n.Command {
			if strings.Contains(arg, "{{.Event.Payload") {
				out = append(out, Violation{Rule: r.Name(), Node: n.ID, Severity: SevHigh,
					Message: "shell command interpolates untrusted Event data without sanitization"})
				break
			}
		}
	}
	return out
}

// PlaintextSecretRule flags `password:` / `token:` / `api_key:` strings
// in node bodies.
type PlaintextSecretRule struct{}

var plaintextSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(password|secret|api[_-]?key|token)\s*[:=]\s*["']?[A-Za-z0-9_\-/+=]{12,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
}

func (r *PlaintextSecretRule) Name() string { return "plaintext_secret" }
func (r *PlaintextSecretRule) Check(w workflow.Workflow) []Violation {
	out := []Violation{}
	for _, n := range w.Graph.Nodes {
		body := n.Prompt + " " + strings.Join(n.Command, " ") + " " + n.URL + " " + n.Body + " " + n.SQL
		for _, p := range plaintextSecretPatterns {
			if p.MatchString(body) {
				if strings.Contains(body, "wick_enc_") {
					continue
				}
				out = append(out, Violation{Rule: r.Name(), Node: n.ID, Severity: SevHigh,
					Message: "node body looks like it contains a plaintext secret"})
				break
			}
		}
	}
	return out
}

// UnparameterizedSQLRule flags db_query bodies that string-concatenate
// template refs into SQL.
type UnparameterizedSQLRule struct{}

func (r *UnparameterizedSQLRule) Name() string { return "sql_unparameterized" }
func (r *UnparameterizedSQLRule) Check(w workflow.Workflow) []Violation {
	out := []Violation{}
	for _, n := range w.Graph.Nodes {
		if n.Type != workflow.NodeDBQuery {
			continue
		}
		if strings.Contains(n.SQL, "{{") && len(n.SQLArgs) == 0 {
			out = append(out, Violation{Rule: r.Name(), Node: n.ID, Severity: SevCritical,
				Message: "db_query interpolates template into SQL without args — use $1/$2 with sql_args"})
		}
	}
	return out
}

// NetworkAllowlistRule flags HTTP nodes hitting non-allowlisted hosts.
type NetworkAllowlistRule struct {
	AllowedHosts []string
}

func (r *NetworkAllowlistRule) Name() string { return "network_allowlist" }
func (r *NetworkAllowlistRule) Check(w workflow.Workflow) []Violation {
	if len(r.AllowedHosts) == 0 {
		return nil
	}
	out := []Violation{}
	for _, n := range w.Graph.Nodes {
		if n.Type != workflow.NodeHTTP {
			continue
		}
		host := hostFromURLTemplate(n.URL)
		if host == "" {
			continue
		}
		allowed := false
		for _, h := range r.AllowedHosts {
			if strings.Contains(host, h) {
				allowed = true
				break
			}
		}
		if !allowed {
			out = append(out, Violation{Rule: r.Name(), Node: n.ID, Severity: SevMedium,
				Message: "http node targets non-allowlisted host: " + host})
		}
	}
	return out
}

func hostFromURLTemplate(u string) string {
	i := strings.Index(u, "://")
	if i < 0 {
		return ""
	}
	rest := u[i+3:]
	if slash := strings.Index(rest, "/"); slash > 0 {
		rest = rest[:slash]
	}
	return rest
}
