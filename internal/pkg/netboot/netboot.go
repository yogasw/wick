// Package netboot makes the pure-Go binary self-sufficient for DNS and
// TLS on hosts that lack the files Go's stdlib expects — most notably
// Android/Termux, whose read-only /etc has no /etc/resolv.conf and no
// /etc/ssl/certs, so DNS lookups hit the dead loopback resolver and every
// outbound HTTPS fails x509 verification.
//
// Termux keeps the real files under $PREFIX (e.g. $PREFIX/etc/resolv.conf,
// $PREFIX/etc/tls/cert.pem) — the same paths install.sh bind-mounts via
// proot for third-party CLIs. Setup points Go at those native files instead
// of embedding a CA bundle or guessing public DNS.
//
// Setup runs as early as possible in each binary's main, before any DB
// connect or outbound HTTPS. It is a no-op on normal hosts (resolv.conf has
// a real nameserver; the system cert pool is populated).
package netboot

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/pkg/safeexec"
)

const (
	resolvConfPath = "/etc/resolv.conf"
	sslCertFileEnv = "SSL_CERT_FILE"
)

// defaultNameservers is the public fallback used only when no usable
// resolv.conf, WICK_DNS_SERVERS, $PREFIX resolv.conf, or Android system
// property is available.
var defaultNameservers = []string{"1.1.1.1:53", "8.8.8.8:53"}

// systemCertLocations mirrors the common stdlib CA locations; a populated
// one means the host can verify HTTPS without help.
var systemCertLocations = []string{"/etc/ssl/certs", "/etc/ssl/cert.pem", "/etc/pki/tls/certs"}

var (
	setupOnce  sync.Once
	setupCount int // guarded by setupOnce; observed by tests
)

// Setup installs the DNS + CA fallbacks once. Idempotent and safe to call
// from any entry point; no-op when the host is already usable.
func Setup() {
	setupOnce.Do(func() {
		setupCount++
		setupDNS()
		setupCA()
	})
}

// setupDNS points net.DefaultResolver at an explicit nameserver only when
// the system resolv.conf configures no nameserver at all (missing/empty —
// the Termux case). A configured loopback nameserver such as
// systemd-resolved's 127.0.0.53 is a working resolver and is left alone.
func setupDNS() {
	content, _ := os.ReadFile(resolvConfPath)
	if hasConfiguredNameserver(string(content)) {
		return
	}
	servers := chooseNameservers(os.Getenv("WICK_DNS_SERVERS"), prefixResolvNameservers(), androidNameservers())
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			var lastErr error
			for _, s := range servers {
				conn, err := d.DialContext(ctx, "udp", s)
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
	log.Info().Str("component", "netboot").Strs("nameservers", servers).
		Msg("no usable /etc/resolv.conf; installed fallback DNS resolver")
}

// setupCA points SSL_CERT_FILE at the Termux CA bundle when the system has
// no usable cert store. Go reads SSL_CERT_FILE on first use, so this must
// run before any HTTPS call.
func setupCA() {
	if os.Getenv(sslCertFileEnv) != "" {
		return
	}
	// Only Termux-like hosts (PREFIX set) need help. Elsewhere trust the
	// OS store — including macOS, whose Keychain-backed pool has no files
	// under /etc and would otherwise look "missing".
	prefix := os.Getenv("PREFIX")
	if prefix == "" || systemHasCerts() {
		return
	}
	bundle := filepath.Join(prefix, "etc", "tls", "cert.pem")
	if _, err := os.Stat(bundle); err != nil {
		log.Warn().Str("component", "netboot").Str("bundle", bundle).
			Msg("system CA store not found and Termux CA bundle missing; outbound HTTPS may fail")
		return
	}
	_ = os.Setenv(sslCertFileEnv, bundle)
	log.Info().Str("component", "netboot").Str("bundle", bundle).
		Msg("no system CA store; using Termux CA bundle for HTTPS")
}

// usableNameservers returns every non-loopback nameserver in resolv.conf
// content, normalized to host:port. Used to extract dialable servers from
// $PREFIX/etc/resolv.conf (a loopback there would not be reachable from
// our own resolver).
func usableNameservers(content string) []string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		ip := net.ParseIP(fields[1])
		if ip == nil || ip.IsLoopback() {
			continue
		}
		out = append(out, net.JoinHostPort(fields[1], "53"))
	}
	return out
}

// hasConfiguredNameserver reports whether resolv.conf lists ANY valid
// nameserver, loopback included. A configured loopback (systemd-resolved's
// 127.0.0.53) is a real resolver, so its presence means we must not
// override DNS — only a truly empty/absent resolv.conf triggers fallback.
func hasConfiguredNameserver(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		if net.ParseIP(fields[1]) != nil {
			return true
		}
	}
	return false
}

// normalizeServer trims a nameserver and appends the default DNS port when
// none is present. Empty input returns empty.
func normalizeServer(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(s); err == nil {
		return s
	}
	return net.JoinHostPort(s, "53")
}

// parseServers splits a comma/space-separated nameserver list into
// normalized host:port entries, dropping blanks.
func parseServers(csv string) []string {
	parts := strings.FieldsFunc(csv, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if ns := normalizeServer(p); ns != "" {
			out = append(out, ns)
		}
	}
	return out
}

// chooseNameservers resolves the nameserver list in priority order:
// explicit override, then $PREFIX resolv.conf, then Android system DNS,
// then public defaults.
func chooseNameservers(override string, prefix, android []string) []string {
	if ns := parseServers(override); len(ns) > 0 {
		return ns
	}
	if len(prefix) > 0 {
		return prefix
	}
	out := make([]string, 0, len(android))
	for _, a := range android {
		if ns := normalizeServer(a); ns != "" {
			out = append(out, ns)
		}
	}
	if len(out) > 0 {
		return out
	}
	return defaultNameservers
}

// prefixResolvNameservers reads $PREFIX/etc/resolv.conf (Termux's real DNS
// config). Empty when $PREFIX is unset or the file is absent/unusable.
func prefixResolvNameservers() []string {
	prefix := os.Getenv("PREFIX")
	if prefix == "" {
		return nil
	}
	content, err := os.ReadFile(filepath.Join(prefix, "etc", "resolv.conf"))
	if err != nil {
		return nil
	}
	return usableNameservers(string(content))
}

// androidNameservers reads net.dns1 / net.dns2 via getprop. Empty on any
// host where getprop is absent.
func androidNameservers() []string {
	var out []string
	for _, prop := range []string{"net.dns1", "net.dns2"} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		raw, err := safeexec.CommandContext(ctx, "getprop", prop).Output()
		cancel()
		if err != nil {
			continue
		}
		if v := strings.TrimSpace(string(raw)); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// systemHasCerts reports whether a standard CA store exists and is
// populated, so the embedded/Termux fallback can be skipped.
func systemHasCerts() bool {
	for _, p := range systemCertLocations {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if !fi.IsDir() {
			return true
		}
		if entries, _ := os.ReadDir(p); len(entries) > 0 {
			return true
		}
	}
	return false
}
