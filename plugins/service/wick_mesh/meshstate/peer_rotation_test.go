package meshstate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"math/big"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// A peer's IP endpoint has no SNI. A rotating certificate callback must
// still be used on every new TLS handshake, without replacing the listener.
func TestPeerListenerDynamicCertificate(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	var seconds atomic.Int64
	clock := func() time.Time { return now.Add(time.Duration(seconds.Load()) * time.Second) }
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	mint := func(serial int64, at time.Time) tls.Certificate {
		t.Helper()
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(serial), NotBefore: at.Add(-time.Minute), NotAfter: at.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, key)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	}
	first := mint(1, now)
	second := mint(2, now.Add(25*time.Hour))
	var renewals atomic.Int64
	get, err := rotatingPeerCertificate(first, clock, func(at time.Time) (tls.Certificate, error) {
		renewals.Add(1)
		if seconds.Load() > 90000 {
			return tls.Certificate{}, errors.New("renewal unavailable")
		}
		return second, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert, SessionTicketsDisabled: true, Time: clock, VerifyConnection: func(tls.ConnectionState) error { return nil }, GetCertificate: get}
	l, err := StartPeerListener(context.Background(), "127.0.0.1:0", cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	check := func(cert tls.Certificate, serial int64) {
		t.Helper()
		// Exact public key and certificate validity checks, not CA/DNS trust.
		cc := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, InsecureSkipVerify: true, Time: clock, VerifyConnection: func(s tls.ConnectionState) error {
			if len(s.PeerCertificates) != 1 {
				return errors.New("wrong certificate count")
			}
			c := s.PeerCertificates[0]
			if !c.PublicKey.(ed25519.PublicKey).Equal(pub) || c.SerialNumber.Int64() != serial || clock().Before(c.NotBefore) || !clock().Before(c.NotAfter) {
				t.Errorf("unexpected certificate: %v", c.SerialNumber)
			}
			return c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature)
		}}
		tr := &http.Transport{TLSClientConfig: cc, DisableKeepAlives: true}
		defer tr.CloseIdleConnections()
		resp, err := (&http.Client{Transport: tr, Timeout: 3 * time.Second}).Get(l.URL())
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 204 {
			t.Fatal(resp.StatusCode)
		}
	}
	check(first, 1)
	seconds.Store(90000)
	check(second, 2)
	check(second, 2)
	if renewals.Load() != 1 {
		t.Fatalf("renewals=%d", renewals.Load())
	}
	seconds.Store(180000)
	if c, e := get(nil); e == nil || c != nil {
		t.Fatal("expired certificate returned after failed renewal")
	}
}
