package meshstate

import (
 "crypto/sha256"
 "encoding/hex"
 "errors"
 "io"
 "os"
 "path/filepath"
)

const MaxChunkBytes = 4 << 20
// BlobStore stores immutable content-addressed chunks on a private local
// filesystem. Paths are generated only from validated SHA-256 digests.
// Resource authorization, quotas, manifest references and GC belong to the
// caller. A chunk digest alone is never an authorization capability.
type BlobStore struct{root string}
func OpenBlobStore(root string)(*BlobStore,error){
 if root==""{return nil,errors.New("empty blob root")}
 if e:=os.MkdirAll(root,0700);e!=nil{return nil,e}
 info,e:=os.Lstat(root);if e!=nil{return nil,e}
 if !info.IsDir()||info.Mode()&os.ModeSymlink!=0{return nil,errors.New("invalid blob root")}
 return &BlobStore{root:root},nil
}
func ChunkDigest(data []byte)string{h:=sha256.Sum256(data);return hex.EncodeToString(h[:])}
func (s *BlobStore) path(id string)(string,error){
 if !validID(id){return "",errors.New("invalid chunk digest")}
 return filepath.Join(s.root,id),nil
}
// Put verifies size and digest before atomic publication; failed transfers
// cannot overwrite an existing valid chunk. Repeated identical puts are safe.
func (s *BlobStore) Put(id string,r io.Reader,size int64)error{
 path,e:=s.path(id);if e!=nil{return e}
 if r==nil||size<0||size>MaxChunkBytes{return errors.New("invalid chunk size")}
 f,e:=os.CreateTemp(s.root,".chunk-*");if e!=nil{return e}
 defer os.Remove(f.Name());defer f.Close()
 h:=sha256.New()
 n,e:=io.Copy(io.MultiWriter(f,h),io.LimitReader(r,size+1));if e!=nil{return e}
 if n!=size||hex.EncodeToString(h.Sum(nil))!=id{return errors.New("chunk size or digest mismatch")}
 if e=f.Sync();e!=nil{return e};if e=f.Close();e!=nil{return e}
 // Same-directory hard link publishes without replacing an existing name.
 // An existing chunk must also pass verification before acknowledging it.
 if e=os.Link(f.Name(),path);e!=nil{
 if !errors.Is(e,os.ErrExist){return e}
 if _,e=s.Read(id);e!=nil{return e}
 }
 d,e:=os.Open(s.root);if e!=nil{return e};defer d.Close()
 return d.Sync()
}
func (s *BlobStore) Read(id string)([]byte,error){
 p,e:=s.path(id);if e!=nil{return nil,e}
 info,e:=os.Lstat(p);if e!=nil{return nil,e}
 if !info.Mode().IsRegular()||info.Size()>MaxChunkBytes{return nil,errors.New("invalid chunk file")}
 f,e:=os.Open(p);if e!=nil{return nil,e};defer f.Close()
 // Verify the opened inode matches the inspected regular file.
 actual,e:=f.Stat();if e!=nil{return nil,e}
 if !actual.Mode().IsRegular()||!os.SameFile(info,actual){return nil,errors.New("chunk file changed")}
 b,e:=io.ReadAll(io.LimitReader(f,MaxChunkBytes+1));if e!=nil{return nil,e}
 if len(b)>MaxChunkBytes||ChunkDigest(b)!=id{return nil,errors.New("corrupt chunk")}
 return b,nil
}
type ChunkRef struct {
 Digest string `json:"digest"`
 Size int64 `json:"size"`
}
type BlobManifest struct {
 Size int64 `json:"size"`
 Digest string `json:"digest"`
 Chunks []ChunkRef `json:"chunks"`
}
// ValidateManifest bounds work before reading content. The file's logical
// identity and destination path are intentionally outside the blob manifest.
func ValidateManifest(m BlobManifest)error{
 if !validID(m.Digest)||m.Size<0||len(m.Chunks)>65536{return errors.New("invalid manifest")}
 var total int64
 for _,c:=range m.Chunks{
 if !validID(c.Digest)||c.Size<=0||c.Size>MaxChunkBytes{return errors.New("invalid chunk reference")}
 total+=c.Size
 }
 if total!=m.Size{return errors.New("manifest size mismatch")}
 return nil
}
// Missing returns unique absent chunks in manifest order. Corrupt chunks
// are an error, not an invisible cache hit.
func (s *BlobStore) Missing(m BlobManifest)([]ChunkRef,error){
 if e:=ValidateManifest(m);e!=nil{return nil,e}
 var out []ChunkRef;seen:=map[string]int64{}
 for _,c:=range m.Chunks{
 if n,ok:=seen[c.Digest];ok{if n!=c.Size{return nil,errors.New("conflicting chunk sizes")};continue};seen[c.Digest]=c.Size
 b,e:=s.Read(c.Digest)
 if errors.Is(e,os.ErrNotExist){out=append(out,c);continue}
 if e!=nil{return nil,e}
 if int64(len(b))!=c.Size{return nil,errors.New("chunk reference size mismatch")}
 }
 return out,nil
}
// Assemble writes to a private temporary file, checks the full digest and
// publishes by rename only after verification. The destination is an already
// authorized, local path selected by the host, never a remote wire path.
func (s *BlobStore) Assemble(m BlobManifest,destination string)error{
 if e:=ValidateManifest(m);e!=nil{return e}
 if destination==""{return errors.New("empty destination")}
 dir:=filepath.Dir(destination)
 f,e:=os.CreateTemp(dir,".mesh-assemble-*");if e!=nil{return e}
 defer os.Remove(f.Name());defer f.Close()
 h:=sha256.New();w:=io.MultiWriter(f,h)
 for _,c:=range m.Chunks{
 b,e:=s.Read(c.Digest);if e!=nil{return e}
 if int64(len(b))!=c.Size{return errors.New("chunk size mismatch")}
 if _,e=w.Write(b);e!=nil{return e}
 }
 if hex.EncodeToString(h.Sum(nil))!=m.Digest{return errors.New("file digest mismatch")}
 if e=f.Sync();e!=nil{return e};if e=f.Close();e!=nil{return e}
 if e=os.Rename(f.Name(),destination);e!=nil{return e}
 d,e:=os.Open(dir);if e!=nil{return e};defer d.Close();return d.Sync()
}
