package guard

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// helpers

func shellNode(id, cmd string) workflow.Node {
	return workflow.Node{ID: id, Type: workflow.NodeShell, Command: []string{"sh", "-c", cmd}}
}

func httpNode(id, url string) workflow.Node {
	return workflow.Node{ID: id, Type: workflow.NodeHTTP, URL: url}
}

func dbNode(id, sql string, args []string) workflow.Node {
	return workflow.Node{ID: id, Type: workflow.NodeDBQuery, SQL: sql, SQLArgs: args}
}

func agentNode(id, prompt string) workflow.Node {
	return workflow.Node{ID: id, Type: workflow.NodeAgent, Prompt: prompt}
}

func wfWith(nodes ...workflow.Node) workflow.Workflow {
	return workflow.Workflow{Graph: workflow.Graph{Nodes: nodes}}
}

func reviewWith(rules []Rule, w workflow.Workflow) Report {
	g := &Guard{Rules: rules, Config: Config{Mode: ModeWarn}}
	return g.Review(context.Background(), w)
}

// --- DestructiveShellRule ---

func TestDestructiveShell_RmRfRoot(t *testing.T) {
	r := reviewWith([]Rule{&DestructiveShellRule{}}, wfWith(shellNode("n1", "rm -rf /")))
	if r.OK {
		t.Fatal("expected violation for rm -rf /")
	}
	if r.Violations[0].Severity != SevCritical {
		t.Errorf("expected critical, got %s", r.Violations[0].Severity)
	}
}

func TestDestructiveShell_RmRfHome(t *testing.T) {
	r := reviewWith([]Rule{&DestructiveShellRule{}}, wfWith(shellNode("n1", "rm -rf ~")))
	if r.OK {
		t.Fatal("expected violation for rm -rf ~")
	}
}

func TestDestructiveShell_DropDatabase(t *testing.T) {
	r := reviewWith([]Rule{&DestructiveShellRule{}}, wfWith(shellNode("n1", "mysql -e 'DROP DATABASE prod'")))
	if r.OK {
		t.Fatal("expected violation for DROP DATABASE")
	}
}

func TestDestructiveShell_SafeCommand(t *testing.T) {
	r := reviewWith([]Rule{&DestructiveShellRule{}}, wfWith(shellNode("n1", "echo hello world")))
	if !r.OK {
		t.Fatalf("unexpected violation: %+v", r.Violations)
	}
}

func TestDestructiveShell_SkipsNonShellNodes(t *testing.T) {
	r := reviewWith([]Rule{&DestructiveShellRule{}}, wfWith(agentNode("n1", "rm -rf /")))
	if !r.OK {
		t.Fatal("rule should not flag non-shell nodes")
	}
}

// --- PromptInjectionRule ---

func TestPromptInjection_EventPayloadInShell(t *testing.T) {
	r := reviewWith([]Rule{&PromptInjectionRule{}}, wfWith(
		shellNode("n1", "echo {{.Event.Payload.userInput}}"),
	))
	if r.OK {
		t.Fatal("expected violation for Event.Payload interpolation in shell")
	}
	if r.Violations[0].Severity != SevHigh {
		t.Errorf("expected high, got %s", r.Violations[0].Severity)
	}
}

func TestPromptInjection_SafeEventField(t *testing.T) {
	// {{.Event.Type}} is metadata, not user-controlled payload
	r := reviewWith([]Rule{&PromptInjectionRule{}}, wfWith(
		shellNode("n1", "echo {{.Event.Type}}"),
	))
	if !r.OK {
		t.Fatalf("unexpected violation: %+v", r.Violations)
	}
}

func TestPromptInjection_SkipsNonShell(t *testing.T) {
	r := reviewWith([]Rule{&PromptInjectionRule{}}, wfWith(
		agentNode("n1", "{{.Event.Payload.x}}"),
	))
	if !r.OK {
		t.Fatal("rule should not flag non-shell nodes")
	}
}

// --- PlaintextSecretRule ---

func TestPlaintextSecret_HardcodedToken(t *testing.T) {
	r := reviewWith([]Rule{&PlaintextSecretRule{}}, wfWith(
		agentNode("n1", "token: ghp_abc123xyz456abc123xyz456abc123xyz456"),
	))
	if r.OK {
		t.Fatal("expected violation for hardcoded token")
	}
}

