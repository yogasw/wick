package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/entity"
)

func TestTestPanelRendersEmptyForm(t *testing.T) {
	html := renderTestPanel(nil)

	for _, name := range []string{"repo_path", "remote", "branch"} {
		if !strings.Contains(html, `name="`+name+`"`) {
			t.Errorf("form is missing the %q input", name)
		}
	}
	if !strings.Contains(html, `data-op="test_run"`) {
		t.Error("no button wired to the test_run op")
	}
	// Unlike the setup guide, this widget SHOULD call the backend — it has to run
	// git. What it must not do is leave the operator guessing which button writes.
	if !strings.Contains(html, "Nothing is fetched, committed or pushed") {
		t.Error("the form does not say the read check changes nothing")
	}
	if !strings.Contains(html, "throwaway") {
		t.Error("the form does not say the write check works in a throwaway folder")
	}
	// No results section before a run.
	if strings.Contains(html, "All checks passed") || strings.Contains(html, "check(s) failed") {
		t.Error("a summary rendered before anything ran")
	}
}

func TestTestPanelSurvivesAReMount(t *testing.T) {
	// The manager re-runs an html= widget's render op whenever it re-mounts the
	// surrounding form, and the observed sequence was: test_run (200, 730ms) then
	// test_panel twice — the second and third calls rendering a blank form over the
	// operator's input. Nothing in the widget could prevent the re-mount, so the
	// values are persisted as hidden config rows and the render reads them back.
	stored := map[string]string{
		"test_repo":   "https://github.com/org/repo.git",
		"test_remote": "upstream",
		"test_branch": "fix/thing",
	}
	out, err := doTestPanel(opCtx(stored, map[string]string{}))
	if err != nil {
		t.Fatalf("doTestPanel: %v", err)
	}
	html, _ := out.(map[string]any)["html"].(string)

	for _, want := range []string{
		`value="https://github.com/org/repo.git"`,
		`value="upstream"`,
		`value="fix/thing"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("a re-mount lost the operator's input: %q missing", want)
		}
	}
}

func TestTestPanelStartsEmptyWhenNothingWasEverEntered(t *testing.T) {
	// A fresh instance must not show a stale target from somewhere else.
	out, err := doTestPanel(opCtx(map[string]string{}, map[string]string{}))
	if err != nil {
		t.Fatalf("doTestPanel: %v", err)
	}
	html, _ := out.(map[string]any)["html"].(string)
	if !strings.Contains(html, `name="repo_path" placeholder="https://github.com/org/repo.git" value=""`) {
		t.Errorf("a fresh panel did not render an empty repo field:\n%s", html)
	}
}

func TestTestRunPersistsTheFormValues(t *testing.T) {
	requireGit(t)
	out, err := doTestRun(opCtx(baseCfg(), map[string]string{
		"repo_path": "https://github.com/org/repo.git",
		"remote":    "upstream",
		"branch":    "fix/thing",
	}))
	if err != nil {
		t.Fatalf("doTestRun: %v", err)
	}
	m, _ := out.(map[string]any)
	fields, ok := m["fields"].(map[string]string)
	if !ok {
		t.Fatalf("run returned no fields map, so a re-mount would render blank: %v", m["fields"])
	}
	want := map[string]string{
		"test_repo":   "https://github.com/org/repo.git",
		"test_remote": "upstream",
		"test_branch": "fix/thing",
	}
	for k, v := range want {
		if fields[k] != v {
			t.Errorf("fields[%s] = %q, want %q", k, fields[k], v)
		}
	}
}

func TestTestFormFieldsAreDeclaredAndHidden(t *testing.T) {
	// The op writes these keys through {fields}, and the core only applies keys that
	// exist in this connector's schema — an undeclared key is silently dropped, so
	// the input would vanish again with no error anywhere.
	declared := map[string]entity.Config{}
	for _, c := range entity.StructToConfigs(Config{}) {
		declared[c.Key] = c
	}
	for _, key := range []string{"test_repo", "test_remote", "test_branch"} {
		cfg, ok := declared[key]
		if !ok {
			t.Errorf("config key %q is written via {fields} but not declared; the write is dropped", key)
			continue
		}
		// They back a widget, not an operator-facing setting, so they must not clutter
		// the form as editable rows. "hidden" is a flag on the row, not a Type.
		if !cfg.Hidden {
			t.Errorf("config %q is not hidden; it would render as an editable text row", key)
		}
	}
}

func TestTestPanelButtonsAreTypeButton(t *testing.T) {
	// A <button> with no type attribute defaults to type="submit", and this markup
	// renders inside the manager's config form. Without type="button" the click
	// submits the form: the page reloads, every field is wiped, and the op never
	// runs — which looks exactly like "the button does nothing and my input
	// disappeared". Cheap to get wrong, invisible in review, so pin it.
	for _, rep := range []*testReport{nil, {Checks: []testCheck{{Status: "ok", Name: "x"}}}} {
		html := renderTestPanel(rep)
		for _, frag := range strings.Split(html, "<button")[1:] {
			tag := frag[:strings.IndexByte(frag, '>')]
			if !strings.Contains(tag, `type="button"`) {
				t.Errorf("button without type=\"button\" would submit the form: <button%s>", tag)
			}
		}
	}
}

func TestTestPanelPreservesFormValuesAcrossARun(t *testing.T) {
	// The button round-trips, so a failed run must come back with the inputs still
	// filled — otherwise fixing a typo means retyping all three fields.
	rep := &testReport{RepoPath: "/srv/code/api", Remote: "upstream", Branch: "fix/thing"}
	html := renderTestPanel(rep)

	for _, want := range []string{
		`name="repo_path" placeholder="https://github.com/org/repo.git" value="/srv/code/api"`,
		`value="upstream"`,
		`value="fix/thing"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("form did not preserve a value: %q missing", want)
		}
	}
}

