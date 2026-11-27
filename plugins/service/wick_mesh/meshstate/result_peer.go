package meshstate

import (
 "context"
 "crypto/ed25519"
 "crypto/tls"
 "errors"
 "time"
)

// StartResultPeer binds the existing pinned-peer TLS and authorized result
// handler to a dedicated listener. No implicit public route or bearer proxy.
// The caller owns the single-process stores and supplies a trusted clock.
// Leaf certificates renew on demand without changing the pinned node key.
func StartResultPeer(ctx context.Context, addr string, key ed25519.PrivateKey, peers *PeerStore, results *Results, policy func()Policy, wifi func()bool, clock func()time.Time)(*PeerListener,error){
 if len(key)!=ed25519.PrivateKeySize||peers==nil||results==nil||clock==nil||PeerID(key.Public().(ed25519.PublicKey))!=peerLocalID(peers){return nil,errors.New("result peer identity mismatch")}
 handler,err:=ResultHandler(results,peers,policy,wifi,clock);if err!=nil{return nil,err}
 key=append(ed25519.PrivateKey(nil),key...)
 cert,err:=NodeCertificate(key,clock());if err!=nil{return nil,err}
 cfg,err:=ServerTLS(cert,peers,clock);if err!=nil{return nil,err}
 get,err:=rotatingPeerCertificate(cert,clock,func(now time.Time)(tls.Certificate,error){return NodeCertificate(key,now)});if err!=nil{return nil,err}
 cfg.Certificates=nil;cfg.GetCertificate=get
 return StartPeerListener(ctx,addr,cfg,handler)
}
