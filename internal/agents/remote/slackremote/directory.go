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
		if err := a.call(ctx, method, f, &rawBody{&body}); err != nil {
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

// DirectoryAPI lists a workspace's users and channels.
type DirectoryAPI interface {
	ListUsers(ctx context.Context) ([]DirEntry, error)
	ListChannels(ctx context.Context) ([]DirEntry, error)
}

// DirectoryTTL is how long one workspace listing is reused, so typing in
// the search box does not page users.list on every key.
const DirectoryTTL = 5 * time.Minute

// Directory caches listings per key (workspace + identity + kind).
type Directory struct {
	mu   sync.Mutex
	rows map[string]dirCache
	now  func() time.Time
}

type dirCache struct {
	at      time.Time
	entries []DirEntry
}

// NewDirectory returns an empty cache.
func NewDirectory() *Directory {
	return &Directory{rows: map[string]dirCache{}, now: time.Now}
}

// Search lists kind through api (or the cached listing under key) and
// returns at most DirectoryMaxResults entries whose names contain query,
// case-insensitively. An empty query returns the first entries by name.
func (d *Directory) Search(ctx context.Context, api DirectoryAPI, key, kind, query string) ([]DirEntry, error) {
	if kind != DirUsers && kind != DirChannels {
		return nil, errors.New("kind must be users or channels")
	}
	key = kind + "|" + key
	d.mu.Lock()
	c, ok := d.rows[key]
	d.mu.Unlock()
	if !ok || d.now().Sub(c.at) > DirectoryTTL {
		var list []DirEntry
		var err error
		if kind == DirUsers {
			list, err = api.ListUsers(ctx)
		} else {
			list, err = api.ListChannels(ctx)
		}
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
		sort.SliceStable(list, func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) })
		c = dirCache{at: d.now(), entries: list}
		d.mu.Lock()
		d.rows[key] = c
		d.mu.Unlock()
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