func TestTestPanelReportsWorstStatusInTheSummary(t *testing.T) {
	cases := []struct {
		name   string
		checks []testCheck
		want   string
	}{
		{"all good", []testCheck{{Status: "ok"}, {Status: "ok"}}, "All checks passed."},
		{"a warning", []testCheck{{Status: "ok"}, {Status: "warn"}}, "1 warning(s)."},
		{"a failure outranks a warning", []testCheck{{Status: "warn"}, {Status: "fail"}}, "1 check(s) failed."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			html := renderTestPanel(&testReport{Checks: c.checks})
			if !strings.Contains(html, c.want) {
				t.Errorf("summary missing %q", c.want)
			}
		})
	}
}

func TestTestPanelEscapesReportContent(t *testing.T) {
	// git's stderr goes straight into the report, and a repository path is operator
	// input. Both land in HTML.
	rep := &testReport{
		Checks: []testCheck{{
			Name:   `<script>alert(1)</script>`,
			Status: "fail",
			Detail: `" onmouseover="alert(2)`,
			Mono:   `<img src=x onerror=alert(3)>`,
		}},
	}
	html := renderTestPanel(rep)

	// The property that matters is that nothing hostile stays ACTIVE. Correct
	// escaping leaves the characters visible as text — "&lt;img … onerror=alert(3)"
	// is inert because there is no element for the handler to attach to — so
	// asserting on substrings like "onerror=alert" would fail on output that is
	// already safe. Assert on the angle brackets and quotes instead: those are what
	// turn text into markup.
	if strings.Contains(html, "<script") {
		t.Errorf("a live script tag survived:\n%s", html)
	}
	if strings.Contains(html, `<img`) {
		t.Errorf("a live img tag survived:\n%s", html)
	}
	// The attribute-breaking quote must be encoded, or Detail could escape its own
	// element and add a real handler.
	if strings.Contains(html, `" onmouseover="`) {
		t.Errorf("a quote escaped its attribute:\n%s", html)
	}
	// And the hostile text should still be readable, encoded, so the operator can
	// see what git actually said.
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Error("the content was dropped rather than encoded; git's message must stay legible")
	}
}

func TestTestPanelStylesAreScopedAndTailwindFree(t *testing.T) {
	html := renderTestPanel(&testReport{Checks: []testCheck{{Status: "ok", Name: "x"}}})

	// Every class must belong to this widget, or it would restyle the manager page.
	for _, frag := range strings.Split(html, `class="`)[1:] {
		classes := frag[:strings.IndexByte(frag, '"')]
		for _, c := range strings.Fields(classes) {
			if !strings.HasPrefix(c, "wgtt-") {
				t.Errorf("class %q is not scoped to this widget", c)
			}
		}
	}
	if !strings.Contains(html, "var(--color-") {
		t.Error("does not style through theme CSS variables")
	}
	// Its prefix must not collide with the setup guide's, or one widget's rules
	// would restyle the other.
	if strings.Contains(html, "wgid-") {
		t.Error("uses the setup guide's class prefix")
	}
}

