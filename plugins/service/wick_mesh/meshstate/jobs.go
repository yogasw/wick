package meshstate

import (
 "bytes"
 "encoding/json"
 "errors"
 "fmt"
 "math"
 "os"
 "path/filepath"
 "sync"
 "time"
)

var (
 ErrConflict = errors.New("idempotency conflict")
 ErrNotFound = errors.New("job not found")
 ErrLease = errors.New("invalid or expired lease")
 ErrUnavailable = errors.New("job unavailable")
)
type State string
const (
 Pending State = "pending"
 Running State = "running"
 Completed State = "completed"
 Cancelled State = "cancelled"
 Failed State = "failed"
)
type Job struct {
 ID string `json:"id"`
 Input json.RawMessage `json:"input"`
 State State `json:"state"`
 Owner string `json:"owner,omitempty"`
 Fence uint64 `json:"fence"`
 Deadline time.Time `json:"deadline"`
 Attempts uint64 `json:"attempts"`
 Checkpoint json.RawMessage `json:"checkpoint,omitempty"`
 Result json.RawMessage `json:"result,omitempty"`
}
type diskJobs struct {
 Version int `json:"version"`
 Jobs map[string]Job `json:"jobs"`
}
// LocalJobs is a single-process durable prototype. It must never be shared
// among processes or used as a distributed lease authority. Quorum-backed
// replicated coordination must replace it before remote workers are enabled.
type LocalJobs struct {
 mu sync.Mutex
 path string
 data diskJobs
 poisoned bool
}
func OpenLocalJobs(path string)(*LocalJobs,error){
 if path==""{return nil,errors.New("empty store path")}
 s:=&LocalJobs{path:path,data:diskJobs{Version:1,Jobs:map[string]Job{}}}
 b,e:=os.ReadFile(path)
 if errors.Is(e,os.ErrNotExist){return s,nil}
 if e!=nil{return nil,e}
 if e=json.Unmarshal(b,&s.data);e!=nil{return nil,e}
 if s.data.Version!=1||s.data.Jobs==nil{return nil,errors.New("unsupported store")}
 for id,j:=range s.data.Jobs{if id!=j.ID||id==""||!json.Valid(j.Input){return nil,errors.New("invalid job record")}}
 return s,nil
}
func cloneJob(j Job)Job{
 j.Input=bytes.Clone(j.Input);j.Checkpoint=bytes.Clone(j.Checkpoint);j.Result=bytes.Clone(j.Result)
 return j
}
func (s *LocalJobs) save(j Job)error{
 if s.poisoned{return errors.New("store needs reopen after uncertain durability")}
 next:=diskJobs{Version:1,Jobs:make(map[string]Job,len(s.data.Jobs)+1)}
 for k,v:=range s.data.Jobs{next.Jobs[k]=v};next.Jobs[j.ID]=cloneJob(j)
 b,e:=json.Marshal(next);if e!=nil{return e}
 dir:=filepath.Dir(s.path)
 if e=os.MkdirAll(dir,0700);e!=nil{return e}
 f,e:=os.CreateTemp(dir,".mesh-jobs-*");if e!=nil{return e}
 defer os.Remove(f.Name())
 defer f.Close()
 if _,e=f.Write(b);e!=nil{return e}
 if e=f.Sync();e!=nil{return e}
 if e=f.Close();e!=nil{return e}
 if e=os.Rename(f.Name(),s.path);e!=nil{return e}
 // Once rename succeeds, a directory sync failure has an uncertain outcome.
 // Fail closed rather than accepting another mutation against stale memory.
 d,e:=os.Open(dir);if e!=nil{s.poisoned=true;return e}
 e=d.Sync();_ = d.Close()
 if e!=nil{s.poisoned=true;return e}
 s.data=next
 return nil
}
func (s *LocalJobs) Get(id string)(Job,error){
 s.mu.Lock();defer s.mu.Unlock()
 if s.poisoned{return Job{},ErrUnavailable}
 j,ok:=s.data.Jobs[id];if !ok{return Job{},ErrNotFound};return cloneJob(j),nil
}
func (s *LocalJobs) Accept(id string,input json.RawMessage)(Job,error){
 s.mu.Lock();defer s.mu.Unlock()
 if s.poisoned{return Job{},ErrUnavailable}
 if id==""||len(id)>256||len(input)>1<<20||!json.Valid(input){return Job{},errors.New("invalid job input")}
 var normalized bytes.Buffer
 if e:=json.Compact(&normalized,input);e!=nil{return Job{},e}
 if j,ok:=s.data.Jobs[id];ok{
  if !bytes.Equal(j.Input,normalized.Bytes()){return Job{},ErrConflict}
  return cloneJob(j),nil
 }
 j:=Job{ID:id,Input:normalized.Bytes(),State:Pending}
 if e:=s.save(j);e!=nil{return Job{},e};return cloneJob(j),nil
}
func (s *LocalJobs) Claim(id,owner string,now time.Time,ttl time.Duration)(Job,error){
 s.mu.Lock();defer s.mu.Unlock()
 if s.poisoned{return Job{},ErrUnavailable}
 j,ok:=s.data.Jobs[id];if !ok{return Job{},ErrNotFound}
 if owner==""||ttl<=0||ttl>time.Hour{return Job{},ErrLease}
 if j.State!=Pending&&(j.State!=Running||now.Before(j.Deadline)){return Job{},ErrUnavailable}
 if j.Fence==math.MaxUint64||j.Attempts==math.MaxUint64{return Job{},ErrUnavailable}
 j.Owner=owner;j.Fence++;j.Attempts++;j.State=Running;j.Deadline=now.Add(ttl)
 if e:=s.save(j);e!=nil{return Job{},e};return cloneJob(j),nil
}
func (s *LocalJobs) mutateLease(id,owner string,fence uint64,now time.Time,fn func(*Job)error)(Job,error){
 s.mu.Lock();defer s.mu.Unlock()
 if s.poisoned{return Job{},ErrUnavailable}
 j,ok:=s.data.Jobs[id];if !ok{return Job{},ErrNotFound}
 if j.State!=Running||j.Owner!=owner||j.Fence!=fence||!now.Before(j.Deadline){return Job{},ErrLease}
 if e:=fn(&j);e!=nil{return Job{},e}
 if e:=s.save(j);e!=nil{return Job{},e};return cloneJob(j),nil
}
func (s *LocalJobs) Renew(id,owner string,fence uint64,now time.Time,ttl time.Duration)(Job,error){
 return s.mutateLease(id,owner,fence,now,func(j *Job)error{
 if ttl<=0||ttl>time.Hour{return ErrLease}
 deadline:=now.Add(ttl);if deadline.After(j.Deadline){j.Deadline=deadline};return nil
 })
}
func validPayload(b json.RawMessage)bool{return len(b)<=1<<20&&json.Valid(b)}
func (s *LocalJobs) Checkpoint(id,owner string,fence uint64,now time.Time,b json.RawMessage)(Job,error){
 return s.mutateLease(id,owner,fence,now,func(j *Job)error{
 if !validPayload(b){return errors.New("invalid checkpoint")};j.Checkpoint=bytes.Clone(b);return nil
 })
}
func (s *LocalJobs) Finish(id,owner string,fence uint64,now time.Time,state State,b json.RawMessage)(Job,error){
 return s.mutateLease(id,owner,fence,now,func(j *Job)error{
 if state!=Completed&&state!=Failed&&state!=Pending{return fmt.Errorf("invalid finish state %q",state)}
 if !validPayload(b){return errors.New("invalid result")}
 j.State=state;j.Owner="";j.Deadline=time.Time{}
 if state==Pending{j.Result=nil}else{j.Result=bytes.Clone(b)};return nil
 })
}
func (s *LocalJobs) Cancel(id string)(Job,error){
 s.mu.Lock();defer s.mu.Unlock()
 if s.poisoned{return Job{},ErrUnavailable}
 j,ok:=s.data.Jobs[id];if !ok{return Job{},ErrNotFound}
 if j.State==Cancelled{return cloneJob(j),nil}
 if j.State!=Pending&&j.State!=Running{return Job{},ErrUnavailable}
 j.State=Cancelled;j.Owner="";j.Deadline=time.Time{}
 if e:=s.save(j);e!=nil{return Job{},e};return cloneJob(j),nil
}
