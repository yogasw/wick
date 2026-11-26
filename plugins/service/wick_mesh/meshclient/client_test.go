package meshclient

import (
 "context"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"

 "github.com/yogasw/wick/plugins/service/wick_mesh/meshstate"
)
func TestPolicyHTTPSContract(t *testing.T){
 calls:=0
 srv:=httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
 calls++
 if r.URL.Path!="/x/wick_mesh/api/policy"||r.Header.Get("Authorization")!="Bearer fixture-token"{t.Error("incorrect request")}
 p,_:=meshstate.Preset(meshstate.Mobile);_ = json.NewEncoder(w).Encode(p)
 }));defer srv.Close()
 p,e:=policy(context.Background(),srv.URL+"/x/wick_mesh/","fixture-token",srv.Client().Transport)
 if e!=nil||p.Profile!=meshstate.Mobile||calls!=1{t.Fatal(p,e,calls)}
}
func TestPolicyRejectsRedirectAndMalformedResponses(t *testing.T){
 targetCalls:=0
 target:=httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){targetCalls++;w.WriteHeader(200)}));defer target.Close()
 for _,mode:=range []string{"redirect","oversize","invalid","denied"}{
 t.Run(mode,func(t *testing.T){
 srv:=httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
 switch mode{case "redirect":http.Redirect(w,r,target.URL,302);case "oversize":_,_=w.Write([]byte(strings.Repeat("x",maxResponse+1)));case "invalid":_,_=w.Write([]byte("{}"));case "denied":w.WriteHeader(403);_,_=w.Write([]byte("fixture-token"))}
 }));defer srv.Close()
 _,e:=policy(context.Background(),srv.URL+"/x/wick_mesh","fixture-token",srv.Client().Transport)
 if e==nil||strings.Contains(e.Error(),"fixture-token"){t.Fatal(e)}
 })
 }
 if targetCalls!=0{t.Fatal("redirect was followed")}
 for _,u:=range []string{"http://example.org/x/wick_mesh","https://user:pass@example.org/x/wick_mesh","https://example.org/other","https://example.org/x/wick_mesh?token=x"}{
 if _,e:=Policy(context.Background(),u,"fixture-token");e==nil{t.Fatal("invalid URL accepted")}
 }
}
