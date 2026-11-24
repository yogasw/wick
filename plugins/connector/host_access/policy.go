package main

import (
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "os"
 "path/filepath"
 "strings"
)

const defaultPolicy = `{"version":1,"filesystem":{"default":"deny","rules":[]},"terminal":{"mode":"disabled"},"system":{"allow":[]}}`

type Rule struct {
 Path string `json:"path"`
 Recursive *bool `json:"recursive,omitempty"`
 Allow []string `json:"allow,omitempty"`
 Deny []string `json:"deny,omitempty"`
 Approval []string `json:"approval,omitempty"`
}
type Policy struct {
 Version int `json:"version"`
 Filesystem struct {
  Default string `json:"default"`
  Rules []Rule `json:"rules"`
  ProtectedPatterns []string `json:"protected_patterns,omitempty"`
 } `json:"filesystem"`
 Terminal struct {
  Mode string `json:"mode"`
  Allow []string `json:"allow,omitempty"`
  Deny []string `json:"deny,omitempty"`
  Approval []string `json:"approval,omitempty"`
  TrustHost bool `json:"trust_host,omitempty"`
  AllowShell bool `json:"allow_shell,omitempty"`
  TimeoutSeconds int `json:"timeout_seconds,omitempty"`
 } `json:"terminal"`
 System struct { Allow []string `json:"allow"` } `json:"system"`
}

var fsOps = []string{"list","read","search","stat","create","write","edit","mkdir","delete","copy","move","disk_usage"}
func contains(xs []string, value string) bool {
 for _, x := range xs { if x == value || x == "*" { return true } }
 return false
}
func validateOps(xs []string, allowed []string) error {
 for _, x := range xs { if x!="*" && !contains(allowed,x) { return fmt.Errorf("unknown operation %q",x) } }
 return nil
}

