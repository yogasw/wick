package meshstate

import (
 "crypto/ed25519"
 "crypto/rand"
 "crypto/sha256"
 "crypto/subtle"
 "encoding/base64"
 "encoding/hex"
 "encoding/json"
 "errors"
 "os"
 "path/filepath"
 "sync"
 "time"
)

// PeerID pins the node identity to its public key. Key rotation requires
// explicit revocation and re-pairing; there is no implicit key replacement.
func PeerID(key ed25519.PublicKey) string {
 if len(key)!=ed25519.PublicKeySize{return ""}
 h:=sha256.Sum256(key);return hex.EncodeToString(h[:])
}
type Grant struct {
 Read bool `json:"read"`
 Write bool `json:"write"`
}
type Peer struct {
 ID string `json:"id"`
 Key ed25519.PublicKey `json:"key"`
 Grants map[string]Grant `json:"grants"`
 Revoked bool `json:"revoked"`
}
type invitation struct {
 Peer Peer `json:"peer"`
 Expires time.Time `json:"expires"`
}
type peerDisk struct {
 Version int `json:"version"`
 LocalID string `json:"local_id"`
 Peers map[string]Peer `json:"peers"`
 Invitations map[string]invitation `json:"invitations"`
}
// PeerStore is local trust state, never part of replicated application data.
// One process owns the directory. It must live on local private storage.
type PeerStore struct {
 mu sync.Mutex
 path string
 data peerDisk
 poisoned bool
}
func validID(id string)bool{b,e:=hex.DecodeString(id);return e==nil&&len(b)==sha256.Size&&hex.EncodeToString(b)==id}
func clonePeer(p Peer)Peer{
 out:=p;out.Key=append(ed25519.PublicKey(nil),p.Key...);out.Grants=map[string]Grant{}
 for k,v:=range p.Grants{out.Grants[k]=v};return out
}
func validatePeer(p Peer)error{
 if !validID(p.ID)||p.ID!=PeerID(p.Key)||len(p.Grants)>len(KnownResources()){return errors.New("invalid peer")}
 for k,g:=range p.Grants{
 if !g.Read&&!g.Write{return errors.New("empty grant")}
 if e:=ValidateRule(k,Rule{Mode:Replicate,Direction:Both});e!=nil{return e}
 };return nil
}
func OpenPeerStore(path,localID string)(*PeerStore,error){
 if path==""||!validID(localID){return nil,errors.New("invalid peer store")}
 s:=&PeerStore{path:path,data:peerDisk{Version:1,LocalID:localID,Peers:map[string]Peer{},Invitations:map[string]invitation{}}}
 b,e:=os.ReadFile(path);if errors.Is(e,os.ErrNotExist){return s,nil};if e!=nil{return nil,e}
 if e=json.Unmarshal(b,&s.data);e!=nil{return nil,e}
 if s.data.Version!=1||s.data.LocalID!=localID||s.data.Peers==nil||s.data.Invitations==nil{return nil,errors.New("invalid peer store version or owner")}
 for id,p:=range s.data.Peers{if id!=p.ID||id==localID||validatePeer(p)!=nil{return nil,errors.New("invalid stored peer")}}
 for h,i:=range s.data.Invitations{if !validID(h)||i.Peer.ID==localID||i.Peer.Revoked||validatePeer(i.Peer)!=nil||i.Expires.IsZero(){return nil,errors.New("invalid stored invitation")}}
 return s,nil
}
func (s *PeerStore) copy()peerDisk{
 d:=peerDisk{Version:1,LocalID:s.data.LocalID,Peers:map[string]Peer{},Invitations:map[string]invitation{}}
 for k,v:=range s.data.Peers{d.Peers[k]=v}
 for k,v:=range s.data.Invitations{d.Invitations[k]=v}
 return d
}
func (s *PeerStore) save(d peerDisk)error{
 if s.poisoned{return errors.New("peer store needs reopen")}
 b,e:=json.Marshal(d);if e!=nil{return e}
 dir:=filepath.Dir(s.path);if e=os.MkdirAll(dir,0700);e!=nil{return e}
 f,e:=os.CreateTemp(dir,".peers-*");if e!=nil{return e}
 defer os.Remove(f.Name());defer f.Close()
 if _,e=f.Write(b);e!=nil{return e}
 if e=f.Sync();e!=nil{return e};if e=f.Close();e!=nil{return e}
 if e=os.Rename(f.Name(),s.path);e!=nil{return e}
 df,e:=os.Open(dir);if e!=nil{s.poisoned=true;return e}
 e=df.Sync();_ = df.Close();if e!=nil{s.poisoned=true;return e}
 s.data=d;return nil
}
// Invite pins an explicitly approved key and grants. The returned secret
// must be transferred securely; only its hash is stored. Caller must be admin.
func (s *PeerStore) Invite(key ed25519.PublicKey,grants map[string]Grant,now time.Time,ttl time.Duration)(string,error){
 s.mu.Lock();defer s.mu.Unlock()
 if s.poisoned||ttl<=0||ttl>15*time.Minute{return "",errors.New("invalid invitation lifetime")}
 p:=clonePeer(Peer{ID:PeerID(key),Key:key,Grants:grants})
 if p.ID==s.data.LocalID||validatePeer(p)!=nil{return "",errors.New("invalid invited peer")}
 if _,ok:=s.data.Peers[p.ID];ok{return "",errors.New("peer already registered; explicit removal or rotation required")}
 d:=s.copy()
 for k,i:=range d.Invitations{if !now.Before(i.Expires)||i.Peer.ID==p.ID{delete(d.Invitations,k)}}
 if len(d.Invitations)>=1024||len(d.Peers)>=1024{return "",errors.New("peer capacity reached")}
 var secret [32]byte;if _,e:=rand.Read(secret[:]);e!=nil{return "",e}
 h:=sha256.Sum256(secret[:]);d.Invitations[hex.EncodeToString(h[:])]=invitation{Peer:p,Expires:now.Add(ttl)}
 if e:=s.save(d);e!=nil{return "",e};return base64.RawURLEncoding.EncodeToString(secret[:]),nil
}
func pairingMessage(localID,peerID,token string)[]byte{return []byte("wick-mesh-pair-v1\x00"+localID+"\x00"+peerID+"\x00"+token)}
func PairProof(private ed25519.PrivateKey,inviterID,token string)([]byte,error){
 if len(private)!=ed25519.PrivateKeySize||!validID(inviterID){return nil,errors.New("invalid pairing key")}
 b,e:=base64.RawURLEncoding.DecodeString(token);if e!=nil||len(b)!=32{return nil,errors.New("invalid token")}
 id:=PeerID(private.Public().(ed25519.PublicKey))
 return ed25519.Sign(private,pairingMessage(inviterID,id,token)),nil
}
// Redeem verifies possession of the pinned private key and consumes the
// invitation in the same durable write that registers the peer.
func (s *PeerStore) Redeem(token string,proof []byte,now time.Time)(Peer,error){
 s.mu.Lock();defer s.mu.Unlock()
 if s.poisoned{return Peer{},errors.New("peer store unavailable")}
 secret,e:=base64.RawURLEncoding.DecodeString(token);if e!=nil||len(secret)!=32||len(proof)!=ed25519.SignatureSize{return Peer{},errors.New("invalid pairing proof")}
 h:=sha256.Sum256(secret);id:=hex.EncodeToString(h[:])
 i,ok:=s.data.Invitations[id]
 if !ok||!now.Before(i.Expires){return Peer{},errors.New("invalid or expired invitation")}
 if _,ok=s.data.Peers[i.Peer.ID];ok{return Peer{},errors.New("peer already registered")}
 if !ed25519.Verify(i.Peer.Key,pairingMessage(s.data.LocalID,i.Peer.ID,token),proof){return Peer{},errors.New("invalid pairing proof")}
 d:=s.copy();delete(d.Invitations,id);d.Peers[i.Peer.ID]=i.Peer
 if e=s.save(d);e!=nil{return Peer{},e};return clonePeer(i.Peer),nil
}
func (s *PeerStore) Authorize(id,resource string,write bool)(ed25519.PublicKey,error){
 s.mu.Lock();defer s.mu.Unlock()
 p,ok:=s.data.Peers[id];g,allowed:=p.Grants[resource]
 if s.poisoned||!ok||p.Revoked||!allowed||(write&&!g.Write)||(!write&&!g.Read){return nil,errors.New("peer not authorized")}
 return append(ed25519.PublicKey(nil),p.Key...),nil
}
func (s *PeerStore) Revoke(id string)error{
 s.mu.Lock();defer s.mu.Unlock()
 if s.poisoned{return errors.New("peer store unavailable")}
 d:=s.copy();p,ok:=d.Peers[id];if !ok{return errors.New("unknown peer")}
 p.Revoked=true;d.Peers[id]=p
 for k,i:=range d.Invitations{if subtle.ConstantTimeCompare([]byte(i.Peer.ID),[]byte(id))==1{delete(d.Invitations,k)}}
 return s.save(d)
}