func TestPlaintextSecret_PrivateKey(t *testing.T) {
	r := reviewWith([]Rule{&PlaintextSecretRule{}}, wfWith(
		agentNode("n1", "-----BEGIN RSA PRIVATE KEY-----"),
	))
	if r.OK {
		t.Fatal("expected violation for private key header")
	}
}

func TestPlaintextSecret_EncryptedValueAllowed(t *testing.T) {
	// wick_enc_ prefix means the value is already encrypted — should pass
	r := reviewWith([]Rule{&PlaintextSecretRule{}}, wfWith(
		agentNode("n1", "token: wick_enc_abc123xyz456abc123xyz456abc123xyz456"),
	))
	if !r.OK {
		t.Fatalf("wick_enc_ prefix should be allowed, got: %+v", r.Violations)
	}
}

func TestPlaintextSecret_ShortValueIgnored(t *testing.T) {
	// Values < 12 chars don't match — avoids false positives on field names
	r := reviewWith([]Rule{&PlaintextSecretRule{}}, wfWith(
		agentNode("n1", "token: short"),
	))
	if !r.OK {
		t.Fatalf("short value should not be flagged: %+v", r.Violations)
	}
}

// --- UnparameterizedSQLRule ---

func TestSQLUnparameterized_TemplateWithoutArgs(t *testing.T) {
	r := reviewWith([]Rule{&UnparameterizedSQLRule{}}, wfWith(
		dbNode("n1", "SELECT * FROM users WHERE id = {{.Event.Payload.id}}", nil),
	))
	if r.OK {
		t.Fatal("expected violation for unparameterized SQL")
	}
	if r.Violations[0].Severity != SevCritical {
		t.Errorf("expected critical, got %s", r.Violations[0].Severity)
	}
}

func TestSQLUnparameterized_TemplateWithArgs(t *testing.T) {
	// Same template but with sql_args — parameterized, safe
	r := reviewWith([]Rule{&UnparameterizedSQLRule{}}, wfWith(
		dbNode("n1", "SELECT * FROM users WHERE id = $1", []string{"{{.Event.Payload.id}}"}),
	))
	if !r.OK {
		t.Fatalf("parameterized SQL should pass: %+v", r.Violations)
	}
}

func TestSQLUnparameterized_PlainSQL(t *testing.T) {
	r := reviewWith([]Rule{&UnparameterizedSQLRule{}}, wfWith(
		dbNode("n1", "SELECT count(*) FROM events", nil),
	))
	if !r.OK {
		t.Fatalf("plain SQL without template should pass: %+v", r.Violations)
	}
}

func TestSQLUnparameterized_SkipsNonDBNodes(t *testing.T) {
	r := reviewWith([]Rule{&UnparameterizedSQLRule{}}, wfWith(
		shellNode("n1", "SELECT * FROM users WHERE id = {{.x}}"),
	))
	if !r.OK {
		t.Fatal("rule should only flag db_query nodes")
	}
}

// --- NetworkAllowlistRule ---

func TestNetworkAllowlist_BlockedHost(t *testing.T) {
	rule := &NetworkAllowlistRule{AllowedHosts: []string{"internal.company.com"}}
	r := reviewWith([]Rule{rule}, wfWith(
		httpNode("n1", "https://evil.example.com/steal"),
	))
	if r.OK {
		t.Fatal("expected violation for non-allowlisted host")
	}
	if r.Violations[0].Severity != SevMedium {
		t.Errorf("expected medium, got %s", r.Violations[0].Severity)
	}
}

func TestNetworkAllowlist_AllowedHost(t *testing.T) {
	rule := &NetworkAllowlistRule{AllowedHosts: []string{"internal.company.com"}}
	r := reviewWith([]Rule{rule}, wfWith(
		httpNode("n1", "https://internal.company.com/api/data"),
	))
	if !r.OK {
		t.Fatalf("allowlisted host should pass: %+v", r.Violations)
	}
}

func TestNetworkAllowlist_EmptyAllowlist(t *testing.T) {
	// Empty allowlist = no restriction (opt-in feature)
	rule := &NetworkAllowlistRule{AllowedHosts: nil}
	r := reviewWith([]Rule{rule}, wfWith(
		httpNode("n1", "https://anywhere.example.com/api"),
	))
	if !r.OK {
		t.Fatalf("empty allowlist should not restrict: %+v", r.Violations)
	}
}

