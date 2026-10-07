package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

// Source types.
const (
	TypeURL    = "url"
	TypeGitHub = "github"
)

const (
	defaultGitHubAPI = "https://api.github.com"
	maxIndexBytes    = 1 << 20
	maxZipBytes      = 100 << 20
)

// Client fetches indexes and zips. GitHubAPI is overridable so tests run
// against httptest instead of the real GitHub.
type Client struct {
	HTTP      *http.Client
	GitHubAPI string
	// Decrypt turns the stored PAT token back into plaintext for one call.
	Decrypt func(string) (string, error)
}

// NewClient returns a Client with sane timeouts and the public GitHub API.
func NewClient(decrypt func(string) (string, error)) *Client {
	return &Client{HTTP: &http.Client{Timeout: 5 * time.Minute}, GitHubAPI: defaultGitHubAPI, Decrypt: decrypt}
}

func (c *Client) api() string {
	if c.GitHubAPI != "" {
		return strings.TrimRight(c.GitHubAPI, "/")
	}
	return defaultGitHubAPI
}

func (c *Client) hc() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// pat returns the plaintext PAT for a GitHub source ("" when none).
func (c *Client) pat(src *entity.PluginSource) string {
	if src == nil || src.Type != TypeGitHub || src.PAT == "" {
		return ""
	}
	if c.Decrypt == nil {
		return src.PAT
	}
	p, err := c.Decrypt(src.PAT)
	if err != nil {
		return ""
	}
	return p
}

// authorize adds the PAT only when the request targets the GitHub API host,
// so a URL inside an index can never receive the token.
func (c *Client) authorize(req *http.Request, src *entity.PluginSource) {
	tok := c.pat(src)
	if tok == "" {
		return
	}
	api, err := url.Parse(c.api())
	if err != nil || req.URL.Host != api.Host {
		return
	}
	req.Header.Set("Authorization", "Bearer "+tok)
}

// FetchResult is one index fetch. NotModified means the ETag matched and
// Entries is nil — the caller keeps its cached index.
type FetchResult struct {
	Entries     []Entry
	Raw         []byte
	ETag        string
	NotModified bool
}

// Fetch resolves the source's current plugin list. etag is the cached ETag
// ("" forces a full fetch).
func (c *Client) Fetch(ctx context.Context, src *entity.PluginSource, etag string) (FetchResult, error) {
	switch src.Type {
	case TypeURL:
		return c.fetchURL(ctx, src, etag)
	case TypeGitHub:
		return c.fetchGitHub(ctx, src, etag)
	}
	return FetchResult{}, fmt.Errorf("unknown source type %q", src.Type)
}

func (c *Client) fetchURL(ctx context.Context, src *entity.PluginSource, etag string) (FetchResult, error) {
	if isZipURL(src.URL) {
		// A bare zip link is a one-off install: the entry comes from the
		// file name; kind and hashes come from the manifest inside the zip.
		key, ver, oa, ok := parseZipName(path.Base(mustPath(src.URL)))
		if !ok {
			return FetchResult{}, fmt.Errorf("zip name must be <key>-<version>-<os>-<arch>.zip")
		}
		e := Entry{Key: key, Name: key, Version: ver, Assets: map[string]Asset{oa: {URL: src.URL}}}
		raw, _ := json.Marshal([]Entry{e})
		return FetchResult{Entries: []Entry{e}, Raw: raw}, nil
	}
	body, newTag, notMod, err := c.get(ctx, src, src.URL, etag, "")
	if err != nil || notMod {
		return FetchResult{NotModified: notMod, ETag: etag}, err
	}
	entries, err := ParseIndex(body, src.URL)
	if err != nil {
		return FetchResult{}, err
	}
	return FetchResult{Entries: filter(src, entries), Raw: body, ETag: newTag}, nil
}

type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

type ghAsset struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	DownloadURL string `json:"browser_download_url"`
}

func (c *Client) fetchGitHub(ctx context.Context, src *entity.PluginSource, etag string) (FetchResult, error) {
	relURL := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=50", c.api(), src.Owner, src.Repo)
	body, newTag, notMod, err := c.get(ctx, src, relURL, etag, "application/vnd.github+json")
	if err != nil || notMod {
		return FetchResult{NotModified: notMod, ETag: etag}, err
	}
	var rels []ghRelease
	if err := json.Unmarshal(body, &rels); err != nil {
		return FetchResult{}, fmt.Errorf("parse releases: %w", err)
	}
	// Highest version per key — not GitHub's "latest", since one repo
	// carries many plugins, each with its own <key>/v<ver> releases.
	best := map[string]ghRelease{}
	bestVer := map[string]string{}
	for _, r := range rels {
		if r.Draft || (r.Prerelease && !src.AllowPrerelease) {
			continue
		}
		key, ver, ok := splitTag(r.TagName)
		if !ok || !keyAllowed(src.KeyFilter, key) {
			continue
		}
		if cur, seen := bestVer[key]; !seen || newer(ver, cur) {
			best[key], bestVer[key] = r, ver
		}
	}
	var entries []Entry
	for key, r := range best {
		e, err := c.releaseEntry(ctx, src, key, bestVer[key], r)
		if err != nil {
			return FetchResult{}, err
		}
		entries = append(entries, e)
	}
	sortEntries(entries)
	raw, _ := json.Marshal(entries)
	return FetchResult{Entries: entries, Raw: raw, ETag: newTag}, nil
}

