package meshstate

import (
 "encoding/json"
 "reflect"
 "testing"
)

// Every delivery order must preserve concurrent heads and suppress
// causally superseded revisions, including duplicate tombstones.
func TestRevisionAllDeliveryOrders(t *testing.T) {
 revisions:=[]Revision{
 {Clock:Vector{"a":1},Value:json.RawMessage("1")},
 {Clock:Vector{"a":2},Value:json.RawMessage("2")},
 {Clock:Vector{"b":1},Deleted:true},
 {Clock:Vector{"a":2,"b":1},Value:json.RawMessage("3")},
 {Clock:Vector{"c":1},Value:json.RawMessage("4")},
 {Clock:Vector{"b":1},Deleted:true},
 }
 want,e:=MergeHeads(revisions,nil);if e!=nil{t.Fatal(e)}
 if len(want)!=2{t.Fatal(want)}
 count:=0
 var visit func(int)
 visit=func(k int){
 if k==len(revisions){
 var heads []Revision
 for _,r:=range revisions{var err error;heads,err=MergeHeads(heads,[]Revision{r});if err!=nil{t.Fatal(err)}}
 if !reflect.DeepEqual(heads,want){t.Fatalf("order-dependent merge: %+v != %+v",heads,want)}
 count++;return
 }
 for i:=k;i<len(revisions);i++{revisions[k],revisions[i]=revisions[i],revisions[k];visit(k+1);revisions[k],revisions[i]=revisions[i],revisions[k]}
 }
 visit(0)
 if count!=720{t.Fatal(count)}
}
