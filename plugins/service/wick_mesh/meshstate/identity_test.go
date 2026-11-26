package meshstate

import (
 "bytes"
 "crypto/ed25519"
 "os"
 "path/filepath"
 "sync"
 "testing"
)

func TestNodeIdentityRestartAndConcurrentOpen(t *testing.T) {
 dir:=filepath.Join(t.TempDir(),"identity")
 const n=16
 keys:=make([]ed25519.PrivateKey,n)
 errs:=make([]error,n)
 var wg sync.WaitGroup
 for i:=0;i<n;i++ {wg.Add(1);go func(i int){defer wg.Done();keys[i],errs[i]=OpenNodeIdentity(dir)}(i)}
 wg.Wait()
 for i:=range keys {if errs[i]!=nil||len(keys[i])!=ed25519.PrivateKeySize||!bytes.Equal(keys[0],keys[i]){t.Fatalf("open %d: identity mismatch or error: %v",i,errs[i])}}
 key,err:=OpenNodeIdentity(dir)
 if err!=nil||!bytes.Equal(key,keys[0]){t.Fatal("identity changed after reopen",err)}
 info,err:=os.Stat(filepath.Join(dir,"node.seed"))
 if err!=nil||info.Mode().Perm()!=0600||info.Size()!=ed25519.SeedSize{t.Fatal("identity file is not private and bounded",err)}
}

func TestNodeIdentityRejectsUnsafeExistingState(t *testing.T) {
 for _,kind:=range []string{"corrupt","public","symlink","directory"}{t.Run(kind,func(t *testing.T){
  dir:=filepath.Join(t.TempDir(),"identity");if err:=os.Mkdir(dir,0700);err!=nil{t.Fatal(err)}
  path:=filepath.Join(dir,"node.seed")
  switch kind {
  case "corrupt":if err:=os.WriteFile(path,[]byte("bad"),0600);err!=nil{t.Fatal(err)}
  case "public":if err:=os.WriteFile(path,make([]byte,32),0644);err!=nil{t.Fatal(err)}
  case "symlink":target:=filepath.Join(t.TempDir(),"seed");if err:=os.WriteFile(target,make([]byte,32),0600);err!=nil{t.Fatal(err)};if err:=os.Symlink(target,path);err!=nil{t.Fatal(err)}
  case "directory":if err:=os.Mkdir(path,0700);err!=nil{t.Fatal(err)}
  }
  if key,err:=OpenNodeIdentity(dir);err==nil||key!=nil{t.Fatal("unsafe identity accepted")}
  if _,err:=os.Lstat(path);err!=nil{t.Fatal("existing state removed",err)}
 })}
}