func TestTestRunReportsMissingRepoPath(t *testing.T) {
	requireGit(t)
	out, err := doTestRun(opCtx(baseCfg(), map[string]string{"repo_path": ""}))
	if err != nil {
		t.Fatalf("doTestRun: %v", err)
	}
	html, _ := out.(map[string]any)["html"].(string)
	// The message must name BOTH accepted shapes, since either is valid input.
	if !strings.Contains(html, "clone URL") {
		t.Errorf("empty target did not mention a clone URL:\n%s", html)
	}
	if !strings.Contains(html, "checkout") {
		t.Errorf("empty target did not mention a checkout path:\n%s", html)
	}
	if !strings.Contains(html, "check(s) failed") {
		t.Error("empty target was not reported as a failure")
	}
}

func TestTestRunReportsANonRepository(t *testing.T) {
	requireGit(t)
	dir := t.TempDir() // exists, but has no .git

	out, err := doTestRun(opCtx(baseCfg(), map[string]string{"repo_path": dir}))
	if err != nil {
		t.Fatalf("doTestRun: %v", err)
	}
	html, _ := out.(map[string]any)["html"].(string)
	if !strings.Contains(html, ".git") {
		t.Errorf("the failure does not explain what is missing:\n%s", html)
	}
}

func TestTestRunAgainstARealRepository(t *testing.T) {
	requireGit(t)
	repo := initTestRepo(t)
	remote := bareRemote(t, repo)
	gitInTest(t, repo, "push", "--quiet", remote, "HEAD:refs/heads/main")

	out, err := doTestRun(opCtx(baseCfg(), map[string]string{
		"repo_path": repo, "remote": "origin", "branch": "fix/allowed",
	}))
	if err != nil {
		t.Fatalf("doTestRun: %v", err)
	}
	html, _ := out.(map[string]any)["html"].(string)

	// The stages that must have run and passed.
	for _, want := range []string{
		"git binary",
		"Repository",
		"Current branch",
		"Effective URL",
		"Policy in effect",
		"Policy verdict",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("report is missing the %q check:\n%s", want, html)
		}
	}
	// main is the checked-out branch and initTestRepo made it, so it must appear.
	if !strings.Contains(html, "main") {
		t.Errorf("current branch not reported:\n%s", html)
	}
}

func TestTestRunReportsAPolicyDenial(t *testing.T) {
	requireGit(t)
	repo := initTestRepo(t)
	bareRemote(t, repo)

	// baseCfg protects main and requires fix|feat|chore branch names, so asking
	// about main must come back denied with a reason.
	out, err := doTestRun(opCtx(baseCfg(), map[string]string{
		"repo_path": repo, "branch": "main",
	}))
	if err != nil {
		t.Fatalf("doTestRun: %v", err)
	}
	html, _ := out.(map[string]any)["html"].(string)

	if !strings.Contains(html, "protected") {
		t.Errorf("a push to a protected branch was not reported as denied:\n%s", html)
	}
	if !strings.Contains(html, "check(s) failed") {
		t.Error("the denial was not counted as a failure")
	}
}

func TestTestRunSurfacesInvalidPolicyConfig(t *testing.T) {
	requireGit(t)
	repo := initTestRepo(t)

	cfg := baseCfg()
	cfg["branch_name_pattern"] = `^(fix/.+$` // will not compile

	out, err := doTestRun(opCtx(cfg, map[string]string{"repo_path": repo, "branch": "fix/x"}))
	if err != nil {
		t.Fatalf("doTestRun: %v", err)
	}
	html, _ := out.(map[string]any)["html"].(string)

	if !strings.Contains(html, "Policy config") {
		t.Errorf("a policy that does not compile was not reported:\n%s", html)
	}
	// The operator needs to know reads still work while they fix it.
	if !strings.Contains(html, "Read operations still work") {
		t.Error("the report does not say which operations are still available")
	}
}

