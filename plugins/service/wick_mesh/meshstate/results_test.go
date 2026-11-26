package meshstate

import (
 "errors"
 "path/filepath"
 "reflect"
 "testing"
)
func TestResultOnlyOfflineResume(t *testing.T){
 a,b:=ChunkDigest([]byte("vps")),ChunkDigest([]byte("phone"))
 ap,bp:=filepath.Join(t.TempDir(),"a.json"),filepath.Join(t.TempDir(),"b.json")
 src,e:=OpenResults(ap,a);if e!=nil{t.Fatal(e)}
 dst,e:=OpenResults(bp,b);if e!=nil{t.Fatal(e)}
 first:=FinalResult{JobID:"job-1",State:Completed,Summary:"done",Counts:map[string]uint64{"items":7}}
 ev,e:=src.Publish(first);if e!=nil||ev.Sequence!=1{t.Fatal(ev,e)}
 ev.Result.Counts["items"]=99
 if _,e=src.Publish(first);e!=nil{t.Fatal(e)}
 if _,e=src.Publish(FinalResult{JobID:"job-2",State:Running});e==nil{t.Fatal("non-final result accepted")}
 page,e:=src.Page(0,1);if e!=nil{t.Fatal(e)}
 if n,e:=dst.ApplyPage(a,page);e!=nil||n!=1{t.Fatal(n,e)}
 dst,e=OpenResults(bp,b);if e!=nil{t.Fatal(e)}
 if n,e:=dst.ApplyPage(a,page);e!=nil||n!=1{t.Fatal(n,e)}
 if _,e=src.Publish(FinalResult{JobID:"job-2",State:Failed,Summary:"failed"});e!=nil{t.Fatal(e)}
 src,e=OpenResults(ap,a);if e!=nil{t.Fatal(e)}
 cursor,e:=dst.Cursor(a);if e!=nil||cursor!=1{t.Fatal(cursor,e)}
 page,e=src.Page(cursor,MaxResultPage);if e!=nil{t.Fatal(e)}
 if n,e:=dst.ApplyPage(a,page);e!=nil||n!=2{t.Fatal(n,e)}
 got,e:=dst.Received(a,1);if e!=nil||!reflect.DeepEqual(got,first){t.Fatal(got,e)}
 // Receiving results never places them in the local publication stream.
 local,e:=dst.Page(0,MaxResultPage);if e!=nil||len(local.Events)!=0{t.Fatal(local,e)}
}
func TestResultRejectsGapConflictAndSpoof(t *testing.T){
 a,b,c:=ChunkDigest([]byte("a")),ChunkDigest([]byte("b")),ChunkDigest([]byte("c"))
 s,e:=OpenResults(filepath.Join(t.TempDir(),"results.json"),b);if e!=nil{t.Fatal(e)}
 event:=ResultEvent{Sequence:1,Result:FinalResult{JobID:"x",State:Completed}}
 page:=ResultPage{Origin:a,After:0,Events:[]ResultEvent{event}}
 if _,e=s.ApplyPage(c,page);e==nil{t.Fatal("spoofed origin")}
 gap:=page;gap.After=1;gap.Events=[]ResultEvent{{Sequence:2,Result:event.Result}}
 if _,e=s.ApplyPage(a,gap);e==nil{t.Fatal("accepted gap")}
 if _,e=s.ApplyPage(a,page);e!=nil{t.Fatal(e)}
 page.Events[0].Result.Summary="altered"
 if _,e=s.ApplyPage(a,page);!errors.Is(e,ErrConflict){t.Fatal(e)}
 page=ResultPage{Origin:a,After:1,Events:[]ResultEvent{{Sequence:2,Result:FinalResult{JobID:"y",State:Completed}},{Sequence:4,Result:FinalResult{JobID:"z",State:Completed}}}}
 if _,e=s.ApplyPage(a,page);e==nil{t.Fatal("accepted invalid page")}
 if n,e:=s.Cursor(a);e!=nil||n!=1{t.Fatal("partially committed page",n,e)}
}
