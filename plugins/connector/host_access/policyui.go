package main

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// getPolicy exposes the effective configuration without mutating it.
func getPolicy(c *connector.Ctx)(any,error){return policyFor(c)}
// validatePolicy is a dry run: proposed JSON never grants permissions.
func validatePolicy(c *connector.Ctx)(any,error){
	p,err:=loadPolicy(c.Input("policy_json"))
	if err!=nil{return map[string]any{"valid":false,"error":err.Error()},nil}
	return map[string]any{"valid":true,"policy":p},nil
}
// checkPolicy uses the same verdict engine as execution; it does not touch files or run commands.
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
