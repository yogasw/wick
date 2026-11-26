package meshstate

import (
 "crypto/ed25519"
 "crypto/rand"
 "errors"
 "io"
 "os"
 "path/filepath"
)

// OpenNodeIdentity loads or atomically creates the private node seed in a
// dedicated local directory whose parent is trusted and already exists.
// Never replicate this directory or silently replace an invalid identity.
// Requires a filesystem supporting hard links and directory fsync.
func OpenNodeIdentity(dir string)(ed25519.PrivateKey,error){
 if dir==""||!filepath.IsAbs(dir){return nil,errors.New("identity requires an absolute local directory")}
 dir=filepath.Clean(dir)
 if err:=os.Mkdir(dir,0700);err!=nil&&!errors.Is(err,os.ErrExist){return nil,err}
 info,err:=os.Lstat(dir)
 if err!=nil{return nil,err}
 if !info.IsDir()||info.Mode().Perm()&0077!=0{return nil,errors.New("identity directory must be private and not a symlink")}
 if err=syncIdentityDirectory(filepath.Dir(dir));err!=nil{return nil,err}
 path:=filepath.Join(dir,"node.seed")
 key,err:=readNodeIdentity(path)
 if err==nil{if err=syncIdentityDirectory(dir);err!=nil{return nil,err};return key,nil}
 if !errors.Is(err,os.ErrNotExist){return nil,err}
 seed:=make([]byte,ed25519.SeedSize)
 defer clear(seed)
 if _,err=rand.Read(seed);err!=nil{return nil,err}
 f,err:=os.CreateTemp(dir,".node-seed-*")
 if err!=nil{return nil,err}
 defer os.Remove(f.Name());defer f.Close()
 if err=f.Chmod(0600);err!=nil{return nil,err}
 if _,err=f.Write(seed);err!=nil{return nil,err}
 if err=f.Sync();err!=nil{return nil,err}
 if err=f.Close();err!=nil{return nil,err}
 // Link, not rename: simultaneous creators must never replace the winner.
 if err=os.Link(f.Name(),path);err!=nil&&!errors.Is(err,os.ErrExist){return nil,err}
 if err=syncIdentityDirectory(dir);err!=nil{return nil,err}
 return readNodeIdentity(path)
}

func readNodeIdentity(path string)(ed25519.PrivateKey,error){
 info,err:=os.Lstat(path)
 if err!=nil{return nil,err}
 if !info.Mode().IsRegular()||info.Mode().Perm()!=0600||info.Size()!=ed25519.SeedSize{return nil,errors.New("invalid or unsafe node seed")}
 f,err:=os.Open(path)
 if err!=nil{return nil,err};defer f.Close()
 actual,err:=f.Stat()
 if err!=nil{return nil,err}
 if !os.SameFile(info,actual)||!actual.Mode().IsRegular()||actual.Mode().Perm()!=0600||actual.Size()!=ed25519.SeedSize{return nil,errors.New("node seed changed during open")}
 seed,err:=io.ReadAll(io.LimitReader(f,ed25519.SeedSize+1))
 defer clear(seed)
 if err!=nil{return nil,err}
 if len(seed)!=ed25519.SeedSize{return nil,errors.New("invalid node seed length")}
 return ed25519.NewKeyFromSeed(seed),nil
}

func syncIdentityDirectory(path string)error{
 f,err:=os.Open(path);if err!=nil{return err};defer f.Close()
 return f.Sync()
}