func TestNetworkAllowlist_SkipsNonHTTPNodes(t *testing.T) {
	rule := &NetworkAllowlistRule{AllowedHosts: []string{"safe.example.com"}}
	r := reviewWith([]Rule{rule}, wfWith(
		shellNode("n1", "curl https://evil.example.com"),
	))
	if !r.OK {
		t.Fatal("rule should only flag http nodes, not shell nodes")
	}
}

// --- Guard.Apply mode enforcement ---

func TestApply_ModeOff_AlwaysPasses(t *testing.T) {
	g := &Guard{Config: Config{Mode: ModeOff}, Rules: []Rule{&DestructiveShellRule{}}}
	report := g.Review(context.Background(), wfWith(shellNode("n1", "rm -rf /")))
	if err := g.Apply(report, nil); err != nil {
		t.Fatalf("ModeOff should never block, got: %v", err)
	}
}

func TestApply_ModeWarn_ViolationAllowed(t *testing.T) {
	g := &Guard{Config: Config{Mode: ModeWarn}, Rules: []Rule{&DestructiveShellRule{}}}
	report := g.Review(context.Background(), wfWith(shellNode("n1", "rm -rf /")))
	if err := g.Apply(report, nil); err != nil {
		t.Fatalf("ModeWarn should not block, got: %v", err)
	}
}

func TestApply_ModeBlock_ViolationBlocked(t *testing.T) {
	g := &Guard{Config: Config{Mode: ModeBlock}, Rules: []Rule{&DestructiveShellRule{}}}
	report := g.Review(context.Background(), wfWith(shellNode("n1", "rm -rf /")))
	if err := g.Apply(report, nil); err == nil {
		t.Fatal("ModeBlock should block on violation")
	}
}

func TestApply_ModeBlock_CleanWorkflowPasses(t *testing.T) {
	g := &Guard{Config: Config{Mode: ModeBlock}, Rules: []Rule{&DestructiveShellRule{}}}
	report := g.Review(context.Background(), wfWith(shellNode("n1", "echo safe")))
	if err := g.Apply(report, nil); err != nil {
		t.Fatalf("ModeBlock should pass clean workflow: %v", err)
	}
}

func TestApply_ModeBlock_OverrideUnblocks(t *testing.T) {
	g := &Guard{Config: Config{Mode: ModeBlock}, Rules: []Rule{&DestructiveShellRule{}}}
	report := g.Review(context.Background(), wfWith(shellNode("n1", "rm -rf /")))
	override := &workflow.Override{Reason: "approved by security team"}
	if err := g.Apply(report, override); err != nil {
		t.Fatalf("override with reason should unblock: %v", err)
	}
}

func TestApply_ModeBlock_EmptyOverrideStillBlocks(t *testing.T) {
	g := &Guard{Config: Config{Mode: ModeBlock}, Rules: []Rule{&DestructiveShellRule{}}}
	report := g.Review(context.Background(), wfWith(shellNode("n1", "rm -rf /")))
	override := &workflow.Override{Reason: ""}
	if err := g.Apply(report, override); err == nil {
		t.Fatal("override with empty reason should not unblock")
	}
}

// --- ContentHash stability ---

func TestContentHash_DeterministicAndDifferent(t *testing.T) {
	w1 := wfWith(shellNode("n1", "echo hello"))
	w2 := wfWith(shellNode("n1", "echo world"))

	h1a := ContentHash(w1)
	h1b := ContentHash(w1)
	h2 := ContentHash(w2)

	if h1a != h1b {
		t.Error("ContentHash must be deterministic")
	}
	if h1a == h2 {
		t.Error("different workflows must produce different hashes")
	}
}

// --- Full guard with default rules ---

func TestNew_DefaultRules_Loaded(t *testing.T) {
	g := New(Config{Mode: ModeWarn})
	if len(g.Rules) == 0 {
		t.Fatal("New() should load default rules")
	}
}

func TestNew_MultipleViolations_AllReported(t *testing.T) {
	w := wfWith(
		shellNode("n1", "rm -rf /"),
		shellNode("n2", "echo {{.Event.Payload.x}}"),
		dbNode("n3", "SELECT * FROM t WHERE id = {{.x}}", nil),
	)
	g := New(Config{Mode: ModeWarn})
	report := g.Review(context.Background(), w)
	if report.OK {
		t.Fatal("expected violations")
	}
	if len(report.Violations) < 3 {
		t.Errorf("expected at least 3 violations, got %d: %+v", len(report.Violations), report.Violations)
	}
}
