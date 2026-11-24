package main

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
	"github.com/yogasw/wick/plugins/tags"
)

type Config struct {
	PolicyJSON string `wick:"key=policy_json;textarea;session_locked;desc=Versioned JSON policy. Empty denies all operations. Only trusted administrators may configure this instance."`
	Overview string `wick:"html=policy_overview;desc=Read-only summary of the saved JSON policy."`
}

type pathInput struct {
	Path string `wick:"required;desc=Absolute path or ~/ path."`
}
type readInput struct {
	Path string `wick:"required;desc=Absolute file path."`
	StartLine int `wick:"desc=First line (1-based, default 1)."`
	EndLine int `wick:"desc=Last inclusive line, max 1000 lines per read."`
}
type writeInput struct {
	Path string `wick:"required;desc=Absolute file path."`
	Content string `wick:"textarea;desc=New text content."`
	ExpectedSHA256 string `wick:"key=expected_sha256;required;desc=SHA256 returned by file_read."`
}
type createInput struct {
	Path string `wick:"required;desc=New file path; existing files refused."`
	Content string `wick:"textarea;desc=Text content."`
}
type editInput struct {
	Path string `wick:"required;desc=Absolute file path."`
	OldText string `wick:"required;textarea;desc=Exact unique text to replace."`
	NewText string `wick:"textarea;desc=Replacement text."`
	ExpectedSHA256 string `wick:"key=expected_sha256;required;desc=SHA256 from file_read."`
}
type copyInput struct {
	Path string `wick:"required;desc=Source file."`
	Destination string `wick:"required;desc=New destination file; no overwrite."`
}
type moveInput struct {
	Path string `wick:"required;desc=Source file."`
	Destination string `wick:"required;desc=New destination file; no overwrite."`
	ExpectedSHA256 string `wick:"key=expected_sha256;required;desc=Source SHA256."`
}
type listInput struct {
	Path string `wick:"required;desc=Directory path."`
	Limit int `wick:"desc=Maximum entries, up to 500."`
}
type searchInput struct {
	Path string `wick:"required;desc=Directory to search."`
	Pattern string `wick:"desc=Filename glob, default *."`
	Query string `wick:"desc=Literal text for content search."`
	MaxDepth int `wick:"desc=Maximum recursion depth, default 8, max 20."`
	Limit int `wick:"desc=Maximum results, default 200."`
}
type execInput struct {
	Command string `wick:"required;desc=Executable name or absolute path."`
	ArgsJSON string `wick:"key=args_json;textarea;desc=JSON array of literal argument strings; no shell expansion."`
	Cwd string `wick:"required;desc=Absolute allowed working directory."`
	TimeoutSeconds int `wick:"desc=Optional timeout, capped by policy."`
}
type shellInput struct {
	Script string `wick:"required;textarea;desc=Script to execute in trusted unrestricted shell mode."`
	Cwd string `wick:"required;desc=Absolute allowed working directory."`
	TimeoutSeconds int `wick:"desc=Optional timeout, capped by policy."`
}
type validateInput struct {
	PolicyJSON string `wick:"key=policy_json;required;textarea;desc=Proposed policy JSON. Validation never changes settings."`
}
type overviewInput struct {
	Browser string `wick:"desc=Current widget value; ignored."`
}

type checkInput struct {
	Operation string `wick:"required;desc=Filesystem operation or exec/exec_shell."`
	Path string `wick:"desc=Path for filesystem checks."`
	Command string `wick:"desc=Command for terminal checks."`
	ArgsJSON string `wick:"key=args_json;textarea;desc=JSON array of arguments."`
}

