package a2aremote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Guard decides which hosts wick may call as an A2A client. The checks run
// twice: on the URL a person typed (a clear error up front) and again on
// every dial, against the address the name actually resolved to, so a DNS
// answer that changes after the check still cannot reach a blocked IP.
type Guard struct {
	// Allowed is the admin allowlist (agents.a2a_remote_allowed_hosts).
	// Empty = any public host. Non-empty = only these hosts, and a listed
	// host may then resolve to a loopback or link-local address too (a
	// sidecar agent on the same machine). "*.example.com" matches every
	// subdomain.
	Allowed []string
}

// ErrBlockedHost is returned for a host the guard refuses.
var ErrBlockedHost = errors.New("host is not allowed")

// ParseAllowlist splits the admin knob: comma, space or newline separated.
func ParseAllowlist(raw string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t' }) {
		if f = strings.ToLower(strings.TrimSpace(f)); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// listed reports whether host is on the allowlist.
func (g Guard) listed(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, a := range g.Allowed {
		if a == host {
			return true
		}
		if strings.HasPrefix(a, "*.") && strings.HasSuffix(host, a[1:]) {
			return true
		}
	}
	return false
}

// metadataIPs are cloud metadata services outside the link-local range.
var metadataIPs = []net.IP{
	net.ParseIP("fd00:ec2::254"),   // AWS IMDS over IPv6
	net.ParseIP("100.100.100.200"), // Alibaba Cloud
}

// metadataHosts are metadata names refused before any lookup.
var metadataHosts = map[string]bool{
	"metadata.google.internal": true, "metadata": true, "instance-data": true,
	"localhost": true, "localhost.localdomain": true,
}

// blockedIP reports an address the guard never dials for a host that is
// not allowlisted: loopback, link-local (169.254.0.0/16 holds the common
// metadata service), unspecified, multicast and the metadata IPs above.
// Private ranges are allowed: an agent on the company network is a normal
// thing to connect.
func blockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	for _, m := range metadataIPs {
		if m.Equal(ip) {
			return true
		}
	}
	return false
}

// CheckURL validates a URL typed by a person: http/https, a host, and a
// host the guard allows. It does not resolve names; the dialer does.
func (g Guard) CheckURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("URL must start with http:// or https://")
	}
	if u.User != nil {
		return nil, errors.New("URL must not carry credentials; use the auth fields")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return nil, errors.New("URL has no host")
	}
	if len(g.Allowed) > 0 {
		if !g.listed(host) {
			return nil, fmt.Errorf("%w: %s is not on the admin allowlist", ErrBlockedHost, host)
		}
		return u, nil
	}
	if metadataHosts[strings.TrimSuffix(host, ".")] || strings.HasSuffix(host, ".localhost") {
		return nil, fmt.Errorf("%w: %s", ErrBlockedHost, host)
	}
	if ip := net.ParseIP(host); ip != nil && blockedIP(ip) {
		return nil, fmt.Errorf("%w: %s is a loopback, link-local or metadata address", ErrBlockedHost, host)
	}
	return u, nil
}

// dialContext refuses, at connect time, a blocked address for a host that
// is not allowlisted.
func (g Guard) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if len(g.Allowed) > 0 {
		if !g.listed(host) {
			return nil, fmt.Errorf("%w: %s is not on the admin allowlist", ErrBlockedHost, host)
		}
		return d.DialContext(ctx, network, addr)
	}
	d.Control = func(_, address string, _ syscall.RawConn) error {
		h, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if ip := net.ParseIP(h); ip == nil || blockedIP(ip) {
			return fmt.Errorf("%w: %s resolves to a loopback, link-local or metadata address", ErrBlockedHost, host)
		}
		return nil
	}
	return d.DialContext(ctx, network, addr)
}

// HTTPClient is the client every remote call goes through: guarded dials,
// redirects re-checked, the auth header added and each response body
// capped at maxBody bytes.
func (g Guard) HTTPClient(auth PlainAuth, maxBody int64) *http.Client {
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           g.dialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       60 * time.Second,
	}
	return &http.Client{
		Transport: authTransport{base: tr, auth: auth, maxBody: maxBody},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if _, err := g.CheckURL(req.URL.String()); err != nil {
				return err
			}
			// A redirect to another host does not take the secret along.
			if req.URL.Host != via[0].URL.Host {
				req.Header.Del("Authorization")
				if auth.Header != "" {
					req.Header.Del(auth.Header)
				}
			}
			return nil
		},
	}
}

// authTransport adds the auth header and caps the body.
type authTransport struct {
	base    http.RoundTripper
	auth    PlainAuth
	maxBody int64
}

func (t authTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.auth.Secret != "" && r.Header.Get("Authorization") == "" {
		r = r.Clone(r.Context())
		switch t.auth.Type {
		case AuthBearer:
			r.Header.Set("Authorization", "Bearer "+t.auth.Secret)
		case AuthAPIKey:
			r.Header.Set(t.auth.Header, t.auth.Secret)
		}
	}
	res, err := t.base.RoundTrip(r)
	if err != nil || t.maxBody <= 0 {
		return res, err
	}
	res.Body = &cappedBody{rc: res.Body, left: t.maxBody}
	return res, nil
}

// ErrTooLarge is returned once a response passes the size cap.
var ErrTooLarge = errors.New("response exceeds the size limit")

type cappedBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *cappedBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, ErrTooLarge
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	return n, err
}

func (b *cappedBody) Close() error { return b.rc.Close() }
