package meshstate

import (
 "crypto/ed25519"
 "crypto/rand"
 "crypto/tls"
 "crypto/x509"
 "crypto/x509/pkix"
 "errors"
 "math/big"
 "time"
)

// NodeCertificate creates a self-signed certificate pinned to a paired node key.
func NodeCertificate(key ed25519.PrivateKey,now time.Time)(tls.Certificate,error){
 if len(key)!=ed25519.PrivateKeySize{return tls.Certificate{},errors.New("invalid node key")}
 serial,e:=rand.Int(rand.Reader,new(big.Int).Lsh(big.NewInt(1),128));if e!=nil{return tls.Certificate{},e}
 if serial.Sign()==0{serial.SetInt64(1)}
 pub:=key.Public().(ed25519.PublicKey)
 tmpl:=&x509.Certificate{SerialNumber:serial,Subject:pkix.Name{CommonName:PeerID(pub)},NotBefore:now.Add(-time.Minute),NotAfter:now.Add(24*time.Hour),KeyUsage:x509.KeyUsageDigitalSignature,ExtKeyUsage:[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth,x509.ExtKeyUsageServerAuth},BasicConstraintsValid:true}
 der,e:=x509.CreateCertificate(rand.Reader,tmpl,tmpl,pub,key);if e!=nil{return tls.Certificate{},e}
 return tls.Certificate{Certificate:[][]byte{der},PrivateKey:key},nil
}
func peerCertificate(cs tls.ConnectionState,now time.Time,usage x509.ExtKeyUsage)(ed25519.PublicKey,error){
 if len(cs.PeerCertificates)!=1{return nil,errors.New("expected one node certificate")}
 c:=cs.PeerCertificates[0];pub,ok:=c.PublicKey.(ed25519.PublicKey)
 if !ok||len(pub)!=ed25519.PublicKeySize||now.Before(c.NotBefore)||!now.Before(c.NotAfter)||c.IsCA||c.KeyUsage&x509.KeyUsageDigitalSignature==0||c.Subject.CommonName!=PeerID(pub){return nil,errors.New("invalid node certificate")}
 if e:=c.CheckSignature(c.SignatureAlgorithm,c.RawTBSCertificate,c.Signature);e!=nil{return nil,e}
 for _,u:=range c.ExtKeyUsage{if u==usage{return pub,nil}}
 return nil,errors.New("certificate usage mismatch")
}
func (s *PeerStore) ActiveKey(id string)(ed25519.PublicKey,error){
 s.mu.Lock();defer s.mu.Unlock()
 p,ok:=s.data.Peers[id];if s.poisoned||!ok||p.Revoked{return nil,errors.New("untrusted peer")}
 return append(ed25519.PublicKey(nil),p.Key...),nil
}
// ServerTLS requires TLS 1.3 and mutual key authentication. Request handlers
// must recheck current grants, including on connections open before revocation.
func ServerTLS(cert tls.Certificate,peers *PeerStore,clock func()time.Time)(*tls.Config,error){
 if peers==nil||clock==nil||len(cert.Certificate)!=1||cert.PrivateKey==nil{return nil,errors.New("invalid server TLS configuration")}
 return &tls.Config{MinVersion:tls.VersionTLS13,Certificates:[]tls.Certificate{cert},ClientAuth:tls.RequireAnyClientCert,SessionTicketsDisabled:true,Time:clock,VerifyConnection:func(cs tls.ConnectionState)error{
 pub,e:=peerCertificate(cs,clock(),x509.ExtKeyUsageClientAuth);if e!=nil{return e};_,e=peers.ActiveKey(PeerID(pub));return e
 }},nil
}
// ClientTLS replaces DNS/CA validation with exact node-key verification.
// Never use this configuration for ordinary external HTTPS endpoints.
func ClientTLS(cert tls.Certificate,peers *PeerStore,target string,clock func()time.Time)(*tls.Config,error){
 if peers==nil||clock==nil||len(cert.Certificate)!=1||cert.PrivateKey==nil{return nil,errors.New("invalid client TLS configuration")}
 if _,e:=peers.ActiveKey(target);e!=nil{return nil,e}
 return &tls.Config{MinVersion:tls.VersionTLS13,Certificates:[]tls.Certificate{cert},InsecureSkipVerify:true,SessionTicketsDisabled:true,Time:clock,VerifyConnection:func(cs tls.ConnectionState)error{
 pub,e:=peerCertificate(cs,clock(),x509.ExtKeyUsageServerAuth);if e!=nil{return e};if PeerID(pub)!=target{return errors.New("unexpected server identity")};_,e=peers.ActiveKey(target);return e
 }},nil
}
func RequestPeer(cs *tls.ConnectionState,peers *PeerStore,resource string,write bool,now time.Time)(string,error){
 if cs==nil||peers==nil||!cs.HandshakeComplete||cs.Version<tls.VersionTLS13{return "",errors.New("mutual TLS required")}
 pub,e:=peerCertificate(*cs,now,x509.ExtKeyUsageClientAuth);if e!=nil{return "",e}
 id:=PeerID(pub);if _,e=peers.Authorize(id,resource,write);e!=nil{return "",e};return id,nil
}
