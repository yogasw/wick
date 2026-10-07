package slackremote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/remote"
)

// Directory kinds a search can ask for.
const (
	DirUsers    = "users"
	DirChannels = "channels"
)

// DirEntry is one pickable Slack user, bot or channel. It is only shown
// to a person who picks it; the id they pick is what gets stored, never a
// name matched on the server.
type DirEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	RealName    string `json:"real_name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Avatar      string `json:"avatar,omitempty"`
	IsBot       bool   `json:"is_bot,omitempty"`
	IsPrivate   bool   `json:"is_private,omitempty"`
}

// MissingScopeError is a directory read refused for want of a Slack scope.
type MissingScopeError struct {
	Kind   string
	Scopes string
}

func (e *MissingScopeError) Error() string {
	what := "users and bots"
	if e.Kind == DirChannels {
		what = "channels"
	}
	return fmt.Sprintf("this Slack token cannot list %s: add the %s scope, or enter the ID manually", what, e.Scopes)
}

// DirectoryMaxResults caps one search's answer.
const DirectoryMaxResults = 20

// directoryMaxPages bounds one listing, so a huge workspace cannot hold a
// request open forever.
const directoryMaxPages = 50

// ListUsers pages users.list, skipping deleted users.
func (a HTTPAPI) ListUsers(ctx context.Context) ([]DirEntry, error) {
	var out []DirEntry
	err := a.pages(ctx, "users.list", url.Values{}, func(raw func(any) error) error {
		var r struct {
			Members []struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Deleted bool   `json:"deleted"`
				IsBot   bool   `json:"is_bot"`
				Profile struct {
					RealName    string `json:"real_name"`
					DisplayName string `json:"display_name"`
					Image48     string `json:"image_48"`
				} `json:"profile"`
			} `json:"members"`
		}
		if err := raw(&r); err != nil {
			return err
		}
		for _, m := range r.Members {
			if m.Deleted || m.ID == "USLACKBOT" {
				continue
			}
			out = append(out, DirEntry{ID: m.ID, Name: m.Name, RealName: m.Profile.RealName, DisplayName: m.Profile.DisplayName, Avatar: m.Profile.Image48, IsBot: m.IsBot})
		}
		return nil
	})
	return out, err
}

// ListChannels pages conversations.list (public and private the token
// can see), skipping archived channels.
func (a HTTPAPI) ListChannels(ctx context.Context) ([]DirEntry, error) {
	var out []DirEntry
	form := url.Values{"types": {"public_channel,private_channel"}, "exclude_archived": {"true"}}
	err := a.pages(ctx, "conversations.list", form, func(raw func(any) error) error {
		var r struct {
			Channels []struct {
				ID         string `json:"id"`
				Name       string `json:"name"`
				IsArchived bool   `json:"is_archived"`
				IsPrivate  bool   `json:"is_private"`
			} `json:"channels"`
		}
		if err := raw(&r); err != nil {
			return err
		}
		for _, c := range r.Channels {
			if !c.IsArchived {
				out = append(out, DirEntry{ID: c.ID, Name: c.Name, IsPrivate: c.IsPrivate})
			}
		}
		return nil
	})
	return out, err
}

// pages calls method once per cursor page, handing each page to each.
func (a HTTPAPI) pages(ctx context.Context, method string, form url.Values, each func(raw func(any) error) error) error {
	cursor := ""
	for i := 0; i < directoryMaxPages; i++ {
		f := url.Values{}
		for k, v := range form {
			f[k] = v
		}
		f.Set("limit", "200")
		if cursor != "" {
			f.Set("cursor", cursor)
		}
		var meta struct {
			Meta struct {
				NextCursor string `json:"next_cursor"`
			} `json:"response_metadata"`
		}
		var body []byte
		if err := a.callRetrying(ctx, method, f, &rawBody{&body}); err != nil {
			return err
		}
		if err := unmarshal(body, &meta); err != nil {
			return err
		}
		if err := each(func(v any) error { return unmarshal(body, v) }); err != nil {
			return err
		}
		if cursor = meta.Meta.NextCursor; cursor == "" {
			return nil
		}
	}
	return nil
}

// directoryMaxRetries bounds how often one page is retried after a 429, and
// directoryMaxWait how long a single Retry-After may hold the picker.
const (
	directoryMaxRetries = 3
	directoryMaxWait    = 30 * time.Second
)

// callRetrying is call that waits out Slack's Retry-After on a 429 instead
// of failing the whole listing: users.list and conversations.list are tier
// 2, and a large workspace's paging can trip it on its own.
func (a HTTPAPI) callRetrying(ctx context.Context, method string, form url.Values, out any) error {
	for attempt := 0; ; attempt++ {
		err := a.call(ctx, method, form, out)
		var ra *remote.RetryAfterError
		if err == nil || !errors.As(err, &ra) || attempt >= directoryMaxRetries || ra.After > directoryMaxWait {
			return err
		}
		t := time.NewTimer(ra.After)
		select {
		case <-ctx.Done():
			t.Stop()
			return err
		case <-t.C:
		}
	}
}

// DirectoryAPI lists a workspace's users and channels.
type DirectoryAPI interface {
	ListUsers(ctx context.Context) ([]DirEntry, error)
	ListChannels(ctx context.Context) ([]DirEntry, error)
}

// DirectoryRefresh is how often a search may re-page a listing. Between
// refreshes, and whenever one fails, the last response answers.
const DirectoryRefresh = 30 * time.Second

// directoryCooldown holds off refreshes after a failure Slack did not put a
// Retry-After on.
const directoryCooldown = 30 * time.Second

// Directory keeps the last response of each listing per key (workspace +
// identity + kind).
type Directory struct {
	mu       sync.Mutex
	rows     map[string]dirCache
	inflight map[string]*dirFetch
	now      func() time.Time
}

// dirFetch is one listing in progress. Every keystroke of a search box is
// its own request, so without it each key typed before the first listing
// lands would page the whole workspace again and trip the rate limit.
type dirFetch struct {
	done    chan struct{}
	entries []DirEntry
	err     error
}

type dirCache struct {
	at        time.Time
	entries   []DirEntry
	coolUntil time.Time
}

// NewDirectory returns an empty cache.
func NewDirectory() *Directory {
	return &Directory{rows: map[string]dirCache{}, inflight: map[string]*dirFetch{}, now: time.Now}
}

// Search filters the last response of kind's listing under key and returns
// at most DirectoryMaxResults entries whose names contain query,
// case-insensitively; an empty query returns the first entries by name.
// Only the first search of a key waits on Slack: later ones answer from the
// last response at once and refresh it in the background at most once per
// DirectoryRefresh, never while Slack's Retry-After runs — so retyping or
// reopening a picker cannot fail with "rate limited".
func (d *Directory) Search(ctx context.Context, api DirectoryAPI, key, kind, query string) ([]DirEntry, error) {
	if kind != DirUsers && kind != DirChannels {
		return nil, errors.New("kind must be users or channels")
	}
	key = kind + "|" + key
	d.mu.Lock()
	c, ok := d.rows[key]
	d.mu.Unlock()
	if ok {
		if now := d.now(); now.Sub(c.at) >= DirectoryRefresh && now.After(c.coolUntil) {
			go func() { _, _ = d.fetch(context.WithoutCancel(ctx), api, key, kind) }()
		}
	} else {
		list, err := d.fetch(ctx, api, key, kind)
		if err != nil {
			if strings.Contains(err.Error(), "missing_scope") {
				scopes := "users:read"
				if kind == DirChannels {
					scopes = "channels:read and groups:read"
				}
				return nil, &MissingScopeError{Kind: kind, Scopes: scopes}
			}
			return nil, err
		}
		c = dirCache{entries: list}
	}
	q := strings.ToLower(strings.TrimLeft(strings.TrimSpace(query), "@#"))
	out := []DirEntry{}
	for _, e := range c.entries {
		if q == "" || matches(e, q) {
			out = append(out, e)
			if len(out) == DirectoryMaxResults {
				break
			}
		}
	}
	return out, nil
}

// fetch lists kind once per key at a time: a search arriving while a listing
// is in flight waits for that listing rather than starting another.
func (d *Directory) fetch(ctx context.Context, api DirectoryAPI, key, kind string) ([]DirEntry, error) {
	d.mu.Lock()
	f, ok := d.inflight[key]
	if !ok {
		f = &dirFetch{done: make(chan struct{})}
		d.inflight[key] = f
		go d.list(context.WithoutCancel(ctx), api, key, kind, f)
	}
	d.mu.Unlock()
	select {
	case <-f.done:
		return f.entries, f.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// list runs one listing detached from the request that started it: that
// request may be cancelled (the user typed on) while others still wait, and
// the finished listing fills the cache for them either way.
func (d *Directory) list(ctx context.Context, api DirectoryAPI, key, kind string, f *dirFetch) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if kind == DirUsers {
		f.entries, f.err = api.ListUsers(ctx)
	} else {
		f.entries, f.err = api.ListChannels(ctx)
	}
	if f.err == nil {
		sort.SliceStable(f.entries, func(i, j int) bool {
			return strings.ToLower(f.entries[i].Name) < strings.ToLower(f.entries[j].Name)
		})
	}
	d.mu.Lock()
	if f.err == nil {
		d.rows[key] = dirCache{at: d.now(), entries: f.entries}
	} else if c, ok := d.rows[key]; ok {
		wait := directoryCooldown
		var ra *remote.RetryAfterError
		if errors.As(f.err, &ra) && ra.After > 0 {
			wait = ra.After
		}
		c.coolUntil = d.now().Add(wait)
		d.rows[key] = c
	}
	delete(d.inflight, key)
	d.mu.Unlock()
	close(f.done)
}

func matches(e DirEntry, q string) bool {
	for _, s := range []string{e.Name, e.RealName, e.DisplayName, e.ID} {
		if s != "" && strings.Contains(strings.ToLower(s), q) {
			return true
		}
	}
	return false
}

// rawBody lets call hand back the whole reply for a second decode.
type rawBody struct{ b *[]byte }

func (r *rawBody) UnmarshalJSON(b []byte) error {
	*r.b = append([]byte(nil), b...)
	return nil
}

func unmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
