package meshstate

import (
 "bytes"
 "encoding/json"
 "errors"
 "fmt"
 "os"
 "path/filepath"
 "sync"
 "unicode/utf8"
)

const (
 MaxResultBytes = 32 << 10
 MaxResultPage = 32
 MaxStoredResults = 4096 // fail closed until retention/snapshot support exists
 MaxResultStoreBytes = 32 << 20
)
// FinalResult is intentionally narrower than Job: it cannot carry inputs,
// checkpoints, credentials, or internal execution history. The publishing
// adapter must redact Summary and authorize each recipient; an artifact digest
// is not authorization to fetch the artifact.
type FinalResult struct {
 JobID string `json:"job_id"`
 State State `json:"state"`
 Summary string `json:"summary"`
 Counts map[string]uint64 `json:"counts,omitempty"`
 Artifacts []string `json:"artifacts,omitempty"`
}
type ResultEvent struct{
 Sequence uint64 `json:"sequence"`
 Result FinalResult `json:"result"`
}
type ResultPage struct{
 Origin string `json:"origin"`
 After uint64 `json:"after"`
 Events []ResultEvent `json:"events"`
}
type resultDisk struct{
 Version int `json:"version"`
 Origin string `json:"origin"`
 Local []ResultEvent `json:"local"`
 Remote map[string][]ResultEvent `json:"remote"`
}
// Results is a private, single-process result feed and inbox. Publishing or
// accepting a contiguous page commits data and cursor together. No automatic
// deletion, relay, or core-job publication is enabled by this primitive.
type Results struct{
 mu sync.Mutex
 path string
 disk resultDisk
 poisoned bool
}
func normalizeResult(v FinalResult)(FinalResult,error){
 if v.JobID==""||len(v.JobID)>256||!utf8.ValidString(v.JobID)||len(v.Summary)>16<<10||!utf8.ValidString(v.Summary)||len(v.Counts)>64||len(v.Artifacts)>128{return FinalResult{},errors.New("invalid result")}
 switch v.State{case Completed,Cancelled,Failed:default:return FinalResult{},errors.New("result must be terminal")}
 out:=v
 if len(v.Counts)>0{out.Counts=map[string]uint64{};for k,n:=range v.Counts{if k==""||len(k)>128||!utf8.ValidString(k){return FinalResult{},errors.New("invalid result counter")};out.Counts[k]=n}}else{out.Counts=nil}
 out.Artifacts=append([]string(nil),v.Artifacts...)
 for _,d:=range out.Artifacts{if !validID(d){return FinalResult{},errors.New("invalid artifact digest")}}
 b,e:=json.Marshal(out);if e!=nil||len(b)>MaxResultBytes{return FinalResult{},errors.New("result size limit")}
 return out,nil
}
func sameResult(a,b FinalResult)bool{
 x,_:=json.Marshal(a);y,_:=json.Marshal(b);return bytes.Equal(x,y)
}
func checkEvents(events []ResultEvent)error{
 if len(events)>MaxStoredResults{return errors.New("result capacity exceeded")}
 ids:=map[string]bool{}
 for i,e:=range events{
 if e.Sequence!=uint64(i+1)||ids[e.Result.JobID]{return errors.New("invalid sequence or duplicate job")}
 if _,err:=normalizeResult(e.Result);err!=nil{return err};ids[e.Result.JobID]=true
 }
 return nil
}
func OpenResults(path,origin string)(*Results,error){
 if path==""||!validID(origin){return nil,errors.New("invalid result store identity")}
 s:=&Results{path:path,disk:resultDisk{Version:1,Origin:origin,Remote:map[string][]ResultEvent{}}}
 f,e:=os.Open(path);if errors.Is(e,os.ErrNotExist){return s,nil};if e!=nil{return nil,e};defer f.Close()
 info,e:=f.Stat();if e!=nil{return nil,e}
 if !info.Mode().IsRegular()||info.Size()>MaxResultStoreBytes{return nil,errors.New("invalid result store file")}
 b,e:=os.ReadFile(path);if e!=nil{return nil,e}
 if e=json.Unmarshal(b,&s.disk);e!=nil{return nil,e}
 if s.disk.Version!=1||s.disk.Origin!=origin||s.disk.Remote==nil||len(s.disk.Remote)>1024{return nil,errors.New("invalid result store version or origin")}
 if e=checkEvents(s.disk.Local);e!=nil{return nil,e}
 for peer,events:=range s.disk.Remote{
 if !validID(peer)||peer==origin{return nil,errors.New("invalid result origin")}
 if e=checkEvents(events);e!=nil{return nil,e}
 }
 return s,nil
}
func (s *Results) copy()resultDisk{
 d:=s.disk;d.Local=append([]ResultEvent(nil),d.Local...);d.Remote=map[string][]ResultEvent{}
 for k,v:=range s.disk.Remote{d.Remote[k]=v};return d
}
func (s *Results) save(d resultDisk)error{
 if s.poisoned{return ErrUnavailable}
 b,e:=json.Marshal(d);if e!=nil{return e};if len(b)>MaxResultStoreBytes{return errors.New("result store capacity exceeded")}
 dir:=filepath.Dir(s.path);if e=os.MkdirAll(dir,0700);e!=nil{return e}
 f,e:=os.CreateTemp(dir,".mesh-results-*");if e!=nil{return e}
 defer os.Remove(f.Name());defer f.Close()
 if _,e=f.Write(b);e!=nil{return e}
 if e=f.Sync();e!=nil{return e};if e=f.Close();e!=nil{return e}
 if e=os.Rename(f.Name(),s.path);e!=nil{return e}
 df,e:=os.Open(dir);if e!=nil{s.poisoned=true;return e}
 e=df.Sync();_ = df.Close();if e!=nil{s.poisoned=true;return e}
 s.disk=d;return nil
}
func copyResultEvent(e ResultEvent)ResultEvent{e.Result,_=normalizeResult(e.Result);return e}
// Publish assigns a stable local sequence and deduplicates identical final
// results. A different final envelope for an existing job is a conflict,
// not last-writer-wins. This prototype retains all entries.
func (s *Results) Publish(v FinalResult)(ResultEvent,error){
 v,e:=normalizeResult(v);if e!=nil{return ResultEvent{},e}
 s.mu.Lock();defer s.mu.Unlock()
 if s.poisoned{return ResultEvent{},ErrUnavailable}
 for _,old:=range s.disk.Local{if old.Result.JobID==v.JobID{
 if !sameResult(old.Result,v){return ResultEvent{},ErrConflict};return copyResultEvent(old),nil
 }}
 if len(s.disk.Local)>=MaxStoredResults{return ResultEvent{},errors.New("result capacity exceeded")}
 event:=ResultEvent{Sequence:uint64(len(s.disk.Local)+1),Result:v}
 d:=s.copy();d.Local=append(d.Local,event)
 if e=s.save(d);e!=nil{return ResultEvent{},e};return copyResultEvent(event),nil
}
func (s *Results) Page(after uint64,limit int)(ResultPage,error){
 s.mu.Lock();defer s.mu.Unlock()
 if s.poisoned{return ResultPage{},ErrUnavailable}
 if limit<1||limit>MaxResultPage||after>uint64(len(s.disk.Local)){return ResultPage{},errors.New("invalid result cursor or limit")}
 page:=ResultPage{Origin:s.disk.Origin,After:after,Events:[]ResultEvent{}}
 for _,event:=range s.disk.Local[int(after):]{
 if len(page.Events)>=limit{break};page.Events=append(page.Events,copyResultEvent(event))
 };return page,nil
}
func (s *Results) Cursor(peer string)(uint64,error){
 s.mu.Lock();defer s.mu.Unlock()
 if s.poisoned||!validID(peer)||peer==s.disk.Origin{return 0,errors.New("invalid result cursor")}
 return uint64(len(s.disk.Remote[peer])),nil
}
// ApplyPage must be called with the authenticated publishing node identity.
// It never accepts relayed results, gaps or changed duplicate events. Replaying
// an already committed page is harmless. Cursor derives from committed data.
func (s *Results) ApplyPage(peer string,page ResultPage)(uint64,error){
 s.mu.Lock();defer s.mu.Unlock()
 if s.poisoned||!validID(peer)||peer==s.disk.Origin||page.Origin!=peer||len(page.Events)>MaxResultPage{return 0,errors.New("invalid result page")}
 existing:=s.disk.Remote[peer];cursor:=uint64(len(existing))
 if page.After>cursor{return cursor,errors.New("result page has a gap")}
 if len(page.Events)==0{return cursor,nil}
 if _,ok:=s.disk.Remote[peer];!ok&&len(s.disk.Remote)>=1024{return cursor,errors.New("peer capacity exceeded")}
 events:=append([]ResultEvent(nil),existing...)
 ids:=map[string]bool{};for _,ev:=range events{ids[ev.Result.JobID]=true}
 for i,ev:=range page.Events{
 if ev.Sequence!=page.After+uint64(i)+1{return cursor,errors.New("invalid result sequence")}
 v,e:=normalizeResult(ev.Result);if e!=nil{return cursor,e};ev.Result=v
 if ev.Sequence<=cursor{
 if !sameResult(events[ev.Sequence-1].Result,v){return cursor,ErrConflict};continue
 }
 if ev.Sequence!=uint64(len(events)+1)||ids[v.JobID]||len(events)>=MaxStoredResults{return cursor,errors.New("duplicate job or result capacity exceeded")}
 ids[v.JobID]=true;events=append(events,ev)
 }
 if len(events)==len(existing){return cursor,nil}
 d:=s.copy();d.Remote[peer]=events
 if e:=s.save(d);e!=nil{return cursor,e};return uint64(len(events)),nil
}
func (s *Results) Received(peer string,sequence uint64)(FinalResult,error){
 s.mu.Lock();defer s.mu.Unlock()
 if s.poisoned{return FinalResult{},ErrUnavailable}
 events:=s.disk.Remote[peer]
 if sequence==0||sequence>uint64(len(events)){return FinalResult{},fmt.Errorf("result: %w",ErrNotFound)}
 return normalizeResult(events[sequence-1].Result)
}
