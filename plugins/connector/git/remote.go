// remote.go handles remote URLs: reading credentials out of them, converting SSH
// remotes to HTTPS, and reporting what was actually used.
//
// The plugin never rewrites .git/config. Credentials baked into a remote URL are
// ignored, not consumed and not removed — the clean URL is passed explicitly to
// each network operation instead.

package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// RemoteInfo records what URL an operation actually used. Every network
// operation reports it, so a push landing on an unexpected host is visible
// immediately instead of being a mystery.
type RemoteInfo struct {
	Original  string
	Effective string
	Slug      string // host/owner/repo, used for policy matching
	Converted bool
}

// StripCredentials removes any user:password@ prefix from an http(s) URL. The
// scp-style form (git@host:path) is returned untouched — its "git@" is a
// transport username, not a credential, and ConvertRemote handles it.
func StripCredentials(raw string) string {
	if !isHTTPURL(raw) {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.User = nil
	return u.String()
}

// isHTTPURL reports whether raw carries an http(s) scheme. Checked by exact
// prefix rather than "http" so a host literally named "httpsomething:path" is
// still treated as the scp-like form it is.
func isHTTPURL(raw string) bool {
	return strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://")
}

// ParseHostMap decodes the remote_host_map kvlist into ssh_host → https_host.
// A malformed value yields an empty map rather than an error: the caller then
// falls back to mechanical conversion, which is correct for the cloud providers.
func ParseHostMap(s string) map[string]string {
	rows, err := ParseKVList(s)
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		ssh := strings.TrimSpace(r["ssh_host"])
		https := normHostMapValue(r["https_host"])
		if ssh != "" && https != "" {
			out[strings.ToLower(ssh)] = https
		}
	}
	return out
}

// normHostMapValue cleans an https_host value into the bare "host[/path]" form
// ConvertRemote concatenates onto "https://".
//
// The column is named "https host", so writing "https://code.company.com" is the
// obvious mistake to make — and left alone it would build "https://https://…"
// and push somewhere that does not exist. Strip the scheme rather than reject the
// row: the intent is unambiguous, and silently dropping a mapping would send the
// push to the unmapped SSH host instead, which is worse.
func normHostMapValue(v string) string {
	v = strings.TrimSpace(v)
	for _, scheme := range []string{"https://", "http://", "ssh://"} {
		v = strings.TrimPrefix(v, scheme)
	}
	v = strings.Trim(v, "/")
	// A value still carrying a scheme separator or a userinfo marker is not a
	// host — refuse it rather than build a broken URL from it.
	if strings.Contains(v, "://") || strings.Contains(v, "@") {
		return ""
	}
	return v
}

// ConvertRemote turns a remote URL into the HTTPS URL a network operation should
// use. HTTPS input is only credential-stripped; SSH input is converted when
// convertSSH is true.
//
// Two shapes deliberately fail instead of guessing:
//   - an ~/.ssh/config Host alias, whose real hostname is unknowable here
//   - conversion disabled while the remote is SSH
func ConvertRemote(raw string, hostMap map[string]string, convertSSH bool) (RemoteInfo, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return RemoteInfo{}, fmt.Errorf("remote URL is empty")
	}

	info := RemoteInfo{Original: raw}

	if isHTTPURL(raw) {
		info.Effective = StripCredentials(raw)
		info.Slug = RepoSlug(info.Effective)
		return info, nil
	}

	// A filesystem path is a perfectly valid git remote (a bare repo on a share,
	// a local mirror) and needs no conversion or credential at all. It must be
	// recognised BEFORE the scp-like parse, because that parse splits on the first
	// ":" — which on Windows is the drive letter, turning C:\repos\x.git into
	// host "C". Left unhandled, every local path remote is rejected as an
	// ~/.ssh/config alias.
	if isLocalPathRemote(raw) {
		info.Effective = raw
		info.Slug = ""
		return info, nil
	}

	host, repoPath, err := splitSSHRemote(raw)
	if err != nil {
		return RemoteInfo{}, err
	}

	if !convertSSH {
		return RemoteInfo{}, fmt.Errorf(
			"remote %q uses SSH but convert_ssh_remote_to_https is off; enable it or set an HTTPS remote", raw)
	}

	target := host
	if mapped, ok := hostMap[strings.ToLower(host)]; ok {
		target = mapped
	} else if !strings.Contains(host, ".") {
		// No dot means this cannot be a real hostname — it is an ~/.ssh/config
		// Host alias. Guessing would silently push to the wrong server.
		return RemoteInfo{}, fmt.Errorf(
			"remote host %q looks like an ~/.ssh/config alias, not a hostname; add a remote_host_map row mapping it to the HTTPS host", host)
	}

	info.Effective = "https://" + strings.TrimRight(target, "/") + "/" + repoPath
	info.Slug = RepoSlug(info.Effective)
	info.Converted = true
	return info, nil
}

