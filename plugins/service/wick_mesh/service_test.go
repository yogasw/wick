package main

import (
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "testing"

 "github.com/yogasw/wick/pkg/service"
 "github.com/yogasw/wick/plugins/service/wick_mesh/meshstate"
)

func TestPolicyREST(t *testing.T){
 mux:=http.NewServeMux()
 profile:="mobile"
 registerPolicy(mux,func()string{return profile})
 rec:=httptest.NewRecorder()
 mux.ServeHTTP(rec,httptest.NewRequest("GET","/api/policy",nil))
 if rec.Code!=200||rec.Header().Get("Content-Type")!="application/json"||rec.Header().Get("Cache-Control")!="no-store"{t.Fatal(rec.Code,rec.Header())}
 var p meshstate.Policy
 if e:=json.Unmarshal(rec.Body.Bytes(),&p);e!=nil{t.Fatal(e)}
 if p.Profile!=meshstate.Mobile||p.Roles.Worker||p.Roles.Voter||p.Rules["results"].Mode!=meshstate.ResultOnly{t.Fatal(p)}
 profile="not-a-profile"
 for _,c:=range []struct{method,path string;want int}{{"GET","/api/policy",400},{"POST","/api/policy",405},{"GET","/api/unknown",404}}{
 rec=httptest.NewRecorder();mux.ServeHTTP(rec,httptest.NewRequest(c.method,c.path,nil))
 if rec.Code!=c.want{t.Fatalf("%s %s: %d want %d",c.method,c.path,rec.Code,c.want)}
 }
}
func TestPolicyRouteRequiresHostToken(t *testing.T){
 m:=Module()
 if len(m.Routes)!=1||m.Routes[0].Prefix!="/api"||m.Routes[0].Auth!=service.Token{t.Fatalf("unsafe route declaration: %+v",m.Routes)}
}
