package meshstate

import (
 "bytes"
 "encoding/json"
 "errors"
 "math"
 "sort"
)

// Vector identifies causality without relying on synchronized wall clocks.
type Vector map[string]uint64
type Relation int
const (
 Equal Relation = iota
 Before
 After
 Concurrent
)
func (v Vector) Valid()bool{
 if len(v)==0||len(v)>1024{return false}
 for k,n:=range v{if k==""||len(k)>256||n==0{return false}}
 return true
}
func Compare(a,b Vector)Relation{
 less,greater:=false,false
 for k,n:=range a{if n<b[k]{less=true};if n>b[k]{greater=true}}
 for k,n:=range b{if n>a[k]{less=true};if n<a[k]{greater=true}}
 switch{case less&&greater:return Concurrent;case less:return Before;case greater:return After;default:return Equal}
}
func Join(a,b Vector)Vector{
 out:=Vector{}
 for k,n:=range a{out[k]=n}
 for k,n:=range b{if n>out[k]{out[k]=n}}
 return out
}
func Advance(v Vector,node string)(Vector,error){
 if (len(v)!=0&&!v.Valid())||node==""||len(node)>256||v[node]==math.MaxUint64{return nil,errors.New("invalid revision")}
 out:=Join(v,nil);out[node]++
 if !out.Valid(){return nil,errors.New("revision exceeds limit")}
 return out,nil
}
// Revision is a field value or a tombstone. Concurrent heads are retained
// rather than silently discarding a user's edit. Repeated delivery is safe.
type Revision struct{
 Clock Vector `json:"clock"`
 Deleted bool `json:"deleted"`
 Value json.RawMessage `json:"value,omitempty"`
}
func canonical(r Revision)(Revision,error){
 if !r.Clock.Valid(){return Revision{},errors.New("invalid clock")}
 r.Clock=Join(r.Clock,nil)
 if r.Deleted{if len(r.Value)!=0{return Revision{},errors.New("tombstone must have no value")};return r,nil}
 if len(r.Value)>1<<20||!json.Valid(r.Value){return Revision{},errors.New("invalid value")}
 var b bytes.Buffer
 if e:=json.Compact(&b,r.Value);e!=nil{return Revision{},e};r.Value=b.Bytes()
 return r,nil
}
// MergeHeads returns the causally maximal set, ordered deterministically.
// An equal clock with different content is a protocol violation.
func MergeHeads(a,b []Revision)([]Revision,error){
 if len(a)>1024||len(b)>1024{return nil,errors.New("too many revisions")}
 all:=make([]Revision,0,len(a)+len(b))
 for _,list:=range [][]Revision{a,b}{for _,r:=range list{
  c,e:=canonical(r);if e!=nil{return nil,e}
  duplicate:=false
  for _,x:=range all{if Compare(c.Clock,x.Clock)==Equal{
   if c.Deleted!=x.Deleted||!bytes.Equal(c.Value,x.Value){return nil,errors.New("revision identity collision")}
   duplicate=true;break
  }}
  if !duplicate{all=append(all,c)}
 }}
 heads:=make([]Revision,0,len(all))
 for i,r:=range all{
  dominated:=false
  for j,x:=range all{if i!=j&&Compare(r.Clock,x.Clock)==Before{dominated=true;break}}
  if !dominated{heads=append(heads,r)}
 }
 sort.Slice(heads,func(i,j int)bool{a,_:=json.Marshal(heads[i].Clock);b,_:=json.Marshal(heads[j].Clock);return bytes.Compare(a,b)<0})
 return heads,nil
}
func ResolveHeads(heads []Revision,node string,deleted bool,value json.RawMessage)(Revision,error){
 heads,e:=MergeHeads(heads,nil);if e!=nil{return Revision{},e}
 if len(heads)==0{return Revision{},errors.New("no heads")}
 v:=Vector{}
 for _,h:=range heads{v=Join(v,h.Clock)}
 v,e=Advance(v,node);if e!=nil{return Revision{},e}
 return canonical(Revision{Clock:v,Deleted:deleted,Value:value})
}