// isLocalPathRemote reports whether raw is a filesystem path rather than a
// network remote. Git accepts both, and a path needs no conversion, no host map
// and no credential.
//
// The test is deliberately narrow so it cannot swallow a real scp-like remote
// (git@host:org/repo.git):
//
//   - "file://…" is explicit.
//   - A Windows drive path ("C:\repos\x.git", "C:/repos/x.git") has a
//     single-letter segment before the colon, which no hostname can be, and a
//     separator straight after it.
//   - A path starting with "/", "./", "../" or "~/" has no colon-host shape at
//     all.
//   - Anything containing no ":" and pointing at something that exists on disk
//     is a relative path ("../mirror.git").
func isLocalPathRemote(raw string) bool {
	if strings.HasPrefix(raw, "file://") {
		return true
	}
	n := norm(raw)

	// Windows drive letter: exactly one letter, then ":", then a separator.
	if len(n) >= 3 && n[1] == ':' && n[2] == '/' {
		c := n[0]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return true
		}
	}

	if strings.HasPrefix(n, "/") || strings.HasPrefix(n, "./") ||
		strings.HasPrefix(n, "../") || strings.HasPrefix(n, "~/") {
		return true
	}

	// A bare relative path only counts when it really exists, so a typo'd
	// hostname is still reported as a hostname rather than silently treated as a
	// directory.
	if !strings.Contains(n, ":") {
		if _, err := os.Stat(raw); err == nil {
			return true
		}
	}
	return false
}

// splitSSHRemote extracts host and repo path from either SSH form:
//
//	git@host:owner/repo.git          (scp-like)
//	ssh://git@host[:port]/owner/repo.git
//
// The repo path keeps every segment, so GitLab subgroups survive.
func splitSSHRemote(raw string) (host, repoPath string, err error) {
	if strings.HasPrefix(raw, "ssh://") {
		u, perr := url.Parse(raw)
		if perr != nil {
			return "", "", fmt.Errorf("parse ssh remote %q: %w", raw, perr)
		}
		host, repoPath = u.Hostname(), strings.TrimLeft(u.Path, "/")
		if host == "" || repoPath == "" {
			return "", "", fmt.Errorf("remote %q is not a recognised git URL", raw)
		}
		return host, repoPath, nil
	}

	at := strings.Index(raw, "@")
	colon := strings.Index(raw, ":")
	if colon < 0 {
		return "", "", fmt.Errorf("remote %q is not a recognised git URL", raw)
	}
	// A colon before the "@" belongs to a credential, not the host/path split, so
	// the shape is not an scp-like remote at all.
	if at > colon {
		return "", "", fmt.Errorf("remote %q is not a recognised git URL", raw)
	}
	host = raw[at+1 : colon] // at == -1 gives raw[0:colon], which is what we want
	repoPath = strings.TrimLeft(raw[colon+1:], "/")
	if host == "" || repoPath == "" {
		return "", "", fmt.Errorf("remote %q is not a recognised git URL", raw)
	}
	return host, repoPath, nil
}

// RepoSlug reduces any remote URL to host/owner/repo (no scheme, no credentials,
// no .git suffix) for policy matching.
func RepoSlug(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var host, p string
	switch {
	case isHTTPURL(raw):
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		host, p = u.Hostname(), strings.TrimLeft(u.Path, "/")
	default:
		h, rp, err := splitSSHRemote(raw)
		if err != nil {
			return ""
		}
		host, p = h, rp
	}
	return host + "/" + strings.TrimSuffix(p, ".git")
}
