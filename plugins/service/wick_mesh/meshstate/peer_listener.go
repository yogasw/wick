package meshstate

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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
	server   *http.Server
	done     chan struct{}
	once     sync.Once
	serveErr error
}

func StartPeerListener(ctx context.Context, addr string, cfg *tls.Config, handler http.Handler) (*PeerListener, error) {
	if ctx == nil || ctx.Err() != nil || addr == "" || cfg == nil || handler == nil || cfg.MinVersion < tls.VersionTLS13 || cfg.VerifyConnection == nil || !cfg.SessionTicketsDisabled || (cfg.ClientAuth != tls.RequireAnyClientCert && cfg.ClientAuth != tls.RequireAndVerifyClientCert) || (len(cfg.Certificates) == 0) == (cfg.GetCertificate == nil) || cfg.GetConfigForClient != nil {
		return nil, errors.New("invalid peer listener configuration")
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	p := &PeerListener{listener: l, server: srv, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		err := srv.Serve(tls.NewListener(l, cfg.Clone()))
		if !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			p.serveErr = err
		}
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = p.Close()
		case <-p.done:
		}
	}()
	return p, nil
}
func (p *PeerListener) URL() string           { return "https://" + p.listener.Addr().String() }
func (p *PeerListener) Done() <-chan struct{} { return p.done }
func (p *PeerListener) Close() error {
	p.once.Do(func() { _ = p.server.Close(); _ = p.listener.Close() })
	<-p.done
	return p.serveErr
}

// rotatingPeerCertificate refreshes the leaf before expiry, preserving the
// trusted factory's node identity. A failed renewal fails the handshake rather
// than serving stale credentials. Certificates are immutable after publication.
func rotatingPeerCertificate(initial tls.Certificate, clock func() time.Time, mint func(time.Time) (tls.Certificate, error)) (func(*tls.ClientHelloInfo) (*tls.Certificate, error), error) {
	if clock == nil || mint == nil {
		return nil, errors.New("missing certificate renewal configuration")
	}
	validate := func(c tls.Certificate, now time.Time) (*x509.Certificate, error) {
		if len(c.Certificate) != 1 || c.PrivateKey == nil {
			return nil, errors.New("invalid peer certificate")
		}
		leaf, err := x509.ParseCertificate(c.Certificate[0])
		if err != nil {
			return nil, err
		}
		if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
			return nil, errors.New("peer certificate outside validity period")
		}
		return leaf, nil
	}
	leaf, err := validate(initial, clock())
	if err != nil {
		return nil, err
	}
	current := &initial
	var mu sync.Mutex
	return func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		mu.Lock()
		defer mu.Unlock()
		now := clock()
		if !now.Before(leaf.NotBefore) && now.Add(time.Hour).Before(leaf.NotAfter) {
			return current, nil
		}
		next, err := mint(now)
		if err != nil {
			return nil, err
		}
		parsed, err := validate(next, now)
		if err != nil {
			return nil, err
		}
		if !parsed.NotAfter.After(now.Add(time.Hour)) {
			return nil, errors.New("renewed certificate lifetime too short")
		}
		if string(parsed.RawSubjectPublicKeyInfo) != string(leaf.RawSubjectPublicKeyInfo) {
			return nil, errors.New("certificate renewal changed peer identity")
		}
		current = &next
		leaf = parsed
		return current, nil
	}, nil
}
