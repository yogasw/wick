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
	"strconv"
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

	// RewriteArgs is the "-c url.<https>.insteadOf=<ssh>" pair that makes git dial
	// Effective while the command line still names the REMOTE. Empty when no rewrite
	// is needed (an HTTPS or local-path remote), so the common case injects nothing.
	//
	// This exists because passing Effective as the remote argument — which is what the
	// connector did — silently costs three things: fetch stops updating
	// refs/remotes/<remote>/*, push --set-upstream records the URL string as
	// branch.<b>.remote so ahead/behind dies, and pull has no upstream to resolve.
	// Every one of them is git bookkeeping that only happens when git knows the remote
	// by name.
	RewriteArgs []string
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

// allowedSchemes are the transports a remote may use. An allow-list, because git's
// transport layer is extensible and several of its transports execute commands.
var allowedSchemes = map[string]bool{
	"https": true, "http": true, "ssh": true,
	// file:// is a plain local path with a scheme on it — no transport helper, no command
	// execution — and isLocalPathRemote already accepts it, so refusing it here would
	// only break a mirror on a share for no gain.
	"file": true,
	// "git" is deliberately absent. The git:// protocol is unauthenticated and
	// unencrypted, and ConvertRemote never supported it: it has no credential to carry,
	// which makes it meaningless for a connector whose whole subject is authenticating
	// as a configured identity. It is refused by name rather than by the parser
	// happening to read "git" as a hostname.
}

