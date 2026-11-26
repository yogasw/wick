package meshstate

import "testing"

func TestMobilePreset(t *testing.T){
 p,e:=Preset(Mobile);if e!=nil{t.Fatal(e)}
 if p.Roles.Worker||p.Roles.Voter||p.Roles.Orchestrator||!p.Roles.Sync{t.Fatal(p.Roles)}
 if p.Rules["results"].Mode!=ResultOnly||p.Rules["internal_messages"].Mode!=Disabled{t.Fatal(p.Rules)}
 if p.AllowsTransfer("secrets",false,true,0)||p.AllowsTransfer("results",true,true,0){t.Fatal("unexpected permission")}
}
func TestPrecedenceAndLimits(t *testing.T){
 group:=map[string]Rule{"files":{Mode:Replicate,Direction:Both}}
 node:=map[string]Rule{"files":{Mode:OnDemand,Direction:Pull,WiFiOnly:true,ByteBudget:10}}
 p,e:=Resolve(Mobile,group,node,nil);if e!=nil{t.Fatal(e)}
 if p.Rules["files"]!=node["files"]||group["files"].Mode!=Replicate{t.Fatal("precedence")}
 for _,x:=range []struct{out,wifi bool;n int64;want bool}{{false,true,10,true},{false,true,11,false},{false,false,1,false},{true,true,1,false},{false,true,-1,false}}{
 if got:=p.AllowsTransfer("files",x.out,x.wifi,x.n);got!=x.want{t.Fatalf("%+v: %v",x,got)}
 }
}
func TestInvalidPolicy(t *testing.T){
 for _,k:=range []string{"secrets","host_policy","runtime","unknown"}{
 if _,e:=Resolve(Full,map[string]Rule{k:{Mode:Replicate,Direction:Both}},nil,nil);e==nil{t.Fatal(k)}
 }
 if _,e:=Resolve(Mobile,nil,nil,&Roles{Voter:true});e==nil{t.Fatal("mobile voter allowed")}
 if _,e:=Resolve(Full,nil,map[string]Rule{"files":{Mode:Replicate,Direction:Pull,ByteBudget:-1}},nil);e==nil{t.Fatal("negative budget")}
}
