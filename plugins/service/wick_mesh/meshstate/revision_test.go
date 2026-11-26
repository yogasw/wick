package meshstate

import (
 "encoding/json"
 "reflect"
 "testing"
)
func TestConcurrentRevisionConvergence(t *testing.T){
 a:=Revision{Clock:Vector{"a":1},Value:json.RawMessage(`"A"`)}
 b:=Revision{Clock:Vector{"b":1},Deleted:true}
 x,e:=MergeHeads([]Revision{a},[]Revision{b});if e!=nil||len(x)!=2{t.Fatal(x,e)}
 y,e:=MergeHeads([]Revision{b,a,a,b},nil);if e!=nil||!reflect.DeepEqual(x,y){t.Fatal(y,e)}
 z,e:=ResolveHeads(x,"a",true,nil);if e!=nil{t.Fatal(e)}
 y,e=MergeHeads([]Revision{z},[]Revision{a,b,a});if e!=nil||len(y)!=1||!y[0].Deleted{t.Fatal(y,e)}
}
func TestRevisionIdentityCollision(t *testing.T){
 a:=Revision{Clock:Vector{"a":1},Value:json.RawMessage(`1`)}
 b:=Revision{Clock:Vector{"a":1},Value:json.RawMessage(`2`)}
 if _,e:=MergeHeads([]Revision{a},[]Revision{b});e==nil{t.Fatal("collision accepted")}
}
func TestVectorAdvanceDoesNotMutate(t *testing.T){
 v:=Vector{"a":1};next,e:=Advance(v,"a");if e!=nil||v["a"]!=1||next["a"]!=2||Compare(v,next)!=Before{t.Fatal(v,next,e)}
}
