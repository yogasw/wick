package meshstate

import (
 "encoding/json"
 "errors"
 "path/filepath"
 "sync"
 "sync/atomic"
 "testing"
 "time"
)
func TestDurableRecoveryAndFence(t *testing.T){
 p:=filepath.Join(t.TempDir(),"jobs.json")
 s,e:=OpenLocalJobs(p);if e!=nil{t.Fatal(e)}
 now:=time.Unix(100,0)
 if _,e=s.Accept("job",json.RawMessage(`{"x":1}`));e!=nil{t.Fatal(e)}
 if _,e=s.Accept("job",json.RawMessage(`{ "x":1 }`));e!=nil{t.Fatal(e)}
 if _,e=s.Accept("job",json.RawMessage(`{"x":2}`));!errors.Is(e,ErrConflict){t.Fatal(e)}
 a,e:=s.Claim("job","a",now,time.Second);if e!=nil{t.Fatal(e)}
 if _,e=s.Checkpoint("job","a",a.Fence,now,json.RawMessage(`{"done":7}`));e!=nil{t.Fatal(e)}
 s,e=OpenLocalJobs(p);if e!=nil{t.Fatal(e)}
 if _,e=s.Claim("job","b",now,time.Second);!errors.Is(e,ErrUnavailable){t.Fatal(e)}
 b,e:=s.Claim("job","b",now.Add(time.Second),time.Minute);if e!=nil{t.Fatal(e)}
 if b.Fence<=a.Fence||string(b.Checkpoint)!=`{"done":7}`{t.Fatal(b)}
 if _,e=s.Finish("job","a",a.Fence,now.Add(time.Second),Completed,json.RawMessage(`null`));!errors.Is(e,ErrLease){t.Fatal(e)}
 if _,e=s.Finish("job","b",b.Fence,now.Add(time.Second),Completed,json.RawMessage(`{"ok":true}`));e!=nil{t.Fatal(e)}
 s,e=OpenLocalJobs(p);if e!=nil{t.Fatal(e)}
 j,e:=s.Get("job");if e!=nil||j.State!=Completed||string(j.Result)!=`{"ok":true}`{t.Fatal(j,e)}
}
func TestConcurrentClaimAndCancel(t *testing.T){
 s,e:=OpenLocalJobs(filepath.Join(t.TempDir(),"jobs.json"));if e!=nil{t.Fatal(e)}
 if _,e=s.Accept("id",json.RawMessage(`null`));e!=nil{t.Fatal(e)}
 now:=time.Unix(100,0);var wins atomic.Int32;var wg sync.WaitGroup
 for i:=0;i<32;i++{wg.Add(1);go func(){defer wg.Done();if _,e:=s.Claim("id","worker",now,time.Minute);e==nil{wins.Add(1)}}()}
 wg.Wait();if wins.Load()!=1{t.Fatal(wins.Load())}
 j,e:=s.Get("id");if e!=nil{t.Fatal(e)}
 if _,e=s.Cancel("id");e!=nil{t.Fatal(e)}
 if _,e=s.Checkpoint("id","worker",j.Fence,now,json.RawMessage(`null`));!errors.Is(e,ErrLease){t.Fatal(e)}
}
