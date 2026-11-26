// Package meshclient provides the bounded, authenticated Mesh management client.
package meshclient

import (
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "net/http"
 "net/url"
 "strings"
 "time"

 "github.com/yogasw/wick/plugins/service/wick_mesh/meshstate"
)

const maxResponse = 128 << 10

// Policy reads a single read-only management endpoint. base must be an
// administrator-configured HTTPS URL of the Mesh service, not agent input.
// The bearer token is never forwarded to redirects or printed in errors.
func Policy(ctx context.Context,base,token string)(meshstate.Policy,error){
 return policy(ctx,base,token,nil)
}
func policy(ctx context.Context,base,token string,transport http.RoundTripper)(meshstate.Policy,error){
 var result meshstate.Policy
 u,e:=url.Parse(base)
 if e!=nil||u.Scheme!="https"||u.Hostname()==""||u.User!=nil||u.RawQuery!=""||u.ForceQuery||u.Fragment!=""||u.Opaque!=""||u.RawPath!=""{
 return result,errors.New("configure a valid HTTPS Mesh service URL")
 }
 if strings.TrimRight(u.Path,"/")!="/x/wick_mesh"{return result,errors.New("Mesh service URL must end in /x/wick_mesh")}
 if token==""||len(token)>8192||strings.ContainsAny(token,"\r\n"){return result,errors.New("configure a Mesh service token")}
 u.Path="/x/wick_mesh/api/policy"
 req,e:=http.NewRequestWithContext(ctx,http.MethodGet,u.String(),nil);if e!=nil{return result,errors.New("invalid Mesh request")}
 req.Header.Set("Authorization","Bearer "+token);req.Header.Set("Accept","application/json")
 client:=&http.Client{Timeout:8*time.Second,Transport:transport,CheckRedirect:func(_ *http.Request,_ []*http.Request)error{return http.ErrUseLastResponse}}
 resp,e:=client.Do(req);if e!=nil{return result,errors.New("Mesh service unreachable or TLS verification failed")}
 defer resp.Body.Close()
 if resp.StatusCode!=http.StatusOK{return result,fmt.Errorf("Mesh service returned HTTP %d",resp.StatusCode)}
 b,e:=io.ReadAll(io.LimitReader(resp.Body,maxResponse+1));if e!=nil||len(b)>maxResponse{return result,errors.New("invalid Mesh response size")}
 if e=json.Unmarshal(b,&result);e!=nil{return meshstate.Policy{},errors.New("invalid Mesh policy response")}
 if _,e=meshstate.Preset(result.Profile);e!=nil{return meshstate.Policy{},errors.New("unsupported Mesh profile")}
 if result.Profile==meshstate.Mobile&&(result.Roles.Worker||result.Roles.Voter||result.Roles.Orchestrator){return meshstate.Policy{},errors.New("invalid mobile roles")}
 for k,r:=range result.Rules{if e=meshstate.ValidateRule(k,r);e!=nil{return meshstate.Policy{},errors.New("unsupported Mesh policy rule")}}
 if len(result.Rules)!=len(meshstate.KnownResources()){return meshstate.Policy{},errors.New("incomplete Mesh policy")}
 return result,nil
}
