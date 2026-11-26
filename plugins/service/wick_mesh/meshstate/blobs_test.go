package meshstate

import (
 "bytes"
 "os"
 "path/filepath"
 "testing"
)
func TestBlobResumeDedupAndAssembly(t *testing.T){
 dir:=t.TempDir();root:=filepath.Join(dir,"chunks")
 s,e:=OpenBlobStore(root);if e!=nil{t.Fatal(e)}
 a,b:=[]byte("alpha"),[]byte("beta")
 data:=bytes.Join([][]byte{a,b,a},nil)
 m:=BlobManifest{Size:int64(len(data)),Digest:ChunkDigest(data),Chunks:[]ChunkRef{{ChunkDigest(a),int64(len(a))},{ChunkDigest(b),int64(len(b))},{ChunkDigest(a),int64(len(a))}}}
 missing,e:=s.Missing(m);if e!=nil||len(missing)!=2{t.Fatal(missing,e)}
 if e=s.Put(ChunkDigest(a),bytes.NewReader(a),int64(len(a)));e!=nil{t.Fatal(e)}
 s,e=OpenBlobStore(root);if e!=nil{t.Fatal(e)}
 missing,e=s.Missing(m);if e!=nil||len(missing)!=1||missing[0].Digest!=ChunkDigest(b){t.Fatal(missing,e)}
 if e=s.Put(ChunkDigest(b),bytes.NewReader(b),int64(len(b)));e!=nil{t.Fatal(e)}
 if e=s.Put(ChunkDigest(a),bytes.NewReader(a),int64(len(a)));e!=nil{t.Fatal(e)}
 dst:=filepath.Join(dir,"file")
 if e=s.Assemble(m,dst);e!=nil{t.Fatal(e)}
 got,e:=os.ReadFile(dst);if e!=nil||!bytes.Equal(got,data){t.Fatal(string(got),e)}
 m.Digest=ChunkDigest([]byte("bad"))
 if e=s.Assemble(m,dst);e==nil{t.Fatal("invalid manifest accepted")}
 got,e=os.ReadFile(dst);if e!=nil||!bytes.Equal(got,data){t.Fatal("failed assembly replaced file",e)}
}
func TestBlobRejectsCorruptionTraversalAndOversize(t *testing.T){
 s,e:=OpenBlobStore(t.TempDir());if e!=nil{t.Fatal(e)}
 b:=[]byte("abc");id:=ChunkDigest(b)
 if e=s.Put("../escape",bytes.NewReader(b),3);e==nil{t.Fatal("traversal")}
 if e=s.Put(id,bytes.NewReader(b),MaxChunkBytes+1);e==nil{t.Fatal("oversize")}
 if e=s.Put(id,bytes.NewReader(b),2);e==nil{t.Fatal("extra bytes")}
 if e=s.Put(id,bytes.NewReader([]byte("bad")),3);e==nil{t.Fatal("wrong digest")}
 if e=s.Put(id,bytes.NewReader(b),3);e!=nil{t.Fatal(e)}
 if e=os.WriteFile(filepath.Join(s.root,id),[]byte("bad"),0600);e!=nil{t.Fatal(e)}
 if _,e=s.Read(id);e==nil{t.Fatal("corruption accepted")}
 if e=s.Put(id,bytes.NewReader(b),3);e==nil{t.Fatal("corrupt existing file acknowledged")}
}
