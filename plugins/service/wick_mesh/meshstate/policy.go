// Package meshstate contains transport-independent Mesh policy primitives.
package meshstate

import (
 "errors"
 "fmt"
 "sort"
)

type Profile string
const (
 Full Profile = "full"
 Mobile Profile = "mobile"
 Worker Profile = "worker"
 Backup Profile = "backup"
 Custom Profile = "custom"
)
type Mode string
const (
 Disabled Mode = "disabled"
 Metadata Mode = "metadata"
 ResultOnly Mode = "result_only"
 OnDemand Mode = "on_demand"
 Replicate Mode = "replicate"
)
type Direction string
const (
 Pull Direction = "pull"
 Push Direction = "push"
 Both Direction = "both"
)
type Rule struct {
 Mode Mode `json:"mode"`
 Direction Direction `json:"direction"`
 WiFiOnly bool `json:"wifi_only"`
 ByteBudget int64 `json:"byte_budget"`
}
type Roles struct {
 Orchestrator bool `json:"orchestrator"`
 Worker bool `json:"worker"`
 Voter bool `json:"voter"`
 Sync bool `json:"sync"`
}
type Policy struct {
 Profile Profile `json:"profile"`
 Roles Roles `json:"roles"`
 Rules map[string]Rule `json:"rules"`
}
var resources = []string{"sessions","messages","projects","memory","agents","skills","attachments","datatables","workflows","schedules","job_history","images","plugins","connectors","users","permissions","tags","settings","files","results","internal_messages","checkpoints","audit"}
func KnownResources() []string {
 out := append([]string(nil), resources...)
 sort.Strings(out)
 return out
}
func Preset(profile Profile) (Policy,error) {
 p := Policy{Profile:profile, Rules:map[string]Rule{}, Roles:Roles{Sync:true}}
 switch profile {
 case Full: p.Roles=Roles{true,true,true,true}
 case Mobile, Backup, Custom:
 case Worker: p.Roles.Worker=true
 default: return Policy{}, errors.New("unknown profile")
 }
 for _,k := range resources {
  r:=Rule{Mode:Disabled,Direction:Pull}
  switch profile {
  case Full: r=Rule{Mode:Replicate,Direction:Both}
  case Backup: r=Rule{Mode:Replicate,Direction:Pull}
  case Mobile:
   switch k {
   case "results": r.Mode=ResultOnly
   case "projects","files","datatables": r.Mode=Metadata
   }
  case Worker:
   switch k {
   case "results","checkpoints","job_history": r.Mode=Replicate; r.Direction=Both
   }
  }
  p.Rules[k]=r
 }
 return p,nil
}
func ValidateRule(key string,r Rule) error {
 known:=false
 for _,k:=range resources { if k==key {known=true;break} }
 if !known {return fmt.Errorf("resource is not registered: %s",key)}
 switch r.Mode {case Disabled,Metadata,ResultOnly,OnDemand,Replicate: default:return errors.New("unknown replication mode")}
 switch r.Direction {case Pull,Push,Both: default:return errors.New("unknown direction")}
 if r.ByteBudget<0 {return errors.New("negative byte budget")}
 return nil
}
// Resolve applies group rules before node overrides without mutating inputs.
// Unknown resources (including secrets, host policies and runtime state) fail closed.
func Resolve(profile Profile,group,node map[string]Rule,roles *Roles)(Policy,error){
 p,err:=Preset(profile);if err!=nil{return Policy{},err}
 for _,layer:=range []map[string]Rule{group,node}{
  for k,r:=range layer{if err:=ValidateRule(k,r);err!=nil{return Policy{},err};p.Rules[k]=r}
 }
 if roles!=nil {p.Roles=*roles}
 if profile==Mobile&&(p.Roles.Voter||p.Roles.Worker||p.Roles.Orchestrator){return Policy{},errors.New("mobile profile is sync-only; use custom profile for explicit compute roles")}
 return p,nil
}
// AllowsTransfer is a policy decision, not authentication or resource ACL.
// Callers must separately authorize the peer and each resource.
func (p Policy) AllowsTransfer(resource string,outbound,wifi bool,bytes int64)bool{
 r,ok:=p.Rules[resource]
 if !ok||ValidateRule(resource,r)!=nil||!p.Roles.Sync||r.Mode==Disabled||bytes<0{return false}
 if r.WiFiOnly&&!wifi{return false}
 if outbound&&r.Direction==Pull||!outbound&&r.Direction==Push{return false}
 return r.ByteBudget==0||bytes<=r.ByteBudget
}