func Module() connector.Module {
	d := wickdocs.Docs{}
	return connector.Module{
		Meta: connector.Meta{
			Key: "host_access", Name: "Host Access", Icon: "🖥️", Fixed: true,
			Description: "Optional host filesystem and terminal access. Default deny. Terminal is explicitly trusted host execution, not a sandbox.",
			DefaultTags: []entity.DefaultTag{tags.Connector, tags.System},
		},
		Configs: entity.StructToConfigs(Config{}),
		ValidateConfig: func(key, value string) error {
			if key != "policy_json" { return nil }
			_, err := loadPolicy(value)
			return err
		},
		Operations: []connector.Category{
			connector.Cat("Policy", "Inspect and simulate the saved policy. Changes must be made by an administrator in settings.",
				connector.Op("policy_get", "Get policy", "Return current JSON policy; does not change permissions.", struct{}{}, getPolicy, d),
				connector.Op("policy_validate", "Validate policy", "Check proposed JSON without saving or granting access.", validateInput{}, validatePolicy, d),
				connector.Op("policy_check", "Check policy", "Simulate one path or command rule; no operation executed. Does not guarantee runtime access.", checkInput{}, checkPolicy, d),
				connector.OpConfigOnly("policy_overview", "Policy overview", "Render saved policy summary.", overviewInput{}, overview, d),
			),
			connector.Cat("Files", "Bounded file operations with policy checks and no symlink following.",
				connector.Op("file_read", "Read file", "Read UTF-8 text with line range and SHA256.", readInput{}, fileAction("read", fileRead), d),
				connector.Op("file_stat", "File stat", "Return file or directory metadata.", pathInput{}, fileAction("stat", func(_ Policy, name string, _ *connector.Ctx)(any,error){return statFile(name)}), d),
				connector.OpDestructive("file_create", "Create file", "Create a new file without overwriting.", createInput{}, fileAction("create", fileCreate), d),
				connector.OpDestructive("file_write", "Write file", "Replace existing file using expected SHA256.", writeInput{}, fileAction("write", fileWrite), d),
				connector.OpDestructive("file_edit", "Edit file", "Replace one exact text match using expected SHA256.", editInput{}, fileAction("edit", fileEdit), d),
				connector.OpDestructive("file_append", "Append file", "Append content using expected SHA256.", writeInput{}, fileAction("write", fileAppend), d),
				connector.OpDestructive("file_copy", "Copy file", "Copy a bounded regular file to a new destination.", copyInput{}, fileAction("copy", func(p Policy,n string,c *connector.Ctx)(any,error){return fileCopy(p,n,c)}), d),
				connector.OpDestructive("file_move", "Move file", "Move a regular file atomically without overwriting a destination; expected SHA256 required.", moveInput{}, fileAction("move", fileMove), d),
				connector.OpDestructive("file_delete", "Delete file", "Delete one regular file. No recursive deletion.", pathInput{}, fileAction("delete", func(_ Policy,n string,_ *connector.Ctx)(any,error){return map[string]bool{"deleted":true},removeFile(n,false)}), d),
			),
			connector.Cat("Directories & search", "List and search only visible paths, bounded by depth and result count.",
				connector.Op("directory_list", "List directory", "List permitted entries.", listInput{}, fileAction("list",fileList), d),
				connector.Op("directory_tree", "Directory tree", "Bounded recursive directory tree.", searchInput{}, fileAction("search",func(p Policy,n string,c *connector.Ctx)(any,error){return search(p,n,c,false,true)}),d),
				connector.OpDestructive("directory_create","Create directory","Create one directory; parent must exist.",pathInput{},fileAction("mkdir",func(_ Policy,n string,_ *connector.Ctx)(any,error){return map[string]bool{"created":true},mkdir(n)}),d),
				connector.OpDestructive("directory_delete","Delete empty directory","Delete only an empty directory.",pathInput{},fileAction("delete",func(_ Policy,n string,_ *connector.Ctx)(any,error){return map[string]bool{"deleted":true},removeFile(n,true)}),d),
				connector.Op("search_files","Search files","Find permitted filenames by glob.",searchInput{},fileAction("search",func(p Policy,n string,c *connector.Ctx)(any,error){return search(p,n,c,false,false)}),d),
				connector.Op("search_content","Search content","Search permitted UTF-8 files for literal text.",searchInput{},fileAction("search",func(p Policy,n string,c *connector.Ctx)(any,error){return search(p,n,c,true,false)}),d),
			),
			connector.Cat("Terminal", "Trusted host execution only. Command filters are not a sandbox; child programs can bypass file policy.",
				connector.OpDestructive("exec","Execute command","Run executable with literal arguments using safeexec. Trusted host mode required.",execInput{},func(c *connector.Ctx)(any,error){return executeCommand(c,false)},d),
				connector.OpDestructive("exec_shell","Execute shell","Run arbitrary script only with explicitly unrestricted shell policy.",shellInput{},func(c *connector.Ctx)(any,error){return executeCommand(c,true)},d),
			),
			connector.Cat("System", "Read host information with explicit system permission.",
				connector.Op("system_info","System info","Return OS, architecture and capability flags.",struct{}{},systemInfo,d),
				connector.Op("disk_usage","Disk usage","Filesystem capacity at permitted directory.",pathInput{},fileAction("disk_usage",func(p Policy,n string,_ *connector.Ctx)(any,error){if !contains(p.System.Allow,"disk_usage"){return nil,fmt.Errorf("disk_usage denied")};return diskUsage(n)}),d),
			),
		},
	}
}