func TestTestRunNeverLeaksTheToken(t *testing.T) {
	requireGit(t)
	repo := initTestRepo(t)
	bareRemote(t, repo)

	const token = "ghp_supersecrettokenvalue123"
	cfg := baseCfg()
	cfg["username"] = "x-access-token"
	cfg["token"] = token

	out, err := doTestRun(opCtx(cfg, map[string]string{
		"repo_path": repo, "remote": "origin", "branch": "fix/x",
	}))
	if err != nil {
		t.Fatalf("doTestRun: %v", err)
	}
	html, _ := out.(map[string]any)["html"].(string)

	if strings.Contains(html, token) {
		t.Error("the token appears in the rendered report")
	}
	// The base64 form decodes in one step, so it must be masked too.
	if b64 := basicAuthValue("x-access-token", token); strings.Contains(html, b64) {
		t.Error("the base64 credential appears in the rendered report")
	}
}

func TestTestRunIgnoresCredentialsBakedIntoTheRemote(t *testing.T) {
	requireGit(t)
	repo := initTestRepo(t)
	gitInTest(t, repo, "remote", "add", "origin",
		"https://olduser:oldsecret@abc.com/org/repo.git")

	out, err := doTestRun(opCtx(baseCfg(), map[string]string{
		"repo_path": repo, "remote": "origin", "branch": "fix/x",
	}))
	if err != nil {
		t.Fatalf("doTestRun: %v", err)
	}
	html, _ := out.(map[string]any)["html"].(string)

	if strings.Contains(html, "oldsecret") {
		t.Errorf("the report echoed a credential from .git/config:\n%s", html)
	}
	// And it should say out loud that the embedded one is not being used.
	if !strings.Contains(html, "ignored") {
		t.Error("the report does not explain that the embedded credential is ignored")
	}

	// .git/config must be untouched.
	raw, rerr := os.ReadFile(filepath.Join(repo, ".git", "config"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !strings.Contains(string(raw), "oldsecret") {
		t.Error(".git/config was rewritten; the connector must never modify it")
	}
}

func TestTestWriteClonesCommitsAndPushes(t *testing.T) {
	requireGit(t)
	source := initTestRepo(t)
	remote := bareRemote(t, source)
	gitInTest(t, source, "push", "--quiet", remote, "HEAD:refs/heads/main")

	out, err := doTestWrite(opCtx(baseCfg(), map[string]string{
		"repo_path": source, "remote": "origin", "branch": "fix/write-check",
	}))
	if err != nil {
		t.Fatalf("doTestWrite: %v", err)
	}
	html, _ := out.(map[string]any)["html"].(string)

	// Every stage must have run and passed.
	for _, want := range []string{"Clone", "Create branch", "Stage", "Commit", "Push", "Cleanup"} {
		if !strings.Contains(html, want) {
			t.Errorf("report is missing the %q step:\n%s", want, html)
		}
	}
	if strings.Contains(html, "check(s) failed") {
		t.Errorf("the write check failed:\n%s", html)
	}
	if !strings.Contains(html, "Write check") {
		t.Error("the report does not say which check ran")
	}

	// The branch really landed on the remote — that is the whole point.
	refs := gitOutputInTest(t, remote, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if !strings.Contains(refs, "fix/write-check") {
		t.Errorf("remote refs = %q, want the pushed test branch", refs)
	}
}

func TestTestWriteRemovesItsSandbox(t *testing.T) {
	requireGit(t)
	source := initTestRepo(t)
	remote := bareRemote(t, source)
	gitInTest(t, source, "push", "--quiet", remote, "HEAD:refs/heads/main")

	if _, err := doTestWrite(opCtx(baseCfg(), map[string]string{
		"repo_path": source, "remote": "origin", "branch": "fix/cleanup-check",
	})); err != nil {
		t.Fatalf("doTestWrite: %v", err)
	}

	// Nothing may survive. A leftover clone is the only mess this test could make,
	// and it would silently be reused by the next run.
	if _, err := os.Stat(sandboxDir()); !os.IsNotExist(err) {
		t.Errorf("sandbox %s still exists after the run (stat err: %v)", sandboxDir(), err)
	}
}

func TestTestWriteSandboxIsOutsideTheInstallDir(t *testing.T) {
	// Installing the plugin does rm -rf on <appdata>/plugins/connectors/git, so a
	// sandbox in there would be destroyed on every rebuild — and a half-finished
	// unzip could leave the install broken. DataDir sits alongside connectors/ for
	// this reason; keep the sandbox there.
	dir := filepath.ToSlash(sandboxDir())
	if strings.Contains(dir, "/connectors/") {
		t.Errorf("sandbox %s is inside the install dir, which is wiped on every install", dir)
	}
	if !strings.HasSuffix(dir, "/test") {
		t.Errorf("sandbox %s does not end in /test", dir)
	}
}

func TestTestWriteRefusesWhenThePolicyDenies(t *testing.T) {
	requireGit(t)
	source := initTestRepo(t)
	remote := bareRemote(t, source)
	gitInTest(t, source, "push", "--quiet", remote, "HEAD:refs/heads/main")

	// A test button that could push to a protected branch would be a hole straight
	// through the policy engine.
	out, err := doTestWrite(opCtx(baseCfg(), map[string]string{
		"repo_path": source, "remote": "origin", "branch": "main",
	}))
	if err != nil {
		t.Fatalf("doTestWrite: %v", err)
	}
	html, _ := out.(map[string]any)["html"].(string)

	if !strings.Contains(html, "check(s) failed") {
		t.Errorf("pushing to a protected branch was not refused:\n%s", html)
	}
	if !strings.Contains(html, "protected") {
		t.Errorf("the refusal does not name the reason:\n%s", html)
	}
	// Nothing may have been created or cloned.
	// "Clone" alone would also match "Clone source", the resolve step that runs
	// before the policy check by design. Assert on the command itself.
	if strings.Contains(html, "git clone") {
		t.Error("the clone ran despite the policy denial")
	}
	if _, err := os.Stat(sandboxDir()); !os.IsNotExist(err) {
		t.Error("a sandbox was created for a run the policy denied")
	}
}

func TestTestWriteRefusesABranchNameViolatingThePattern(t *testing.T) {
	requireGit(t)
	source := initTestRepo(t)
	bareRemote(t, source)

	out, err := doTestWrite(opCtx(baseCfg(), map[string]string{
		"repo_path": source, "remote": "origin", "branch": "temp-hack",
	}))
	if err != nil {
		t.Fatalf("doTestWrite: %v", err)
	}
	html, _ := out.(map[string]any)["html"].(string)
	if !strings.Contains(html, "pattern") {
		t.Errorf("a branch violating the pattern was not refused:\n%s", html)
	}
}

func TestTestWriteRequiresABranchName(t *testing.T) {
	requireGit(t)
	source := initTestRepo(t)
	bareRemote(t, source)

	out, err := doTestWrite(opCtx(baseCfg(), map[string]string{
		"repo_path": source, "remote": "origin", "branch": "",
	}))
	if err != nil {
		t.Fatalf("doTestWrite: %v", err)
	}
	html, _ := out.(map[string]any)["html"].(string)
	// Inventing a name would probably violate the operator's own pattern, so it
	// asks instead.
	if !strings.Contains(html, "Enter a branch name") {
		t.Errorf("no prompt for the missing branch name:\n%s", html)
	}
}

func TestTestWriteLeavesTheSourceRepositoryUntouched(t *testing.T) {
	requireGit(t)
	source := initTestRepo(t)
	remote := bareRemote(t, source)
	gitInTest(t, source, "push", "--quiet", remote, "HEAD:refs/heads/main")

	before := gitOutputInTest(t, source, "status", "--porcelain=v2", "--branch")
	beforeBranches := gitOutputInTest(t, source, "for-each-ref", "--format=%(refname:short)", "refs/heads")

	if _, err := doTestWrite(opCtx(baseCfg(), map[string]string{
		"repo_path": source, "remote": "origin", "branch": "fix/untouched",
	})); err != nil {
		t.Fatalf("doTestWrite: %v", err)
	}

	// The write test clones; it must not commit into, or branch in, the repository
	// the operator pointed it at.
	if after := gitOutputInTest(t, source, "status", "--porcelain=v2", "--branch"); after != before {
		t.Errorf("source repository status changed:\nbefore %q\nafter  %q", before, after)
	}
	if after := gitOutputInTest(t, source, "for-each-ref", "--format=%(refname:short)", "refs/heads"); after != beforeBranches {
		t.Errorf("source repository branches changed:\nbefore %q\nafter  %q", beforeBranches, after)
	}
}

func TestTestPanelOpsAreConfigOnly(t *testing.T) {
	// These run git with the instance's credential. An agent must never be able to
	// call them, and they must not count toward the agent-visible tool surface.
	want := map[string]bool{"test_panel": true, "test_run": true, "test_write": true}
	seen := map[string]bool{}
	for _, cat := range Operations() {
		for _, op := range cat.Ops {
			if want[op.Key] {
				seen[op.Key] = true
				if !op.ConfigOnly {
					t.Errorf("op %q must be declared with OpConfigOnly", op.Key)
				}
			}
		}
	}
	for k := range want {
		if !seen[k] {
			t.Errorf("op %q is not registered", k)
		}
	}
}

func TestTestPanelFieldIsWiredToItsOp(t *testing.T) {
	var tag string
	for _, cfg := range entity.StructToConfigs(Config{}) {
		if cfg.Key == "test_panel" {
			tag = cfg.Options
		}
	}
	if tag != "test_panel" {
		t.Errorf("test_panel field points at op %q, want test_panel", tag)
	}
}

func TestTestPanelExposesOnlyItsOwnTwoChecks(t *testing.T) {
	// The panel writes — but only inside its own sandbox, and only through the two
	// check ops. It must never wire a button straight to a real operation: those
	// stay behind the agent surface where each destructive one is opted into per
	// instance, and a config page is exactly where someone clicks to see what a
	// button does.
	html := renderTestPanel(&testReport{
		RepoPath: "/srv/code/api",
		Checks:   []testCheck{{Status: "ok", Name: "x"}},
	})

	for _, op := range []string{"push", "commit", "merge", "reset", "rebase", "clone", "raw", "stash_drop", "tag_delete"} {
		if strings.Contains(html, `data-op="`+op+`"`) {
			t.Errorf("the panel wires a button straight to the %q operation", op)
		}
	}

	if n := strings.Count(html, "data-op="); n != 2 {
		t.Errorf("panel has %d actions, want exactly 2 (read check, write check)", n)
	}
	for _, want := range []string{`data-op="test_run"`, `data-op="test_write"`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing action %s", want)
		}
	}

	// The write button has to say what it does before it is clicked.
	for _, want := range []string{"throwaway", "deletes the folder", "pass the policy"} {
		if !strings.Contains(html, want) {
			t.Errorf("the write check is not explained: %q missing", want)
		}
	}
}

func TestTestRunJudgesTheCommitMessage(t *testing.T) {
	requireGit(t)
	repo := initTestRepo(t)
	bareRemote(t, repo)

	cfg := baseCfg()
	cfg["commit_message_pattern"] = `^(feat|fix|chore)(\(.+\))?: .+`

	run := func(msg string) string {
		out, err := doTestRun(opCtx(cfg, map[string]string{
			"repo_path": repo, "remote": "origin", "branch": "fix/x", "message": msg,
		}))
		if err != nil {
			t.Fatalf("doTestRun(%q): %v", msg, err)
		}
		html, _ := out.(map[string]any)["html"].(string)
		return html
	}

	// This is the regression: the input was declared and rendered but never read, so
	// every message — conforming or not — reported "no message was entered".
	if html := run("fix: something real"); !strings.Contains(html, "Accepted") {
		t.Errorf("a conforming message was not accepted:\n%s", html)
	}
	if html := run("wip"); !strings.Contains(html, "does not match the required pattern") {
		t.Errorf("a non-conforming message was not refused:\n%s", html)
	}
	// No message, but a rule exists: say so rather than implying it passed.
	if html := run(""); !strings.Contains(html, "no message was entered") {
		t.Errorf("an unchecked rule was not flagged:\n%s", html)
	}
}

func TestTestRunSaysNothingAboutCommitsWhenNoRuleExists(t *testing.T) {
	requireGit(t)
	repo := initTestRepo(t)
	bareRemote(t, repo)

	// With no rule configured, an empty message must not add a line to the report —
	// an unused optional field should be invisible.
	out, err := doTestRun(opCtx(baseCfg(), map[string]string{
		"repo_path": repo, "remote": "origin", "branch": "fix/x",
	}))
	if err != nil {
		t.Fatalf("doTestRun: %v", err)
	}
	html, _ := out.(map[string]any)["html"].(string)
	if strings.Contains(html, "no message was entered") {
		t.Error("reported an unchecked commit rule when none is configured")
	}
}