// checkTransport refuses a remote whose transport is not on the allow-list.
//
// This is a by-design refusal replacing an accidental one. "ext::false" WAS rejected —
// but only because the scp-style parser could not make sense of it and reported
// `remote host "ext" looks like an ~/.ssh/config alias`. The class is remote code
// execution (git's ext transport runs its argument as a command), and it was being
// stopped by a parser's confusion: any future parser change could have let it through,
// and the message told an operator to fix a host map rather than that they had asked for
// something forbidden.
//
// Anything with a "scheme::" prefix is refused outright. That covers ext:: and fd::, and
// every transport helper git gains later, without this list having to be maintained.
func checkTransport(raw string) error {
	low := strings.ToLower(raw)

	// "scheme::rest" is git's transport-helper syntax. Checked before "://" because
	// "ext::ssh://host/x" contains both.
	if i := strings.Index(low, "::"); i > 0 && !strings.Contains(low[:i], "/") {
		return fmt.Errorf("remote %q uses the %q transport, which is not allowed: "+
			"it can execute arbitrary commands. Use an https, ssh or git URL",
			raw, low[:i])
	}
	if i := strings.Index(low, "://"); i > 0 {
		scheme := low[:i]
		if !allowedSchemes[scheme] {
			return fmt.Errorf("remote %q uses the %q scheme, which is not allowed: "+
				"only https, http, ssh and git URLs, or a local filesystem path", raw, scheme)
		}
	}
	return nil
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
	if err := checkTransport(raw); err != nil {
		return RemoteInfo{}, err
	}

	info := RemoteInfo{Original: raw}

	if isHTTPURL(raw) {
		info.Effective = StripCredentials(raw)
		info.Slug = RepoSlug(info.Effective)
		// A credential embedded in .git/config WINS over the connector's own: verified
		// against git 2.52 — with "https://user:pass@host/repo.git" configured, git dials
		// with that credential and never consults GIT_ASKPASS, so the connector would
		// authenticate as whoever's password is in the file. Passing the stripped URL as
		// the remote argument used to be what prevented this, at the cost of breaking
		// remote-tracking refs and upstream (see RewriteArgs).
		//
		// insteadOf gets both: the command still names the remote, and git substitutes
		// the credential-free URL when it dials.
		if info.Effective != raw {
			info.RewriteArgs = []string{"-c", "url." + info.Effective + ".insteadOf=" + raw}
		}
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

	// The rewrite is keyed on the PREFIX rather than the whole URL, so it also covers
	// a push URL that differs from the fetch URL on the same host. Longest-prefix wins
	// in git, and this is the most specific prefix that still identifies the host.
	info.RewriteArgs = []string{"-c",
		"url.https://" + strings.TrimRight(target, "/") + "/.insteadOf=" + sshPrefixOf(raw, host)}
	return info, nil
}

// sshPrefixOf returns the scp-style prefix insteadOf has to match: "git@host:" for
// the usual form, "ssh://host/" when a scheme was written out.
//
// Matching the prefix rather than the full URL is what makes one rewrite cover every
// repository on that host, which matters because the rewrite is injected per command
// and the remote's URL is read fresh each time.
func sshPrefixOf(raw, host string) string {
	if i := strings.Index(raw, "://"); i >= 0 {
		return raw[:i+3] + host + "/"
	}
	if at := strings.Index(raw, "@"); at > 0 {
		return raw[:at+1] + host + ":"
	}
	return host + ":"
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
// RepoID is a remote broken into the parts a policy rule is written against.
//
// Structured rather than one string because matching a glob against a raw URL made
// every encoding variant its own bug. A trailing dot on the hostname —
// "bitbucket.org./owner/repo", which is valid DNS and routes and TLS-verifies
// normally — did not match the glob "bitbucket.org/owner/repo", so the same
// repository resolved to the global fallback and every rule stopped applying. Case,
// ports, double slashes and the ".git" suffix had each been handled with a targeted
// fix; the trailing dot was the one nobody thought of, and the next encoding would
// have been the one after that.
//
// Normalising into fields moves the problem from "did I remember this spelling" to
// "does the parser agree", which is a question with one answer.
type RepoID struct {
	Host  string // lowercased, port stripped, trailing dots removed
	Owner string // first path segment, lowercased
	Name  string // remaining segments joined, ".git" removed, lowercased
	Local bool   // a filesystem remote: no host, so no host-based rule can apply
}

// Slug renders host/owner/name, the form policy rules are written in. Empty when the
// remote could not be identified, which callers treat as "no slug".
func (r RepoID) Slug() string {
	if r.Local || r.Host == "" || r.Owner == "" {
		return ""
	}
	if r.Name == "" {
		return r.Host + "/" + r.Owner
	}
	return r.Host + "/" + r.Owner + "/" + r.Name
}

// ParseRepoID normalises a remote into its parts.
//
// A filesystem remote yields Local, with no host: a local path has nothing a
// host/owner/repo rule could match, and inventing one produced slugs like
// "C/\Users\x\repo" (the drive colon eaten by the port stripper) and
// "file/srv/mirror" (the scheme read as a hostname). Both are junk that a careless
// glob could match by accident.
func ParseRepoID(raw string) RepoID {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return RepoID{}
	}
	// Checked before the URL parse: "file://" IS a URL, and treating it as one made
	// "file" the hostname.
	if isLocalPathRemote(raw) {
		return RepoID{Local: true}
	}

	var host, p string
	switch {
	case isHTTPURL(raw):
		u, err := url.Parse(raw)
		if err != nil {
			return RepoID{}
		}
		host, p = u.Hostname(), u.Path
	default:
		h, rp, err := splitSSHRemote(raw)
		if err != nil {
			return RepoID{}
		}
		host, p = h, rp
	}

	host = normHost(host)
	if host == "" {
		return RepoID{}
	}

	// Empty segments collapse, so "//owner//repo" and "/owner/repo" are one thing.
	segs := make([]string, 0, 4)
	for _, seg := range strings.Split(p, "/") {
		if seg != "" {
			segs = append(segs, strings.ToLower(seg))
		}
	}
	if len(segs) == 0 {
		return RepoID{Host: host}
	}
	id := RepoID{Host: host, Owner: segs[0]}
	if len(segs) > 1 {
		// Every remaining segment, so a GitLab subgroup path survives intact.
		id.Name = strings.TrimSuffix(strings.Join(segs[1:], "/"), ".git")
	}
	return id
}

// normHost lowercases a hostname and strips the parts that do not identify it.
//
// The trailing dot is the one that mattered: "bitbucket.org." is the fully qualified
// form of "bitbucket.org", accepted by DNS and by TLS, so it reached the same server
// while comparing unequal to every rule. A port is dropped for the same reason — the
// host is the identity, the port is how you reach it.
func normHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if i := strings.LastIndex(h, ":"); i > 0 && !strings.Contains(h[i+1:], ".") {
		// A port, not part of an IPv6 literal (which url.Hostname already unwrapped).
		if _, err := strconv.Atoi(h[i+1:]); err == nil {
			h = h[:i]
		}
	}
	return strings.TrimRight(h, ".")
}

// RepoSlug is ParseRepoID(raw).Slug(), kept because it is what the rest of the
// connector already calls.
func RepoSlug(raw string) string {
	return ParseRepoID(raw).Slug()
}
