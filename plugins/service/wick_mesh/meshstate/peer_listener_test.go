package meshstate

import (
 "context"
 "crypto/tls"
 "net/http"
 "net/http/httptest"
 "testing"
 "time"
)

func TestPeerListenerLifecycle(t *testing.T) {
 fixture:=httptest.NewTLSServer(http.NotFoundHandler()); defer fixture.Close()
 cert:=fixture.TLS.Certificates[0]
 cfg:=&tls.Config{MinVersion:tls.VersionTLS13,Certificates:[]tls.Certificate{cert},ClientAuth:tls.RequireAnyClientCert,SessionTicketsDisabled:true,VerifyConnection:func(cs tls.ConnectionState)error{return nil}}
 ctx,cancel:=context.WithCancel(context.Background());defer cancel()
 listener,err:=StartPeerListener(ctx,"127.0.0.1:0",cfg,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(204)}))
 if err!=nil{t.Fatal(err)}
 defer listener.Close()
 tr:=fixture.Client().Transport.(*http.Transport).Clone();defer tr.CloseIdleConnections()
 tr.TLSClientConfig.Certificates=[]tls.Certificate{cert}
 client:=&http.Client{Transport:tr,Timeout:2*time.Second}
 resp,err:=client.Get(listener.URL());if err!=nil{t.Fatal(err)};resp.Body.Close()
 if resp.StatusCode!=204{t.Fatal(resp.StatusCode)}
 unauth:=fixture.Client().Transport.(*http.Transport).Clone();defer unauth.CloseIdleConnections()
 if r,e:=(&http.Client{Transport:unauth,Timeout:2*time.Second}).Get(listener.URL());e==nil{r.Body.Close();t.Fatal("peer without certificate accepted")}
 cancel()
 select{case <-listener.Done():case <-time.After(3*time.Second):t.Fatal("listener did not stop")}
 if err:=listener.Close();err!=nil{t.Fatal(err)}
}

func TestPeerListenerRejectsUnsafeTLS(t *testing.T){
 cfg:=&tls.Config{MinVersion:tls.VersionTLS13}
 if l,e:=StartPeerListener(context.Background(),"127.0.0.1:0",cfg,http.NotFoundHandler());e==nil{l.Close();t.Fatal("unsafe TLS accepted")}
}
