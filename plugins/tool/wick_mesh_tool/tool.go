package main

import (
 "context"
 "html/template"
 "io"
 "net/http"
 "sort"
 "strings"

 "github.com/a-h/templ"
 "github.com/yogasw/wick/pkg/entity"
 "github.com/yogasw/wick/pkg/tool"
 "github.com/yogasw/wick/plugins/service/wick_mesh/meshclient"
 "github.com/yogasw/wick/plugins/service/wick_mesh/meshstate"
)

type Config struct{
 BaseURL string `wick:"required;url;desc=HTTPS Mesh service URL ending in /x/wick_mesh"`
 Token string `wick:"required;secret;desc=Mesh service bearer token"`
}
func Module()tool.Module{return tool.Module{
 Meta:tool.Tool{Key:"wick_mesh_tool",Name:"Wick Mesh",Description:"Inspect Mesh replication policy",Icon:"🕸️",Category:"Plugins"},
 Configs:entity.StructToConfigs(Config{}),Register:register,
}}
type ruleRow struct{Resource string; Rule meshstate.Rule}
type view struct{Base,Search,Error string;Policy meshstate.Policy;Rows []ruleRow;Total int}
func buildView(base,search string,p meshstate.Policy,e error)view{
 v:=view{Base:base,Search:search,Policy:p,Total:len(p.Rules)}
 if e!=nil{v.Error="Mesh service unavailable. Check the service URL, token and TLS settings.";return v}
 for k,r:=range p.Rules{if strings.Contains(strings.ToLower(k),strings.ToLower(search)){v.Rows=append(v.Rows,ruleRow{k,r})}}
 sort.Slice(v.Rows,func(i,j int)bool{return v.Rows[i].Resource<v.Rows[j].Resource})
 return v
}
func register(r tool.Router){
 r.GET("/",func(c *tool.Ctx){
 u,ok:=c.User();if !ok||!u.IsAdmin{c.JSON(http.StatusForbidden,map[string]string{"error":"administrator access required"});return}
 p,e:=meshclient.Policy(c.Context(),c.Cfg("base_url"),c.Cfg("token"))
 search:=c.Query("q");if len(search)>128{search=search[:128]}
 v:=buildView(c.Base(),search,p,e)
 c.HTML(templ.ComponentFunc(func(_ context.Context,w io.Writer)error{return page.Execute(w,v)}))
 })
 r.GET("/api/policy",func(c *tool.Ctx){
 u,ok:=c.User();if !ok||!u.IsAdmin{c.JSON(http.StatusForbidden,map[string]string{"error":"administrator access required"});return}
 p,e:=meshclient.Policy(c.Context(),c.Cfg("base_url"),c.Cfg("token"))
 c.W.Header().Set("Cache-Control","no-store")
 if e!=nil{c.JSON(http.StatusBadGateway,map[string]string{"error":"Mesh service unavailable"});return}
 c.JSON(http.StatusOK,p)
 })
}
var page=template.Must(template.New("mesh").Parse(`<section class="space-y-4">
<header class="flex flex-wrap items-start justify-between gap-3">
<div><h1 class="text-2xl font-semibold">Mesh overview</h1><p class="text-sm opacity-70">Replication policy · Experimental preview</p></div>
<a class="rounded-lg border px-4 py-2" href="{{.Base}}/">Refresh</a>
</header>
{{if .Error}}<div role="alert" class="rounded-lg border p-4"><strong>Service unavailable</strong><p>{{.Error}}</p><a class="underline" href="{{.Base}}/">Retry</a></div>
{{else}}
<div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
<div class="rounded-lg border p-3"><small>Profile</small><p class="font-semibold">{{.Policy.Profile}}</p></div>
<div class="rounded-lg border p-3"><small>Orchestrator</small><p>{{if .Policy.Roles.Orchestrator}}Allowed{{else}}Disabled{{end}}</p></div>
<div class="rounded-lg border p-3"><small>Worker</small><p>{{if .Policy.Roles.Worker}}Allowed{{else}}Disabled{{end}}</p></div>
<div class="rounded-lg border p-3"><small>Voter</small><p>{{if .Policy.Roles.Voter}}Allowed{{else}}Disabled{{end}}</p></div>
</div>
<div class="rounded-lg border p-4 text-sm">This is the configured preset, not live cluster health. Pairing, job execution and synchronization controls are not yet available here.</div>
<form method="get" action="{{.Base}}/" class="flex flex-wrap items-end gap-2">
<label class="flex-1 min-w-0">Find resource<input class="block w-full rounded-lg border bg-transparent px-3 py-2" name="q" value="{{.Search}}" maxlength="128" placeholder="e.g. files"></label>
<button class="rounded-lg border px-4 py-2" type="submit">Filter</button><a class="px-3 py-2 underline" href="{{.Base}}/">Clear</a>
</form>
<h2 class="font-semibold">Replication rules ({{len .Rows}} / {{.Total}})</h2>
<div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
{{range .Rows}}<article class="rounded-lg border p-3 min-w-0"><h3 class="font-semibold break-words">{{.Resource}}</h3><dl class="mt-2 grid grid-cols-2 gap-2 text-sm"><dt>Mode</dt><dd>{{.Rule.Mode}}</dd><dt>Direction</dt><dd>{{.Rule.Direction}}</dd><dt>Network</dt><dd>{{if .Rule.WiFiOnly}}Wi-Fi only{{else}}Any{{end}}</dd><dt>Limit / transfer</dt><dd>{{if .Rule.ByteBudget}}{{.Rule.ByteBudget}} B{{else}}Unlimited{{end}}</dd></dl></article>
{{else}}<p>No matching resources.</p>{{end}}
</div>{{end}}</section>`))
