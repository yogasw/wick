package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

func testPolicy(t *testing.T, root string) Policy {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"version": 1,
		"filesystem": map[string]any{
			"default": "deny",
			"protected_patterns": []string{"**/.env", "**/*.pem"},
			"rules": []any{
				map[string]any{"path": root, "allow": []string{"*"}},
				map[string]any{"path": filepath.Join(root,"private"), "deny": []string{"*"}},
				map[string]any{"path": filepath.Join(root,"review"), "approval": []string{"delete"}},
			},
		},
		"terminal": map[string]any{"mode":"whitelist","allow":[]string{"git","go"},"deny":[]string{"git push --force"},"approval":[]string{"git push"},"trust_host":true},
		"system": map[string]any{"allow":[]string{"disk_usage"}},
	})
	if err != nil { t.Fatal(err) }
	p, err := loadPolicy(string(raw))
	if err != nil { t.Fatal(err) }
	return p
}

func TestDefaultDeny(t *testing.T) {
	p, err := loadPolicy("")
	if err != nil { t.Fatal(err) }
	if verdict, _ := p.fileVerdict("read", t.TempDir()); verdict != "deny" { t.Fatal(verdict) }
	if verdict, _ := p.commandVerdict("git", []string{"status"}, false); verdict != "deny" { t.Fatal(verdict) }
}

func TestPathRulePrecedence(t *testing.T) {
	root := t.TempDir()
	p := testPolicy(t, root)
	for _, tc := range []struct{ op, path, want string }{
		{"read", filepath.Join(root,"file.go"), "allow"},
		{"read", filepath.Join(root,"private","file.go"), "deny"},
		{"read", filepath.Join(root,"nested",".env"), "deny"},
		{"read", filepath.Join(root,"nested","secret.pem"), "deny"},
		{"delete", filepath.Join(root,"review","file.go"), "approval"},
		{"read", root+"-elsewhere/file.go", "deny"},
	} {
		if got,_ := p.fileVerdict(tc.op,tc.path); got!=tc.want { t.Errorf("%s %s: got %s, want %s",tc.op,tc.path,got,tc.want) }
	}
}

func TestCommandRulePrecedence(t *testing.T) {
	p := testPolicy(t, t.TempDir())
	for _, tc := range []struct{ cmd string; args []string; want string }{
		{"git",[]string{"status"},"allow"},
		{"git",[]string{"push","--force"},"deny"},
		{"git",[]string{"push","origin"},"approval"},
		{"bash",[]string{"-c","echo x"},"deny"},
	} {
		if got,_:=p.commandVerdict(tc.cmd,tc.args,false);got!=tc.want { t.Errorf("%v: got %s want %s",tc,got,tc.want) }
	}
	p.Terminal.TrustHost=false
	if got,_:=p.commandVerdict("git",nil,false);got!="deny"{t.Fatal("untrusted terminal allowed")}
}

func TestInvalidPolicy(t *testing.T) {
	for _, raw := range []string{
		"{", defaultPolicy+"{}", strings.Replace(defaultPolicy,"\"version\":1","\"version\":2",1),
		strings.Replace(defaultPolicy,"\"mode\":\"disabled\"","\"mode\":\"typo\"",1),
		strings.Replace(defaultPolicy,"\"version\":1","\"version\":1,\"unknown\":true",1),
	} {
		if _,err:=loadPolicy(raw);err==nil{t.Errorf("accepted %q",raw)}
	}
}

func TestRejectTraversal(t *testing.T) {
	root:=t.TempDir()
	for _, name := range []string{"relative",root+string(os.PathSeparator)+".."+string(os.PathSeparator)+"secret","~/../secret"} {
		if _,err:=absolutePath(name);err==nil{t.Errorf("accepted %q",name)}
	}
}

func TestModuleSingleInstanceAndDestructiveDefaults(t *testing.T) {
	m:=Module()
	if !m.Meta.Fixed||m.AllowSessionConfig||m.DefaultAccess.AllowOthersConfigure{t.Fatal("unsafe module access defaults")}
	for _, op:=range m.AllOps(){
		switch op.Key{
		case "exec","exec_shell","file_write","file_delete","file_move","file_create","file_edit":
			if !op.Destructive{t.Errorf("%s must default disabled",op.Key)}
		case "policy_overview":
			if !op.ConfigOnly{t.Fatal("overview exposed to MCP")}
		case "policy_update","policy_patch":
			t.Fatal("policy self-modification exposed")
		}
	}
}

func TestOverviewEscapesPolicy(t *testing.T) {
	p:=testPolicy(t,t.TempDir())
	p.Filesystem.Rules[0].Path=filepath.Join(p.Filesystem.Rules[0].Path,"<script>alert(1)</script>")
	raw,_:=json.Marshal(p)
	c:=connector.NewPluginCtx(t.Context(),map[string]string{"policy_json":string(raw)},nil)
	out,err:=overview(c)
	if err!=nil{t.Fatal(err)}
	body:=out.(map[string]string)["html"]
	if strings.Contains(body,"<script>")||!strings.Contains(body,"&lt;script&gt;"){t.Fatal("policy widget did not escape HTML")}
}
