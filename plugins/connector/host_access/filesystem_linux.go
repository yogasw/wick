//go:build linux

package main

import (
 "crypto/rand"
 "crypto/sha256"
 "encoding/hex"
 "errors"
 "fmt"
 "io"
 "os"
 "path/filepath"
 "strings"
 "sync"

 "golang.org/x/sys/unix"
)

const maxFileBytes = 1<<20
var fileMu sync.Mutex

// Walk directory descriptors without following any symlink. Never use a
// checked path in a second path-based syscall. Android supports openat.
func openDir(name string)(int,error){
 fd,err:=unix.Open("/",unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC,0)
 if err!=nil{return -1,err}
 for _,part:=range strings.Split(strings.TrimPrefix(name,"/"),"/"){
  if part==""{continue}
  next,e:=unix.Openat(fd,part,unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,0)
  unix.Close(fd);if e!=nil{return -1,e};fd=next
 }
 return fd,nil
}
func parentFD(name string)(int,string,error){
 if name=="/"{return -1,"",errors.New("root cannot be modified")}
 fd,err:=openDir(filepath.Dir(name));return fd,filepath.Base(name),err
}
func openRegular(name string)(*os.File,error){
 fd,base,err:=parentFD(name);if err!=nil{return nil,err};defer unix.Close(fd)
 f,err:=unix.Openat(fd,base,unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC,0)
 if err!=nil{return nil,err}
 var st unix.Stat_t
 if err=unix.Fstat(f,&st);err!=nil||st.Mode&unix.S_IFMT!=unix.S_IFREG||st.Nlink!=1{
  unix.Close(f);return nil,errors.New("only regular files with one hard link are supported")
 }
 return os.NewFile(uintptr(f),name),nil
}
func readFile(name string)([]byte,error){
 f,err:=openRegular(name);if err!=nil{return nil,err};defer f.Close()
 b,err:=io.ReadAll(io.LimitReader(f,maxFileBytes+1));if err!=nil{return nil,err}
 if len(b)>maxFileBytes{return nil,errors.New("file exceeds 1 MiB limit")}
 return b,nil
}
func digest(b []byte)string{h:=sha256.Sum256(b);return hex.EncodeToString(h[:])}
func statFile(name string)(map[string]any,error){
 fd,err:=openDir(name)
 if err==nil {defer unix.Close(fd);return map[string]any{"path":name,"type":"directory"},nil}
 f,err:=openRegular(name);if err!=nil{return nil,err};defer f.Close()
 s,err:=f.Stat();if err!=nil{return nil,err}
 return map[string]any{"path":name,"type":"file","size":s.Size(),"modified":s.ModTime(),"mode":s.Mode().Perm().String()},nil
}
func listDir(name string, limit int)([]string,bool,error){
 fd,err:=openDir(name);if err!=nil{return nil,false,err}
 f:=os.NewFile(uintptr(fd),name);defer f.Close()
 names,err:=f.Readdirnames(limit+1);if err!=nil&&err!=io.EOF{return nil,false,err}
 more:=len(names)>limit;if more{names=names[:limit]};return names,more,nil
}
func writeNew(name string,b []byte)error{
 if len(b)>maxFileBytes{return errors.New("content too large")}
 fd,base,err:=parentFD(name);if err!=nil{return err};defer unix.Close(fd)
 n,err:=unix.Openat(fd,base,unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC,0600);if err!=nil{return err}
 f:=os.NewFile(uintptr(n),name);_,err=f.Write(b);if err==nil{err=f.Sync()};e:=f.Close();if err==nil{err=e}
 if err!=nil{_ = unix.Unlinkat(fd,base,0)};return err
}
// Replace atomically in the same parent, with expected hash and inode recheck.
// This is optimistic concurrency, not a lock against unrelated OS processes.
func replaceFile(name string,b []byte,expected string)error{
 if expected==""{return errors.New("expected_sha256 is required")}
 if len(b)>maxFileBytes{return errors.New("content too large")}
 fd,base,err:=parentFD(name);if err!=nil{return err};defer unix.Close(fd)
 n,err:=unix.Openat(fd,base,unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC,0);if err!=nil{return err}
 f:=os.NewFile(uintptr(n),name);defer f.Close()
 var before unix.Stat_t
 if err=unix.Fstat(n,&before);err!=nil{return err}
 if before.Mode&unix.S_IFMT!=unix.S_IFREG||before.Nlink!=1{return errors.New("not a regular single-link file")}
 old,err:=io.ReadAll(io.LimitReader(f,maxFileBytes+1));if err!=nil{return err}
 if len(old)>maxFileBytes||digest(old)!=expected{return errors.New("expected_sha256 mismatch or file too large")}
 var r [12]byte;if _,err=rand.Read(r[:]);err!=nil{return err}
 tmp:=".host-access-"+hex.EncodeToString(r[:])
 out,err:=unix.Openat(fd,tmp,unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC,before.Mode&0777);if err!=nil{return err}
 defer unix.Unlinkat(fd,tmp,0)
 wf:=os.NewFile(uintptr(out),tmp);_,err=wf.Write(b);if err==nil{err=wf.Sync()};e:=wf.Close();if err==nil{err=e};if err!=nil{return err}
 var now unix.Stat_t
 if err=unix.Fstatat(fd,base,&now,unix.AT_SYMLINK_NOFOLLOW);err!=nil{return err}
 if before.Dev!=now.Dev||before.Ino!=now.Ino||before.Size!=now.Size||before.Mtim!=now.Mtim||before.Ctim!=now.Ctim||now.Nlink!=1{return errors.New("file changed during edit")}
 return unix.Renameat(fd,tmp,fd,base)
}
func mkdir(name string)error{
 fd,b,err:=parentFD(name);if err!=nil{return err};defer unix.Close(fd);return unix.Mkdirat(fd,b,0700)
}
func removeFile(name string,directory bool)error{
 fd,b,err:=parentFD(name);if err!=nil{return err};defer unix.Close(fd)
 var st unix.Stat_t;if err=unix.Fstatat(fd,b,&st,unix.AT_SYMLINK_NOFOLLOW);err!=nil{return err}
 if directory{if st.Mode&unix.S_IFMT!=unix.S_IFDIR{return errors.New("not a directory")};return unix.Unlinkat(fd,b,unix.AT_REMOVEDIR)}
 if st.Mode&unix.S_IFMT!=unix.S_IFREG||st.Nlink!=1{return errors.New("not a regular single-link file")}
 return unix.Unlinkat(fd,b,0)
}
func diskUsage(name string)(map[string]any,error){
 fd,err:=openDir(name);if err!=nil{return nil,err};defer unix.Close(fd)
 var s unix.Statfs_t;if err=unix.Fstatfs(fd,&s);err!=nil{return nil,err}
 return map[string]any{"path":name,"total_bytes":uint64(s.Blocks)*uint64(s.Bsize),"available_bytes":uint64(s.Bavail)*uint64(s.Bsize),"free_bytes":uint64(s.Bfree)*uint64(s.Bsize)},nil
}
func fileUnsupported()error{return fmt.Errorf("filesystem not supported")}
