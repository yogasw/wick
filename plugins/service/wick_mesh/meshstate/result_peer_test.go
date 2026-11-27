package meshstate

import (
 "context"
 "crypto/ed25519"
 "path/filepath"
 "sync/atomic"
 "testing"
 "time"
)

// Exercise the actual dedicated listener and production result transport,
// including durable restart, leaf expiry, offline delivery and revocation.
func TestResultPeerLifecycle(t *testing.T) {
 root:=t.TempDir()
 now:=time.Now()
 var hours atomic.Int64
 clock:=func()time.Time{return now.Add(time.Duration(hours.Load())*time.Hour)}
 ak,e:=OpenNodeIdentity(filepath.Join(root,"a"));if e!=nil{t.Fatal(e)}
 bk,e:=OpenNodeIdentity(filepath.Join(root,"b"));if e!=nil{t.Fatal(e)}
 aid,bid:=PeerID(ak.Public().(ed25519.PublicKey)),PeerID(bk.Public().(ed25519.PublicKey))
 ap,bp:=filepath.Join(root,"a-peers"),filepath.Join(root,"b-peers")
 ar,br:=filepath.Join(root,"a-results"),filepath.Join(root,"b-results")
 as,e:=OpenPeerStore(ap,aid);if e!=nil{t.Fatal(e)}
 bs,e:=OpenPeerStore(bp,bid);if e!=nil{t.Fatal(e)}
 pairForTest(t,as,aid,bk,map[string]Grant{"results":{Read:true}},clock())
 pairForTest(t,bs,bid,ak,map[string]Grant{"results":{Write:true}},clock())
 src,e:=OpenResults(ar,aid);if e!=nil{t.Fatal(e)}
 dst,e:=OpenResults(br,bid);if e!=nil{t.Fatal(e)}
 full,_:=Preset(Full);mobile,_:=Preset(Mobile)
 start:=func()*PeerListener{
  t.Helper()
  l,e:=StartResultPeer(context.Background(),"127.0.0.1:0",ak,as,src,func()Policy{return full},func()bool{return true},clock);if e!=nil{t.Fatal(e)};t.Cleanup(func(){_ = l.Close()});return l
 }
 l:=start()
 pull:=func(want uint64){
  t.Helper()
  cert,e:=NodeCertificate(bk,clock());if e!=nil{t.Fatal(e)}
  n,e:=PullResults(context.Background(),l.URL(),aid,cert,bs,dst,mobile,true,clock)
  if e!=nil||n!=want{t.Fatalf("cursor=%d want=%d err=%v",n,want,e)}
 }
 publish:=func(id string){t.Helper();if _,e:=src.Publish(FinalResult{JobID:id,State:Completed,Summary:"final"});e!=nil{t.Fatal(e)}}
 publish("first");pull(1)
 hours.Store(25);publish("second");pull(2)
 if e=l.Close();e!=nil{t.Fatal(e)}
 ak,e=OpenNodeIdentity(filepath.Join(root,"a"));if e!=nil{t.Fatal(e)}
 as,e=OpenPeerStore(ap,aid);if e!=nil{t.Fatal(e)}
 bs,e=OpenPeerStore(bp,bid);if e!=nil{t.Fatal(e)}
 src,e=OpenResults(ar,aid);if e!=nil{t.Fatal(e)}
 dst,e=OpenResults(br,bid);if e!=nil{t.Fatal(e)}
 publish("third");l=start();pull(3);pull(3)
 if v,e:=dst.Received(aid,3);e!=nil||v.JobID!="third"{t.Fatal(v,e)}
 if e=as.Revoke(bid);e!=nil{t.Fatal(e)}
 cert,e:=NodeCertificate(bk,clock());if e!=nil{t.Fatal(e)}
 if _,e=PullResults(context.Background(),l.URL(),aid,cert,bs,dst,mobile,true,clock);e==nil{t.Fatal("revoked peer received results")}
 if n,e:=dst.Cursor(aid);e!=nil||n!=3{t.Fatal(n,e)}
}