func absolutePath(raw string) (string,error) {
 if raw=="" || strings.ContainsRune(raw,0) { return "",errors.New("absolute path required") }
 for _, part := range strings.Split(filepath.ToSlash(raw), "/") { if part == ".." { return "", errors.New("parent traversal is not allowed") } }
 // Accept the OS home alias on Android, but never follow caller-selected symlinks.
 home, err := os.UserHomeDir()
 if err==nil && home!="" {
  if raw=="~" { raw=home } else if strings.HasPrefix(raw,"~/") { raw=filepath.Join(home,raw[2:]) }
  if real, e:=filepath.EvalSymlinks(home); e==nil && within(home,raw) {
   rel,_:=filepath.Rel(home,raw); raw=filepath.Join(real,rel)
  }
 }
 if !filepath.IsAbs(raw) { return "",errors.New("absolute path or ~/ required") }
 for _, part:=range strings.Split(filepath.ToSlash(raw),"/") { if part==".." { return "",errors.New("parent traversal is not allowed") } }
 return filepath.Clean(raw),nil
}
func within(base,p string) bool {
 rel,err:=filepath.Rel(base,p)
 return err==nil && rel!=".." && !strings.HasPrefix(rel,".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func loadPolicy(raw string) (Policy,error) {
 var p Policy
 if strings.TrimSpace(raw)=="" || raw=="{}" { raw=defaultPolicy }
 if len(raw)>128*1024 { return p,errors.New("policy too large") }
 d:=json.NewDecoder(strings.NewReader(raw)); d.DisallowUnknownFields()
 if err:=d.Decode(&p);err!=nil{return p,err}
 if err:=d.Decode(new(any));err!=io.EOF{return p,errors.New("trailing JSON")}
 if p.Version!=1{return p,errors.New("policy version must be 1")}
 if p.Filesystem.Default!="allow" && p.Filesystem.Default!="deny" {return p,errors.New("filesystem.default must be allow or deny")}
 if len(p.Filesystem.Rules)>256{return p,errors.New("too many path rules")}
 for i:=range p.Filesystem.Rules {
  r:=&p.Filesystem.Rules[i]; a,err:=absolutePath(r.Path);if err!=nil{return p,fmt.Errorf("rule %d: %w",i,err)};r.Path=a
  for _, ops:=range [][]string{r.Allow,r.Deny,r.Approval}{if err:=validateOps(ops,fsOps);err!=nil{return p,err}}
 }
 for _,g:=range p.Filesystem.ProtectedPatterns {
  if g=="" {return p,errors.New("empty protected pattern")}
  for _,s:=range strings.Split(filepath.ToSlash(g),"/"){if s=="**"{continue};if _,err:=filepath.Match(s,"x");err!=nil{return p,err}}
 }
 switch p.Terminal.Mode {case "disabled","whitelist","blocklist","all": default:return p,errors.New("invalid terminal.mode")}
 if p.Terminal.TimeoutSeconds<0||p.Terminal.TimeoutSeconds>3600{return p,errors.New("terminal timeout must be 0..3600")}
 if err:=validateOps(p.System.Allow,[]string{"system_info","disk_usage"});err!=nil{return p,err}
 return p,nil
}

func globParts(pattern, parts []string) bool {
 if len(pattern)==0{return len(parts)==0}
 if pattern[0]=="**" {
  return globParts(pattern[1:],parts)||(len(parts)>0&&globParts(pattern,parts[1:]))
 }
 if len(parts)==0{return false}
 matched,_:=filepath.Match(pattern[0],parts[0]);return matched&&globParts(pattern[1:],parts[1:])
}
func protected(pattern,p string) bool {
 if !strings.Contains(pattern,"/"){matched,_:=filepath.Match(pattern,filepath.Base(p));return matched}
 return globParts(strings.Split(strings.TrimPrefix(filepath.ToSlash(pattern),"/"),"/"),strings.Split(strings.TrimPrefix(filepath.ToSlash(p),"/"),"/"))
}
func (p Policy) fileVerdict(op, name string) (string,string) {
 if !contains(fsOps,op){return "deny","unknown operation"}
 // Never expose these to native file operations even if broad defaults allow.
 // Admin can explicitly choose trusted terminal mode instead; it is not sandboxed.
 for _,g:=range p.Filesystem.ProtectedPatterns{if protected(g,name){return "deny","protected pattern: "+g}}
 allow:=false; approval:=""
 for _,r:=range p.Filesystem.Rules{
  if name!=r.Path && ((r.Recursive!=nil&&!*r.Recursive)||!within(r.Path,name)){continue}
  if contains(r.Deny,op){return "deny","denied by "+r.Path}
  if contains(r.Approval,op){approval=r.Path}
  if contains(r.Allow,op){allow=true}
 }
 if approval!=""{return "approval","admin approval required by "+approval}
 if allow||p.Filesystem.Default=="allow"{return "allow","policy allows operation"}
 return "deny","default deny"
}
func (p Policy) authorize(op,raw string)(string,error){
 name,err:=absolutePath(raw);if err!=nil{return "",err}
 v,reason:=p.fileVerdict(op,name);if v!="allow"{return "",fmt.Errorf("%s: %s (%s)",op,reason,v)}
 return name,nil
}
func (p Policy) commandVerdict(name string, args []string, shell bool)(string,string){
 t:=p.Terminal
 if t.Mode=="disabled"{return "deny","terminal disabled"}
 if !t.TrustHost{return "deny","no OS sandbox configured; trusted host mode must be explicitly enabled by admin"}
 if shell&&!t.AllowShell{return "deny","shell disabled"}
 // Rules match an executable name/path or an exact argv prefix. No shell parsing.
 match:=func(rules []string)bool{
  for _,r:=range rules{
   if r=="*"||r==name||r==filepath.Base(name){return true}
   prefix:=strings.Fields(r)
   if len(prefix)<2 || (prefix[0]!=name&&prefix[0]!=filepath.Base(name)) || len(args)<len(prefix)-1{continue}
   yes:=true;for i,a:=range prefix[1:]{if args[i]!=a{yes=false;break}};if yes{return true}
  };return false
 }
 if match(t.Deny){return "deny","command deny rule"}
 if match(t.Approval){return "approval","requires admin policy change; no self approval"}
 if t.Mode=="all"||t.Mode=="blocklist"||match(t.Allow){return "allow","trusted host execution (not sandboxed)"}
 return "deny","command not in whitelist"
}
