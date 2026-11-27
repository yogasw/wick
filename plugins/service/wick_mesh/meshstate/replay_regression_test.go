package meshstate

import (
 "crypto/ed25519"
 "crypto/rand"
 "path/filepath"
 "os"
 "testing"
 "time"
)

func TestReplayChangingSkew(t *testing.T) {
 pub,key,err:=ed25519.GenerateKey(rand.Reader);if err!=nil{t.Fatal(err)}
 now:=time.Unix(1000,0)
 msg,err:=SignEnvelope(key,"a","b","/jobs",nil,now);if err!=nil{t.Fatal(err)}
 w,err:=NewReplayWindow(8);if err!=nil{t.Fatal(err)}
 if err=w.Verify(msg,pub,"a","b","/jobs",nil,now,time.Second);err!=nil{t.Fatal(err)}
 if err=w.Verify(msg,pub,"a","b","/jobs",nil,now.Add(2*time.Second),time.Minute);err==nil{t.Fatal("replay accepted when skew widened")}
}

func TestReplayClockRollback(t *testing.T) {
 pub,key,err:=ed25519.GenerateKey(rand.Reader);if err!=nil{t.Fatal(err)}
 now:=time.Unix(1000,0)
 a,err:=SignEnvelope(key,"a","b","/jobs",nil,now);if err!=nil{t.Fatal(err)}
 later:=now.Add(10*time.Minute)
 b,err:=SignEnvelope(key,"a","b","/jobs",nil,later);if err!=nil{t.Fatal(err)}
 w,err:=NewReplayWindow(8);if err!=nil{t.Fatal(err)}
 if err=w.Verify(a,pub,"a","b","/jobs",nil,now,time.Minute);err!=nil{t.Fatal(err)}
 if err=w.Verify(b,pub,"a","b","/jobs",nil,later,time.Minute);err!=nil{t.Fatal(err)}
 if err=w.Verify(a,pub,"a","b","/jobs",nil,now,time.Minute);err==nil{t.Fatal("replay accepted after clock rollback")}
}

func TestDurableReplayRestart(t *testing.T) {
 pub,key,err:=ed25519.GenerateKey(rand.Reader);if err!=nil{t.Fatal(err)}
 now:=time.Unix(1000,0)
 path:=filepath.Join(t.TempDir(),"replay.json")
 if err=os.Chmod(filepath.Dir(path),0700);err!=nil{t.Fatal(err)}
 msg,err:=SignEnvelope(key,"a","b","/jobs",nil,now);if err!=nil{t.Fatal(err)}
 w,err:=OpenReplayWindow(path,8);if err!=nil{t.Fatal(err)}
 if err=w.Verify(msg,pub,"a","b","/jobs",nil,now,time.Minute);err!=nil{t.Fatal(err)}
 w,err=OpenReplayWindow(path,8);if err!=nil{t.Fatal(err)}
 if err=w.Verify(msg,pub,"a","b","/jobs",nil,now,time.Minute);err==nil{t.Fatal("replay accepted after restart")}
 later:=now.Add(10*time.Minute)
 next,err:=SignEnvelope(key,"a","b","/jobs",nil,later);if err!=nil{t.Fatal(err)}
 if err=w.Verify(next,pub,"a","b","/jobs",nil,later,time.Minute);err!=nil{t.Fatal(err)}
 w,err=OpenReplayWindow(path,8);if err!=nil{t.Fatal(err)}
 if err=w.Verify(msg,pub,"a","b","/jobs",nil,now,time.Minute);err==nil{t.Fatal("replay accepted after restart and clock rollback")}
}

func TestDurableReplayPoisonAndCorruption(t *testing.T) {
 pub,key,err:=ed25519.GenerateKey(rand.Reader);if err!=nil{t.Fatal(err)}
 now:=time.Unix(1000,0)
 dir:=t.TempDir();if err=os.Chmod(dir,0700);err!=nil{t.Fatal(err)}
 path:=filepath.Join(dir,"replay.json")
 w,err:=OpenReplayWindow(path,8);if err!=nil{t.Fatal(err)}
 if err=os.Mkdir(path,0700);err!=nil{t.Fatal(err)}
 msg,err:=SignEnvelope(key,"a","b","/jobs",nil,now);if err!=nil{t.Fatal(err)}
 if err=w.Verify(msg,pub,"a","b","/jobs",nil,now,time.Minute);err==nil{t.Fatal("admitted despite failed durable write")}
 if err=os.Remove(path);err!=nil{t.Fatal(err)}
 next,err:=SignEnvelope(key,"a","b","/jobs",nil,now);if err!=nil{t.Fatal(err)}
 if err=w.Verify(next,pub,"a","b","/jobs",nil,now,time.Minute);err==nil{t.Fatal("poisoned replay state accepted command")}
 if err=os.WriteFile(path,[]byte("{bad json"),0600);err!=nil{t.Fatal(err)}
 if _,err=OpenReplayWindow(path,8);err==nil{t.Fatal("corrupt replay ledger accepted")}
}
