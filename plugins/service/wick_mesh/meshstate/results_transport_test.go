package meshstate

import (
 "context"
 "crypto/ed25519"
 "crypto/rand"
 "net/http/httptest"
 "path/filepath"
 "testing"
 "time"
)
func TestResultTransportOfflineAndRevocation(t *testing.T){
 now:=time.Now();clock:=func()time.Time{return now};wifi:=func()bool{return true}
 a,ak,e:=ed25519.GenerateKey(rand.Reader);if e!=nil{t.Fatal(e)}
 b,bk,e:=ed25519.GenerateKey(rand.Reader);if e!=nil{t.Fatal(e)}
 aid,bid:=PeerID(a),PeerID(b)
 dir:=t.TempDir()
 as,e:=OpenPeerStore(filepath.Join(dir,"a-peers"),aid);if e!=nil{t.Fatal(e)}
 bs,e:=OpenPeerStore(filepath.Join(dir,"b-peers"),bid);if e!=nil{t.Fatal(e)}
 pairForTest(t,as,aid,bk,map[string]Grant{"results":{Read:true}},now)
 pairForTest(t,bs,bid,ak,map[string]Grant{"results":{Write:true}},now)
 ac,e:=NodeCertificate(ak,now);if e!=nil{t.Fatal(e)}
 bc,e:=NodeCertificate(bk,now);if e!=nil{t.Fatal(e)}
 src,e:=OpenResults(filepath.Join(dir,"a-results"),aid);if e!=nil{t.Fatal(e)}
 dp:=filepath.Join(dir,"b-results");dst,e:=OpenResults(dp,bid);if e!=nil{t.Fatal(e)}
 full,_:=Preset(Full);mobile,_:=Preset(Mobile)
 handler,e:=ResultHandler(src,as,func()Policy{return full},wifi,clock);if e!=nil{t.Fatal(e)}
 srv:=httptest.NewUnstartedServer(handler);srv.TLS,e=ServerTLS(ac,as,clock);if e!=nil{t.Fatal(e)}
 srv.StartTLS();defer srv.Close()
 if _,e=src.Publish(FinalResult{JobID:"one",State:Completed,Summary:"final"});e!=nil{t.Fatal(e)}
 pull:=func(p Policy)(uint64,error){return PullResults(context.Background(),srv.URL,aid,bc,bs,dst,p,true,clock)}
 if n,e:=pull(mobile);e!=nil||n!=1{t.Fatal(n,e)}
 // Destination is offline while source publishes a second job result.
 if _,e=src.Publish(FinalResult{JobID:"two",State:Failed,Summary:"final failure"});e!=nil{t.Fatal(e)}
 dst,e=OpenResults(dp,bid);if e!=nil{t.Fatal(e)}
 restricted,_:=Preset(Mobile);restricted.Rules["results"]=Rule{Mode:Metadata,Direction:Pull}
 if _,e=pull(restricted);e==nil{t.Fatal("metadata policy allowed full result")}
 restricted.Rules["results"]=Rule{Mode:ResultOnly,Direction:Pull,ByteBudget:1}
 if _,e=pull(restricted);e==nil{t.Fatal("byte budget ignored")}
 if n,e:=dst.Cursor(aid);e!=nil||n!=1{t.Fatal(n,e)}
 if n,e:=pull(mobile);e!=nil||n!=2{t.Fatal(n,e)}
 if n,e:=pull(mobile);e!=nil||n!=2{t.Fatal("repeat pull",n,e)}
 if v,e:=dst.Received(aid,2);e!=nil||v.JobID!="two"{t.Fatal(v,e)}
 if e=as.Revoke(bid);e!=nil{t.Fatal(e)}
 if _,e=pull(mobile);e==nil{t.Fatal("revoked peer received results")}
}
