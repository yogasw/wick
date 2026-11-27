package meshstate

import (
 "encoding/hex"
 "encoding/json"
 "errors"
 "io"
 "os"
 "path/filepath"
 "strings"
 "time"
)

const maxReplayDiskBytes = 8 << 20
const maxDurableReplayCapacity = 16384

type replayDisk struct {
 Version int `json:"version"`
 Last time.Time `json:"last"`
 Seen map[string]time.Time `json:"seen"`
}

// OpenReplayWindow opens a private durable nonce ledger owned by one process.
// The caller must provide an absolute path inside an existing trusted private
// directory and must never open multiple live instances for the same file.
// Loss of this file invalidates the replay guarantee; do not restore an old
// backup into a live trust domain. Operation receipts are still required.
func OpenReplayWindow(path string,capacity int)(*ReplayWindow,error){
 if !filepath.IsAbs(path)||capacity<1||capacity>maxDurableReplayCapacity{return nil,errors.New("invalid durable replay configuration")}
 dir:=filepath.Dir(path)
 info,err:=os.Lstat(dir);if err!=nil{return nil,err}
 if !info.IsDir()||info.Mode().Perm()&0077!=0{return nil,errors.New("replay directory must be private")}
 r,err:=NewReplayWindow(capacity);if err!=nil{return nil,err};r.path=path
 info,err=os.Lstat(path)
 if errors.Is(err,os.ErrNotExist){return r,nil};if err!=nil{return nil,err}
 if !info.Mode().IsRegular()||info.Mode().Perm()!=0600||info.Size()>maxReplayDiskBytes{return nil,errors.New("invalid replay file")}
 f,err:=os.Open(path);if err!=nil{return nil,err};defer f.Close()
 actual,err:=f.Stat();if err!=nil{return nil,err}
 if !os.SameFile(info,actual)||actual.Mode().Perm()!=0600||actual.Size()>maxReplayDiskBytes{return nil,errors.New("replay file changed during open")}
 var disk replayDisk
 dec:=json.NewDecoder(io.LimitReader(f,maxReplayDiskBytes+1));dec.DisallowUnknownFields()
 if err=dec.Decode(&disk);err!=nil{return nil,err}
 var extra any;if err=dec.Decode(&extra);err!=io.EOF{return nil,errors.New("trailing replay data")}
 if disk.Version!=1||disk.Last.IsZero()||disk.Seen==nil||len(disk.Seen)>capacity{return nil,errors.New("invalid replay state")}
 for id,until:=range disk.Seen{
  i:=strings.LastIndexByte(id,0)
  if i<1||i>128||len(id)-i-1!=64||!until.After(disk.Last)||until.After(disk.Last.Add(10*time.Minute)){return nil,errors.New("invalid replay entry")}
  if _,err=hex.DecodeString(id[i+1:]);err!=nil{return nil,errors.New("invalid replay nonce")}
 }
 r.last=disk.Last;r.seen=disk.Seen
 return r,nil
}

// saveReplay is called with the window locked, before returning admission.
// Any failure poisons the live window; caller must reopen durable state.
func(r *ReplayWindow) saveReplay()error{
 b,err:=json.Marshal(replayDisk{Version:1,Last:r.last,Seen:r.seen});if err!=nil{return err}
 if len(b)>maxReplayDiskBytes{return errors.New("replay ledger capacity exceeded")}
 dir:=filepath.Dir(r.path)
 f,err:=os.CreateTemp(dir,".replay-*");if err!=nil{return err}
 defer os.Remove(f.Name());defer f.Close()
 if _,err=f.Write(b);err!=nil{return err}
 if err=f.Sync();err!=nil{return err}
 if err=f.Close();err!=nil{return err}
 if err=os.Rename(f.Name(),r.path);err!=nil{return err}
 d,err:=os.Open(dir);if err!=nil{return err};defer d.Close()
 return d.Sync()
}
