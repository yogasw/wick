package meshstate

import (
 "crypto/ed25519"
 "crypto/rand"
 "net/http"
 "io"
 "crypto/tls"
 "net/http/httptest"
 "path/filepath"
 "testing"
 "time"
)
func pairForTest(t *testing.T,s *PeerStore,inviter string,key ed25519.PrivateKey,grants map[string]Grant,now time.Time){
 t.Helper()
 tok,e:=s.Invite(key.Public().(ed25519.PublicKey),grants,now,time.Minute);if e!=nil{t.Fatal(e)}
 proof,e:=PairProof(key,inviter,tok);if e!=nil{t.Fatal(e)}
 if _,e=s.Redeem(tok,proof,now);e!=nil{t.Fatal(e)}
}
func TestMutualTLSPeerAuthorization(t *testing.T){
 now:=time.Now();clock:=func()time.Time{return now}
 a,ak,e:=ed25519.GenerateKey(rand.Reader);if e!=nil{t.Fatal(e)}
 b,bk,e:=ed25519.GenerateKey(rand.Reader);if e!=nil{t.Fatal(e)}
 c,ck,e:=ed25519.GenerateKey(rand.Reader);if e!=nil{t.Fatal(e)}
 as,e:=OpenPeerStore(filepath.Join(t.TempDir(),"a.json"),PeerID(a));if e!=nil{t.Fatal(e)}
 bs,e:=OpenPeerStore(filepath.Join(t.TempDir(),"b.json"),PeerID(b));if e!=nil{t.Fatal(e)}
 pairForTest(t,as,PeerID(a),bk,map[string]Grant{"results":{Read:true}},now)
 pairForTest(t,bs,PeerID(b),ak,nil,now)
 pairForTest(t,bs,PeerID(b),ck,nil,now)
 ac,e:=NodeCertificate(ak,now);if e!=nil{t.Fatal(e)}
 bc,e:=NodeCertificate(bk,now);if e!=nil{t.Fatal(e)}
 serverState := make(chan tls.ConnectionState, 4)
 srv:=httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
 select{case serverState<-*r.TLS:default:}
 if _,e:=RequestPeer(r.TLS,as,"results",r.Method!="GET",now);e!=nil{http.Error(w,"forbidden",http.StatusForbidden);return}
 w.WriteHeader(http.StatusOK)
 }))
 srv.TLS,e=ServerTLS(ac,as,clock);if e!=nil{t.Fatal(e)}
 srv.StartTLS();defer srv.Close()
 cfg,e:=ClientTLS(bc,bs,PeerID(a),clock);if e!=nil{t.Fatal(e)}
 tr:=&http.Transport{TLSClientConfig:cfg};defer tr.CloseIdleConnections()
 client:=&http.Client{Transport:tr,Timeout:5*time.Second}
 do:=func(method string,want int){
 t.Helper();req,e:=http.NewRequest(method,srv.URL,nil);if e!=nil{t.Fatal(e)}
 resp,e:=client.Do(req);if e!=nil{t.Fatal(e)};_,e=io.Copy(io.Discard,resp.Body);resp.Body.Close();if e!=nil{t.Fatal(e)};if resp.StatusCode!=want{t.Fatalf("status %d, want %d",resp.StatusCode,want)}
 }
 do("GET",200);do("POST",403)
 wrong,e:=ClientTLS(bc,bs,PeerID(c),clock);if e!=nil{t.Fatal(e)}
 wt:=&http.Transport{TLSClientConfig:wrong};defer wt.CloseIdleConnections()
 if resp,e:=(&http.Client{Transport:wt,Timeout:5*time.Second}).Get(srv.URL);e==nil{resp.Body.Close();t.Fatal("wrong server accepted")}
 if e=as.Revoke(PeerID(b));e!=nil{t.Fatal(e)}
 // Verify revocation against the server-side state of a previously authenticated connection.
 select{case state:=<-serverState:if _,e:=RequestPeer(&state,as,"results",false,now);e==nil{t.Fatal("revoked established peer accepted")};default:t.Fatal("missing server TLS state")}
 tr.CloseIdleConnections()
 if resp,e:=client.Get(srv.URL);e==nil{resp.Body.Close();t.Fatal("revoked peer handshake accepted")}
}