// releaseEntry reads the release's plugins.json asset (relative urls resolve
// to the release's own assets) or, without one, synthesizes the entry from
// zip asset names.
func (c *Client) releaseEntry(ctx context.Context, src *entity.PluginSource, key, ver string, r ghRelease) (Entry, error) {
	byName := map[string]ghAsset{}
	for _, a := range r.Assets {
		byName[a.Name] = a
	}
	e := Entry{Key: key, Name: key, Version: ver, Assets: map[string]Asset{}}
	if idx, ok := byName["plugins.json"]; ok {
		body, err := c.downloadAsset(ctx, src, idx)
		if err != nil {
			return Entry{}, fmt.Errorf("%s plugins.json: %w", r.TagName, err)
		}
		parsed, err := ParseIndex(body, idx.DownloadURL)
		if err != nil {
			return Entry{}, fmt.Errorf("%s: %w", r.TagName, err)
		}
		for _, p := range parsed {
			if p.Key == key {
				e = p
			}
		}
	} else {
		for _, a := range r.Assets {
			if _, _, oa, ok := parseZipName(a.Name); ok {
				e.Assets[oa] = Asset{URL: a.DownloadURL}
			}
		}
	}
	// Map every asset back to its release asset so private repos download
	// through the API endpoint. An api_url written in plugins.json itself is
	// never trusted: only a matching release asset sets it.
	for oa, a := range e.Assets {
		a.APIURL = ""
		if ga, ok := byName[path.Base(mustPath(a.URL))]; ok {
			a.APIURL = ga.URL
			if a.URL == "" {
				a.URL = ga.DownloadURL
			}
		}
		e.Assets[oa] = a
	}
	return e, nil
}

// downloadAsset fetches a small release asset (plugins.json) in memory.
func (c *Client) downloadAsset(ctx context.Context, src *entity.PluginSource, a ghAsset) ([]byte, error) {
	if src.Private {
		body, _, _, err := c.get(ctx, src, a.URL, "", "application/octet-stream")
		return body, err
	}
	body, _, _, err := c.get(ctx, src, a.DownloadURL, "", "")
	return body, err
}

// get performs a GET capped at maxIndexBytes with optional ETag.
func (c *Client) get(ctx context.Context, src *entity.PluginSource, u, etag, accept string) ([]byte, string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", false, err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	c.authorize(req, src)
	resp, err := c.hc().Do(req)
	if err != nil {
		return nil, "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return nil, etag, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		limited := resp.StatusCode == http.StatusTooManyRequests ||
			(resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0")
		return nil, "", false, &StatusError{Code: resp.StatusCode, URL: redact(u), Body: strings.TrimSpace(string(msg)), RateLimited: limited}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIndexBytes))
	return body, resp.Header.Get("ETag"), false, err
}

// StatusError is a non-200 answer from a source.
type StatusError struct {
	Code int
	URL  string
	Body string
	// RateLimited is a 429, or a 403 with X-RateLimit-Remaining: 0 (GitHub).
	RateLimited bool
}

func (e *StatusError) Error() string { return fmt.Sprintf("%s: status %d", e.URL, e.Code) }

func filter(src *entity.PluginSource, in []Entry) []Entry {
	out := in[:0]
	for _, e := range in {
		if keyAllowed(src.KeyFilter, e.Key) {
			out = append(out, e)
		}
	}
	return out
}

func sortEntries(es []Entry) {
	for i := 1; i < len(es); i++ {
		for j := i; j > 0 && es[j].Key < es[j-1].Key; j-- {
			es[j], es[j-1] = es[j-1], es[j]
		}
	}
}

func splitTag(tag string) (key, version string, ok bool) {
	i := strings.Index(tag, "/v")
	if i <= 0 || i+2 >= len(tag) {
		return "", "", false
	}
	return tag[:i], tag[i+2:], true
}

// IsZipURL reports whether u points straight at a .zip (a one-off link
// source) rather than a plugins.json index.
func IsZipURL(u string) bool { return isZipURL(u) }

func isZipURL(u string) bool { return strings.HasSuffix(strings.ToLower(mustPath(u)), ".zip") }

func mustPath(u string) string {
	if p, err := url.Parse(u); err == nil {
		return p.Path
	}
	return u
}

// redact drops the query string so signed URLs never reach logs or the UI.
func redact(u string) string {
	if p, err := url.Parse(u); err == nil {
		p.RawQuery = ""
		return p.String()
	}
	return u
}
