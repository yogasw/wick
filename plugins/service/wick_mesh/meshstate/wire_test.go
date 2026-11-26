package meshstate

import (
 "crypto/ed25519"
 "crypto/rand"
 "testing"
 "time"
)
func TestEnvelopeBindingAndReplay(t *testing.T){
 pub,key,e:=ed25519.GenerateKey(rand.Reader);if e!=nil{t.Fatal(e)}
 now:=time.Unix(100,0);body:=[]byte("message")
 msg,e:=SignEnvelope(key,"a","b","/sync",body,now);if e!=nil{t.Fatal(e)}
 w,e:=NewReplayWindow(2);if e!=nil{t.Fatal(e)}
 for _,c:=range []struct{sender,target,route string;body []byte}{{"x","b","/sync",body},{"a","x","/sync",body},{"a","b","/jobs",body},{"a","b","/sync",[]byte("tampered")}}{
 if e=w.Verify(msg,pub,c.sender,c.target,c.route,c.body,now,time.Second);e==nil{t.Fatal("accepted invalid binding")}
 }
 if e=w.Verify(msg,pub,"a","b","/sync",body,now,time.Second);e!=nil{t.Fatal(e)}
 if e=w.Verify(msg,pub,"a","b","/sync",body,now,time.Second);e==nil{t.Fatal("accepted replay")}
 if e=w.Verify(msg,pub,"a","b","/sync",body,now.Add(time.Second),time.Second);e==nil{t.Fatal("accepted expired envelope")}
}
func TestWrongKeyAndCapacity(t *testing.T){
 pub,key,e:=ed25519.GenerateKey(rand.Reader);if e!=nil{t.Fatal(e)}
 other,_,e:=ed25519.GenerateKey(rand.Reader);if e!=nil{t.Fatal(e)}
 now:=time.Unix(100,0)
 a,e:=SignEnvelope(key,"a","b","/sync",nil,now);if e!=nil{t.Fatal(e)}
 b,e:=SignEnvelope(key,"a","b","/sync",nil,now);if e!=nil{t.Fatal(e)}
 w,e:=NewReplayWindow(1);if e!=nil{t.Fatal(e)}
 if e=w.Verify(a,other,"a","b","/sync",nil,now,time.Second);e==nil{t.Fatal("wrong key")}
 if e=w.Verify(a,pub,"a","b","/sync",nil,now,time.Second);e!=nil{t.Fatal(e)}
 if e=w.Verify(b,pub,"a","b","/sync",nil,now,time.Second);e==nil{t.Fatal("overflow accepted")}
}
func TestNegotiateVersion(t *testing.T){
 if n,e:=NegotiateVersion([]int{1,2,3},[]int{1,2,4});n!=2||e!=nil{t.Fatal(n,e)}
 if _,e:=NegotiateVersion([]int{1},[]int{2});e==nil{t.Fatal("incompatible")}
}
