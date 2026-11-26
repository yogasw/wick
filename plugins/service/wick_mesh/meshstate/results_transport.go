package meshstate

import (
 "bytes"
 "context"
 "crypto/tls"
 "encoding/json"
 "errors"
 "io"
 "net/http"
 "net/url"
 "strconv"
 "strings"
 "time"
)

const ResultRoute="/mesh/v1/results"
const maxResultWire=MaxResultPage*(MaxResultBytes+128)+4096
// resultTransfer additionally checks data class: metadata-only and on-demand
// scopes cannot silently authorize the background final-result stream.
func resultTransfer(p Policy,outbound,wifi bool,size int64)bool{
 rule,ok:=p.Rules["results"]
 return ok&&(rule.Mode==Replicate||rule.Mode==ResultOnly)&&p.AllowsTransfer("results",outbound,wifi,size)
}
// ResultHandler serves the dedicated, explicitly published final-result feed
// to paired peers. Do not attach it to a bearer-token proxy or ordinary HTTP
// listener. The stream is for a single trust scope; per-object ACLs are still
// required before adapters publish real multi-user Wick job data.
func ResultHandler(store *Results,peers *PeerStore,policy func()Policy,wifi func()bool,clock func()time.Time)(http.Handler,error){
 if store==nil||peers==nil||policy==nil||wifi==nil||clock==nil||store.disk.Origin!=peers.data.LocalID{return nil,errors.New("invalid result handler configuration")}
 mux:=http.NewServeMux()
 mux.HandleFunc("GET "+ResultRoute,func(w http.ResponseWriter,r *http.Request){
 w.Header().Set("Cache-Control","no-store")
 if _,e:=RequestPeer(r.TLS,peers,"results",false,clock());e!=nil{http.Error(w,"forbidden",403);return}
 p:=policy();isWiFi:=wifi()
 if !resultTransfer(p,true,isWiFi,0){http.Error(w,"result transfer disabled",403);return}
 q,e:=url.ParseQuery(r.URL.RawQuery);if e!=nil||len(q)!=2||len(q["after"])!=1||len(q["limit"])!=1{http.Error(w,"invalid cursor",400);return}
 after,e:=strconv.ParseUint(q.Get("after"),10,64);if e!=nil{http.Error(w,"invalid cursor",400);return}
 limit,e:=strconv.Atoi(q.Get("limit"));if e!=nil||limit<1||limit>MaxResultPage{http.Error(w,"invalid limit",400);return}
 page,e:=store.Page(after,limit);if e!=nil{http.Error(w,"cursor unavailable",409);return}
 // Reduce batch size rather than splitting a single result or exceeding
 // the configured per-transfer budget. Never hide a budget-blocked item.
 for{
 b,e:=json.Marshal(page);if e!=nil{http.Error(w,"result unavailable",500);return}
 if len(b)<=maxResultWire&&resultTransfer(p,true,isWiFi,int64(len(b))){
 w.Header().Set("Content-Type","application/json");_,_=w.Write(b);return
 }
 if len(page.Events)<=1{http.Error(w,"result exceeds transfer budget",413);return}
 page.Events=page.Events[:len(page.Events)-1]
 }
 })
 return mux,nil
}
// PullResults makes one bounded HTTPS/mTLS request and atomically commits
// the received events with their cursor. The endpoint is a configured peer
// address, never derived from job input or a redirect.
func PullResults(ctx context.Context,endpoint,target string,cert tls.Certificate,peers *PeerStore,dst *Results,policy Policy,wifi bool,clock func()time.Time)(uint64,error){
 if peers==nil||dst==nil||clock==nil||dst.disk.Origin!=peers.data.LocalID||!resultTransfer(policy,false,wifi,0){return 0,errors.New("result pull disabled or invalid")}
 if _,e:=peers.Authorize(target,"results",true);e!=nil{return 0,e}
 u,e:=url.Parse(endpoint)
 if e!=nil||u.Scheme!="https"||u.Hostname()==""||u.User!=nil||u.RawQuery!=""||u.ForceQuery||u.Fragment!=""||u.Opaque!=""||u.RawPath!=""||strings.TrimRight(u.Path,"/")!=""{return 0,errors.New("invalid peer endpoint")}
 cursor,e:=dst.Cursor(target);if e!=nil{return 0,e}
 cfg,e:=ClientTLS(cert,peers,target,clock);if e!=nil{return cursor,e}
 u.Path=ResultRoute;q:=url.Values{};q.Set("after",strconv.FormatUint(cursor,10));q.Set("limit",strconv.Itoa(MaxResultPage));u.RawQuery=q.Encode()
 req,e:=http.NewRequestWithContext(ctx,http.MethodGet,u.String(),nil);if e!=nil{return cursor,errors.New("invalid result request")}
 req.Header.Set("Accept","application/json")
 tr:=&http.Transport{TLSClientConfig:cfg,TLSHandshakeTimeout:5*time.Second,MaxResponseHeaderBytes:16<<10,DisableCompression:true}
 defer tr.CloseIdleConnections()
 client:=&http.Client{Transport:tr,Timeout:10*time.Second,CheckRedirect:func(_ *http.Request,_ []*http.Request)error{return http.ErrUseLastResponse}}
 resp,e:=client.Do(req);if e!=nil{return cursor,errors.New("peer unavailable or authentication failed")}
 defer resp.Body.Close()
 if resp.StatusCode!=200{return cursor,errors.New("peer refused result pull")}
 max:=int64(maxResultWire);if budget:=policy.Rules["results"].ByteBudget;budget>0&&budget<max{max=budget}
 b,e:=io.ReadAll(io.LimitReader(resp.Body,max+1));if e!=nil||int64(len(b))>max||!resultTransfer(policy,false,wifi,int64(len(b))){return cursor,errors.New("result transfer exceeds limit")}
 var page ResultPage
 dec:=json.NewDecoder(bytes.NewReader(b));dec.DisallowUnknownFields()
 if e=dec.Decode(&page);e!=nil{return cursor,errors.New("invalid result page")}
 var extra any;if e=dec.Decode(&extra);e!=io.EOF{return cursor,errors.New("trailing result data")}
 if page.After!=cursor||page.Origin!=target{return cursor,errors.New("invalid result response cursor or origin")}
 // Check the current peer grant again before committing; revocation
 // serializes against this local transaction.
 peers.mu.Lock();defer peers.mu.Unlock()
 peer,ok:=peers.data.Peers[target]
 if peers.poisoned||!ok||peer.Revoked||!peer.Grants["results"].Write{return cursor,errors.New("peer grant revoked")}
 return dst.ApplyPage(target,page)
}
