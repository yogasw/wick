package managedbin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// MaxDownloadBytes bounds one binary download. omp-linux-x64 is ~286 MB.
const MaxDownloadBytes = 600 << 20

// allowedHosts are the only hosts a download (including every redirect)
// may touch: the API, github.com release URLs, and the asset CDNs they
// redirect to.
var allowedHosts = map[string]bool{
	"api.github.com":                       true,
	"github.com":                           true,
	"objects.githubusercontent.com":        true,
	"release-assets.githubusercontent.com": true,
}

// client talks to GitHub. apiBase and the host check are fields so tests
// can point them at an httptest server; production uses the defaults.
type client struct {
	http    *http.Client
	apiBase string
	allowed func(u *url.URL) bool
}

func newClient() *client {
	c := &client{apiBase: "https://api.github.com", allowed: defaultAllowed}
	c.http = &http.Client{
		Timeout: 30 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return errors.New("too many redirects")
			}
			if !c.allowed(req.URL) {
				return fmt.Errorf("redirect to disallowed host %q", req.URL.Host)
			}
			return nil
		},
	}
	return c
}

func defaultAllowed(u *url.URL) bool {
	return u.Scheme == "https" && allowedHosts[strings.ToLower(u.Hostname())]
}

// statusError is a non-200 answer; download retries the 5xx ones.
type statusError struct {
	url  string
	code int
	text string
}

func (e *statusError) Error() string { return fmt.Sprintf("GET %s: %s", e.url, e.text) }

func (c *client) get(ctx context.Context, rawURL string) (*http.Response, error) {
	return c.getAccept(ctx, rawURL, "")
}

// getAccept is get with an explicit Accept header ("" = the default for
// the host): an API asset URL needs application/octet-stream to answer
// with the file instead of its JSON description.
func (c *client) getAccept(ctx context.Context, rawURL, accept string) (*http.Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if !c.allowed(u) {
		return nil, fmt.Errorf("refusing non-GitHub URL %q", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "wick-managed-binaries")
	if accept != "" {
		req.Header.Set("Accept", accept)
	} else if strings.Contains(u.Host, "api.") || strings.HasPrefix(rawURL, c.apiBase) {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, &statusError{url: rawURL, code: resp.StatusCode, text: resp.Status}
	}
	return resp, nil
}

type ghAsset struct {
	Name               string `json:"name"`
	URL                string `json:"url"` // API asset URL, the download fallback
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	PublishedAt string    `json:"published_at"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	Assets      []ghAsset `json:"assets"`
}

func (r ghRelease) toRelease() Release {
	out := Release{Tag: r.TagName, Version: TagVersion(r.TagName), Published: r.PublishedAt, Prerelease: r.Prerelease}
	for _, a := range r.Assets {
		sum := ""
		if strings.HasPrefix(a.Digest, "sha256:") {
			sum = strings.ToLower(strings.TrimPrefix(a.Digest, "sha256:"))
		}
		out.Assets = append(out.Assets, Asset{Name: a.Name, URL: a.BrowserDownloadURL, APIURL: a.URL, Size: a.Size, SHA256: sum})
	}
	return out
}

func (c *client) getJSON(ctx context.Context, path string, v any) error {
	resp, err := c.get(ctx, c.apiBase+path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v)
}

// latest is GET /repos/{repo}/releases/latest (never a draft/prerelease).
func (c *client) latest(ctx context.Context, repo string) (Release, error) {
	var r ghRelease
	if err := c.getJSON(ctx, "/repos/"+repo+"/releases/latest", &r); err != nil {
		return Release{}, err
	}
	return r.toRelease(), nil
}

// byTag is GET /repos/{repo}/releases/tags/{tag}.
func (c *client) byTag(ctx context.Context, repo, tag string) (Release, error) {
	var r ghRelease
	if err := c.getJSON(ctx, "/repos/"+repo+"/releases/tags/"+url.PathEscape(tag), &r); err != nil {
		return Release{}, err
	}
	return r.toRelease(), nil
}

// list is the most recent published releases, newest first.
func (c *client) list(ctx context.Context, repo string, n int) ([]Release, error) {
	var rs []ghRelease
	if err := c.getJSON(ctx, fmt.Sprintf("/repos/%s/releases?per_page=%d", repo, n), &rs); err != nil {
		return nil, err
	}
	out := make([]Release, 0, len(rs))
	for _, r := range rs {
		if r.Draft {
			continue
		}
		out = append(out, r.toRelease())
	}
	return out, nil
}

// FetchSmall implements Fetcher for extra checks (SHA256SUMS.txt).
func (c *client) FetchSmall(ctx context.Context, rawURL string, max int64) ([]byte, error) {
	resp, err := c.get(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s larger than %d bytes", rawURL, max)
	}
	return b, nil
}

// downloadAsset fetches a release asset. github.com's release download URL
// sometimes answers 504 for minutes while the same file is served fine
// through the API asset URL, so a 5xx there is retried once and then the
// API URL is tried. Every route is checked against the same sha256 by the
// caller, so the fallback changes where the bytes come from, not what is
// accepted.
func (c *client) downloadAsset(ctx context.Context, a Asset, dest string, max int64, progress func(done, total int64)) (string, error) {
	sum, err := c.download(ctx, a.URL, "", dest, max, progress)
	if !isServerError(err) {
		return sum, err
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(retryDelay):
	}
	if sum, err = c.download(ctx, a.URL, "", dest, max, progress); !isServerError(err) || a.APIURL == "" {
		return sum, err
	}
	sum, apiErr := c.download(ctx, a.APIURL, "application/octet-stream", dest, max, progress)
	if apiErr != nil {
		return "", fmt.Errorf("%w; API asset URL: %v", err, apiErr)
	}
	return sum, nil
}

// retryDelay is the pause before the release URL is tried again.
var retryDelay = 3 * time.Second

func isServerError(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.code >= 500
}

// download streams rawURL into dest (created 0600), hashing as it goes,
// and returns the hex sha256. It stops past max bytes. progress receives
// (done, total) — total may be -1 when unknown.
func (c *client) download(ctx context.Context, rawURL, accept, dest string, max int64, progress func(done, total int64)) (string, error) {
	resp, err := c.getAccept(ctx, rawURL, accept)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.ContentLength > max {
		return "", fmt.Errorf("asset is %d bytes, over the %d byte limit", resp.ContentLength, max)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	w := io.MultiWriter(f, h)
	buf := make([]byte, 256<<10)
	var done int64
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			done += int64(n)
			if done > max {
				f.Close()
				return "", fmt.Errorf("download exceeded the %d byte limit", max)
			}
			if _, werr := w.Write(buf[:n]); werr != nil {
				f.Close()
				return "", werr
			}
			if progress != nil {
				progress(done, resp.ContentLength)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return "", rerr
		}
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fileSHA256 hashes a file on disk.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