func getPolicy(c *connector.Ctx)(any,error){return policyFor(c)}
func validatePolicy(c *connector.Ctx)(any,error){
	p,err:=loadPolicy(c.Input("policy_json"))
	if err!=nil{return map[string]any{"valid":false,"error":err.Error()},nil}
	return map[string]any{"valid":true,"policy":p},nil
}
func checkPolicy(c *connector.Ctx)(any,error){
	p,err:=policyFor(c);if err!=nil{return nil,err}
	op:=c.Input("operation")
	var verdict,reason string
	if op=="exec"||op=="exec_shell"{
		args,e:=parseArgs(c.Input("args_json"));if e!=nil{return nil,e}
		verdict,reason=p.commandVerdict(c.Input("command"),args,op=="exec_shell")
	}else{
		n,e:=absolutePath(c.Input("path"));if e!=nil{return nil,e}
		verdict,reason=p.fileVerdict(op,n)
	}
	return map[string]any{"verdict":verdict,"reason":reason,"executed":false},nil
}

// The config-only widget is intentionally read-only; saving JSON uses Wick's
// normal settings permissions instead of granting a policy-write MCP tool.
func overview(c *connector.Ctx)(any,error){
	p,err:=policyFor(c)
	if err!=nil{return map[string]string{"html":"<p>Invalid policy: "+html.EscapeString(err.Error())+"</p>"},nil}
	var b strings.Builder
	fmt.Fprintf(&b,"<div><p><strong>Filesystem default:</strong> %s</p>",html.EscapeString(p.Filesystem.Default))
	for _,r:=range p.Filesystem.Rules{
		fmt.Fprintf(&b,"<p><code>%s</code><br>Allow: %s · Deny: %s · Approval: %s</p>",html.EscapeString(r.Path),html.EscapeString(strings.Join(r.Allow,", ")),html.EscapeString(strings.Join(r.Deny,", ")),html.EscapeString(strings.Join(r.Approval,", ")))
	}
	fmt.Fprintf(&b,"<p><strong>Terminal:</strong> %s · trusted host: %t · shell: %t</p>",html.EscapeString(p.Terminal.Mode),p.Terminal.TrustHost,p.Terminal.AllowShell)
	raw,_:=json.MarshalIndent(p,"","  ")
	fmt.Fprintf(&b,"<details><summary>Effective JSON</summary><pre style=\"white-space:pre-wrap;word-break:break-word\">%s</pre></details><p>Terminal is not sandboxed. Policy approval rules deny execution until an administrator changes the saved policy.</p></div>",html.EscapeString(string(raw)))
	return map[string]string{"html":b.String()},nil
}
