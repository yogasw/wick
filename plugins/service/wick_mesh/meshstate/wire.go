package meshstate

import (
 "crypto/ed25519"
 "crypto/rand"
 "crypto/sha256"
 "encoding/base64"
 "encoding/hex"
 "encoding/json"
 "errors"
 "strings"
 "sync"
 "time"
)

const WireV1=1
// Envelope binds a signature to one recipient, route, payload and time window.
// It is an application-level primitive, not a substitute for mTLS, peer ACLs,
// durable replay protection or the consensus-backed command log.
type Envelope struct {
 Version int `json:"version"`
 Sender string `json:"sender"`
 Recipient string `json:"recipient"`
 Route string `json:"route"`
 Nonce string `json:"nonce"`
 Timestamp int64 `json:"timestamp"`
 Digest string `json:"digest"`
 Signature string `json:"signature"`
}
func digest(b []byte)string{
 sum:=sha256.Sum256(b);return hex.EncodeToString(sum[:])
}
func (e Envelope) unsigned()([]byte,error){e.Signature="";return json.Marshal(e)}
func (e Envelope) valid()bool{
 if e.Version!=WireV1||e.Sender==""||e.Recipient==""||len(e.Sender)>128||len(e.Recipient)>128||e.Sender==e.Recipient||!strings.HasPrefix(e.Route,"/")||len(e.Route)>1024{return false}
 nonce,err:=hex.DecodeString(e.Nonce);if err!=nil||len(nonce)!=32{return false}
 hash,err:=hex.DecodeString(e.Digest);return err==nil&&len(hash)==sha256.Size
}
func SignEnvelope(key ed25519.PrivateKey,sender,recipient,route string,body []byte,now time.Time)(Envelope,error){
 if len(key)!=ed25519.PrivateKeySize||len(body)>1<<20{return Envelope{},errors.New("invalid signing input")}
 var nonce [32]byte
 if _,err:=rand.Read(nonce[:]);err!=nil{return Envelope{},err}
 e:=Envelope{Version:WireV1,Sender:sender,Recipient:recipient,Route:route,Nonce:hex.EncodeToString(nonce[:]),Timestamp:now.UnixMilli(),Digest:digest(body)}
 if !e.valid(){return Envelope{},errors.New("invalid envelope")}
 b,err:=e.unsigned();if err!=nil{return Envelope{},err}
 e.Signature=base64.RawStdEncoding.EncodeToString(ed25519.Sign(key,b))
 return e,nil
}
// ReplayWindow is process-local unless opened with OpenReplayWindow.
// Durable nonce admission is not an atomic external side-effect receipt.
type ReplayWindow struct {
 mu sync.Mutex
 seen map[string]time.Time
 capacity int
 last time.Time
 path string
 poisoned bool
}
func NewReplayWindow(capacity int)(*ReplayWindow,error){
 if capacity<1||capacity>1000000{return nil,errors.New("invalid replay capacity")}
 return &ReplayWindow{capacity:capacity,seen:make(map[string]time.Time)},nil
}
func (r *ReplayWindow) Verify(e Envelope,key ed25519.PublicKey,expectedSender,recipient,route string,body []byte,now time.Time,skew time.Duration)error{
 if r==nil||!e.valid()||len(key)!=ed25519.PublicKeySize||len(body)>1<<20||skew<=0||skew>5*time.Minute{return errors.New("invalid verification input")}
 if e.Sender!=expectedSender||e.Recipient!=recipient||e.Route!=route||e.Digest!=digest(body){return errors.New("envelope binding mismatch")}
 sent:=time.UnixMilli(e.Timestamp)
 if sent.After(now.Add(skew))||!now.Before(sent.Add(skew)){return errors.New("expired envelope")}
 sig,err:=base64.RawStdEncoding.DecodeString(e.Signature)
 if err!=nil||len(sig)!=ed25519.SignatureSize{return errors.New("invalid signature")}
 b,err:=e.unsigned();if err!=nil||!ed25519.Verify(key,b,sig){return errors.New("invalid signature")}
 r.mu.Lock();defer r.mu.Unlock()
 now=now.UTC()
 if r.poisoned||now.Before(r.last){return errors.New("replay state unavailable or clock rolled back")}
 r.last=now
 for k,until:=range r.seen{if !now.Before(until){delete(r.seen,k)}}
 id:=e.Sender+"\x00"+e.Nonce
 if _,ok:=r.seen[id];ok{return errors.New("replayed envelope")}
 if len(r.seen)>=r.capacity{return errors.New("replay window full")}
 // Retain for the maximum accepted skew, even if this call uses less.
 r.seen[id]=sent.Add(5*time.Minute)
 if r.path!=""{if err:=r.saveReplay();err!=nil{r.poisoned=true;return err}}
 return nil
}
// NegotiateVersion returns the highest exact shared supported wire revision.
// Missing common support is an error, never an implicit downgrade.
func NegotiateVersion(local,remote []int)(int,error){
 if len(local)>64||len(remote)>64{return 0,errors.New("too many wire versions")}
 best:=0
 for _,v:=range local{if v<=0{return 0,errors.New("invalid wire version")}}
 for _,v:=range remote{if v<=0{return 0,errors.New("invalid wire version")}}
 for _,a:=range local{for _,b:=range remote{if a==b&&a>best{best=a}}}
 if best==0{return 0,errors.New("incompatible wire versions")}
 return best,nil
}
