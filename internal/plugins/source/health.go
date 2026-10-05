package source

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

// Step is one row of the source health check.
type Step struct {
	N       int    `json:"n"`
	Name    string `json:"name"`
	Status  string `json:"status"` // ok | fail | skip
	Message string `json:"message"`
}

// HealthFunc reports the runtime health of an installed plugin key; ok=false
// with installed=false means it is not installed.
type HealthFunc func(key string) (installed, ok bool, detail string)

// Test runs the six-step health check in order. A failed step skips the
// steps that depend on it, so the admin sees exactly where it broke.
func (c *Client) Test(ctx context.Context, src *entity.PluginSource, health HealthFunc) []Step {
	steps := []Step{
		{N: 1, Name: "Reachable"}, {N: 2, Name: "Auth"}, {N: 3, Name: "Index"},
		{N: 4, Name: "Asset for host"}, {N: 5, Name: "Dry-run"}, {N: 6, Name: "After install"},
	}
	failAt := -1
	set := func(i int, err error, okMsg string) {
		if err != nil {
			steps[i].Status, steps[i].Message = "fail", err.Error()
			failAt = i
			return
		}
		steps[i].Status, steps[i].Message = "ok", okMsg
	}

	set(0, c.checkReachable(ctx, src), "")
	if failAt < 0 {
		steps[0].Message = "reached " + targetHost(c, src)
		msg, err := c.checkAuth(ctx, src)
		set(1, err, msg)
	}
	var entries []Entry
	if failAt < 0 {
		res, err := c.Fetch(ctx, src, "")
		if err == nil {
			err = ValidateIndex(res.Entries)
		}
		entries = res.Entries
		set(2, err, fmt.Sprintf("%d plugin(s): %s", len(entries), keyList(entries)))
	}
	if failAt < 0 {
		var bad []string
		for _, e := range entries {
			a, ok := e.Assets[HostOSArch()]
			switch {
			case !ok:
				bad = append(bad, e.Key+": no "+HostOSArch()+" build")
			case !zipNameOK(e, HostOSArch(), a.URL) && !isZipURL(src.URL):
				bad = append(bad, e.Key+": file name is not <key>-<version>-<os>-<arch>.zip")
			}
		}
		var err error
		if len(bad) > 0 {
			err = errors.New(strings.Join(bad, "; "))
		}
		set(3, err, HostOSArch()+" build present for every plugin")
	}
	if failAt < 0 {
		var err error
		var done []string
		for _, e := range entries {
			in, ierr := c.InstallEntry(ctx, src, e, InstallOptions{DryRun: true})
			if ierr != nil {
				err = fmt.Errorf("%s: %w", e.Key, ierr)
				break
			}
			done = append(done, fmt.Sprintf("%s %s v%s", in.Key, in.Kind, in.Version))
		}
		set(4, err, "verified zip_sha256, signature, manifest: "+strings.Join(done, ", "))
	}
	if failAt < 0 {
		var bad, good []string
		for _, e := range entries {
			if health == nil {
				break
			}
			installed, ok, detail := health(e.Key)
			if !installed {
				continue
			}
			if ok {
				good = append(good, e.Key+" "+detail)
			} else {
				bad = append(bad, e.Key+": "+detail)
			}
		}
		switch {
		case len(bad) > 0:
			set(5, errors.New(strings.Join(bad, "; ")), "")
		case len(good) == 0:
			steps[5].Status, steps[5].Message = "skip", "no plugin from this source is installed yet"
		default:
			set(5, nil, strings.Join(good, ", "))
		}
	}
	for i := range steps {
		if steps[i].Status == "" {
			steps[i].Status, steps[i].Message = "skip", fmt.Sprintf("skipped: step %d failed", failAt+1)
		}
	}
	return steps
}

func targetHost(c *Client, src *entity.PluginSource) string {
	raw := src.URL
	if src.Type == TypeGitHub {
		raw = c.api()
	}
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return raw
}

// checkReachable requires HTTPS (http only for localhost) and a TCP-level
// answer from the host.
func (c *Client) checkReachable(ctx context.Context, src *entity.PluginSource) error {
	raw := src.URL
	if src.Type == TypeGitHub {
		if src.Owner == "" || src.Repo == "" {
			return errors.New("owner and repo are required")
		}
		raw = c.api()
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid URL %q", raw)
	}
	if err := RequireHTTPS(u); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, u.String(), nil)
	resp, err := c.hc().Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach %s: %v", u.Host, err)
	}
	resp.Body.Close()
	return nil
}

// RequireHTTPS rejects plain http except for loopback hosts.
func RequireHTTPS(u *url.URL) error {
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	if u.Scheme == "http" && (host == "localhost" || net.ParseIP(host).IsLoopback()) {
		return nil
	}
	return fmt.Errorf("%s is not HTTPS (http is only allowed for localhost)", u.Host)
}

func (c *Client) checkAuth(ctx context.Context, src *entity.PluginSource) (string, error) {
	if src.Type != TypeGitHub {
		return "no auth needed for a URL source", nil
	}
	repo := src.Owner + "/" + src.Repo
	if c.pat(src) != "" {
		if _, _, _, err := c.get(ctx, src, c.api()+"/user", "", "application/vnd.github+json"); err != nil {
			if rateLimited(err) {
				return "", fmt.Errorf("GitHub rate limit reached (%s); try again later", statusText(err))
			}
			return "", fmt.Errorf("PAT rejected by GitHub (%s)", statusText(err))
		}
	} else if src.Private {
		return "", fmt.Errorf("private repo %s needs a PAT", repo)
	}
	if _, _, _, err := c.get(ctx, src, c.api()+"/repos/"+repo, "", "application/vnd.github+json"); err != nil {
		var se *StatusError
		if rateLimited(err) {
			return "", fmt.Errorf("GitHub rate limit reached (%s); try again later or add a PAT", statusText(err))
		}
		if errors.As(err, &se) && (se.Code == 403 || se.Code == 404) {
			if c.pat(src) == "" {
				return "", fmt.Errorf("%d: %s not found or private (add a PAT)", se.Code, repo)
			}
			return "", fmt.Errorf("%d: PAT has no Contents access to %s", se.Code, repo)
		}
		return "", err
	}
	if c.pat(src) == "" {
		return "public repo " + repo + " readable without a PAT", nil
	}
	return "PAT valid, can read " + repo, nil
}

func rateLimited(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.RateLimited
}

func statusText(err error) string {
	var se *StatusError
	if errors.As(err, &se) {
		return fmt.Sprintf("status %d", se.Code)
	}
	return err.Error()
}

func keyList(es []Entry) string {
	ks := make([]string, 0, len(es))
	for _, e := range es {
		ks = append(ks, e.Key+" v"+strings.TrimPrefix(e.Version, "v"))
	}
	return strings.Join(ks, ", ")
}
