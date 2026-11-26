package meshstate

import (
 "context"
 "crypto/tls"
 "errors"
 "net"
 "net/http"
 "sync"
 "time"
)

// PeerListener owns a dedicated TLS socket, never the ordinary Wick proxy.
// The caller supplies the pinned-peer TLS config and authorized handler.
// Cancellation closes active connections; durable work must be restartable.
type PeerListener struct {
 listener net.Listener
 server *http.Server
 done chan struct{}
 once sync.Once
 serveErr error
}

func StartPeerListener(ctx context.Context, addr string, cfg *tls.Config, handler http.Handler)(*PeerListener,error){
 if ctx==nil||ctx.Err()!=nil||addr==""||cfg==nil||handler==nil||cfg.MinVersion<tls.VersionTLS13||cfg.VerifyConnection==nil||!cfg.SessionTicketsDisabled||(cfg.ClientAuth!=tls.RequireAnyClientCert&&cfg.ClientAuth!=tls.RequireAndVerifyClientCert)||len(cfg.Certificates)==0||cfg.GetConfigForClient!=nil{return nil,errors.New("invalid peer listener configuration")}
 l,err:=net.Listen("tcp",addr);if err!=nil{return nil,err}
 srv:=&http.Server{Handler:handler,ReadHeaderTimeout:5*time.Second,ReadTimeout:15*time.Second,WriteTimeout:30*time.Second,IdleTimeout:30*time.Second,MaxHeaderBytes:16<<10,BaseContext:func(net.Listener)context.Context{return ctx}}
 p:=&PeerListener{listener:l,server:srv,done:make(chan struct{})}
 go func(){defer close(p.done);err:=srv.Serve(tls.NewListener(l,cfg.Clone()));if !errors.Is(err,http.ErrServerClosed)&&!errors.Is(err,net.ErrClosed){p.serveErr=err}}()
 go func(){select{case <-ctx.Done():_ = p.Close();case <-p.done:}}()
 return p,nil
}
func(p *PeerListener) URL()string{return "https://"+p.listener.Addr().String()}
func(p *PeerListener) Done()<-chan struct{}{return p.done}
func(p *PeerListener) Close()error{p.once.Do(func(){_ = p.server.Close();_ = p.listener.Close()});<-p.done;return p.serveErr}
