package meshstate

import (
 "crypto/ed25519"
 "crypto/rand"
 "os"
 "path/filepath"
 "strings"
 "testing"
 "time"
)
func TestPairingDurabilityAndRevocation(t *testing.T){
 local,_,e:=ed25519.GenerateKey(rand.Reader);if e!=nil{t.Fatal(e)}
 pub,priv,e:=ed25519.GenerateKey(rand.Reader);if e!=nil{t.Fatal(e)}
 _,wrong,e:=ed25519.GenerateKey(rand.Reader);if e!=nil{t.Fatal(e)}
 path:=filepath.Join(t.TempDir(),"peers.json")
 now:=time.Unix(100,0)
 s,e:=OpenPeerStore(path,PeerID(local));if e!=nil{t.Fatal(e)}
 tok,e:=s.Invite(pub,map[string]Grant{"results":{Read:true}},now,time.Minute);if e!=nil{t.Fatal(e)}
 raw,e:=os.ReadFile(path);if e!=nil||strings.Contains(string(raw),tok){t.Fatal("plaintext token stored",e)}
 proof,e:=PairProof(wrong,PeerID(local),tok);if e!=nil{t.Fatal(e)}
 if _,e=s.Redeem(tok,proof,now);e==nil{t.Fatal("wrong key accepted")}
 proof,e=PairProof(priv,PeerID(local),tok);if e!=nil{t.Fatal(e)}
 s,e=OpenPeerStore(path,PeerID(local));if e!=nil{t.Fatal(e)}
 p,e:=s.Redeem(tok,proof,now);if e!=nil||p.ID!=PeerID(pub){t.Fatal(p,e)}
 s,e=OpenPeerStore(path,PeerID(local));if e!=nil{t.Fatal(e)}
 if _,e=s.Redeem(tok,proof,now);e==nil{t.Fatal("reused invitation")}
 if _,e=s.Authorize(p.ID,"results",false);e!=nil{t.Fatal(e)}
 if _,e=s.Authorize(p.ID,"results",true);e==nil{t.Fatal("write allowed")}
 if _,e=s.Authorize(p.ID,"secrets",false);e==nil{t.Fatal("secrets allowed")}
 if e=s.Revoke(p.ID);e!=nil{t.Fatal(e)}
 s,e=OpenPeerStore(path,PeerID(local));if e!=nil{t.Fatal(e)}
 if _,e=s.Authorize(p.ID,"results",false);e==nil{t.Fatal("revoked peer allowed")}
 if _,e=s.Invite(pub,nil,now,time.Minute);e==nil{t.Fatal("revoked key silently re-admitted")}
}
func TestPairingExpiryAndScope(t *testing.T){
 local,_,e:=ed25519.GenerateKey(rand.Reader);if e!=nil{t.Fatal(e)}
 pub,priv,e:=ed25519.GenerateKey(rand.Reader);if e!=nil{t.Fatal(e)}
 now:=time.Unix(100,0);s,e:=OpenPeerStore(filepath.Join(t.TempDir(),"peers.json"),PeerID(local));if e!=nil{t.Fatal(e)}
 if _,e=s.Invite(pub,map[string]Grant{"host_policy":{Read:true}},now,time.Minute);e==nil{t.Fatal("host policy allowed")}
 tok,e:=s.Invite(pub,nil,now,time.Second);if e!=nil{t.Fatal(e)}
 proof,e:=PairProof(priv,PeerID(local),tok);if e!=nil{t.Fatal(e)}
 if _,e=s.Redeem(tok,proof,now.Add(time.Second));e==nil{t.Fatal("expired invite accepted")}
}
