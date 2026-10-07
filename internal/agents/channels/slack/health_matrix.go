// Package slack — health_matrix.go: the per-feature health matrix (§8.3).
//
// Scopes are read from the X-OAuth-Scopes header Slack puts on every Web
// API response — one auth.test proves the whole list, where the dry-run
// probes of health.go can only prove one method each. Events have no such
// header: they are judged from the app manifest when one is known, else
// from what this instance has actually received since boot.

package slack

import (
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	slackgo "github.com/slack-go/slack"
)

// Matrix cell / row verdicts.
const (
	StatusOK    = "ok"
	StatusWarn  = "warn"
	StatusError = "error"
	StatusOff   = "off"
	// StatusPending is an event not seen since boot when no manifest says
	// whether it is subscribed: unknown, not wrong. A fresh app or a fresh
	// restart has simply not had one yet, so it never makes a row warn.
	StatusPending = "pending"
)

// EventsFrom values: how a row's events were judged.
const (
	EventsFromManifest = "manifest"
	EventsFromReceived = "received"
)

// offHint labels an item of a feature that is switched off.
const offHint = "off — not checked"

// MatrixItem is one scope or event of a feature row.
type MatrixItem struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Hint   string `json:"hint,omitempty"`
}

// MatrixRow is one feature of the matrix.
type MatrixRow struct {
	Key    string       `json:"key"`
	Label  string       `json:"label"`
	Need   Need         `json:"need"`
	Status string       `json:"status"`
	Scopes []MatrixItem `json:"scopes"`
	Events []MatrixItem `json:"events"`
	// EventsFrom says where the event verdicts come from: the manifest
	// (certain) or the events received since boot (only proves what came).
	EventsFrom string `json:"events_from"`
}

// MatrixInput is everything a verdict depends on. Manifest* are nil when
// no app configuration token is set (events then fall back to Seen).
type MatrixInput struct {
	TokenScopes    []string
	ManifestScopes []string
	ManifestEvents []string
	Seen           map[string]time.Time
	Active         func(key string) bool
}

// BuildMatrix judges every feature of the requirement table.
func BuildMatrix(in MatrixInput) []MatrixRow {
	rows := make([]MatrixRow, 0, len(requirements))
	for _, f := range Requirements() {
		r := MatrixRow{Key: f.Key, Label: f.Label, Need: f.Need, Scopes: []MatrixItem{}, Events: []MatrixItem{}, EventsFrom: EventsFromReceived}
		if in.ManifestEvents != nil {
			r.EventsFrom = EventsFromManifest
		}
		active := in.Active == nil || in.Active(f.Key)
		miss := StatusError
		if f.Need == NeedOptional || f.Need == NeedInfo || f.Key == FeatureInstant {
			miss = StatusWarn
		}
		for _, sc := range f.BotScopes {
			it := MatrixItem{Name: sc, Status: StatusOK}
			switch {
			case !active:
				it.Status, it.Hint = StatusOff, offHint
			case slices.Contains(in.TokenScopes, sc):
			case in.ManifestScopes != nil && slices.Contains(in.ManifestScopes, sc):
				it.Status, it.Hint = miss, "in the manifest but not in the token — reinstall the app to your workspace"
			default:
				it.Status, it.Hint = miss, "add the bot scope "+sc+" under OAuth & Permissions, then reinstall the app"
			}
			r.Scopes = append(r.Scopes, it)
		}
		for _, ev := range f.Events {
			it := MatrixItem{Name: ev, Status: StatusOK}
			switch {
			case !active:
				it.Status, it.Hint = StatusOff, offHint
			case in.ManifestEvents != nil:
				if !slices.Contains(in.ManifestEvents, ev) {
					it.Status, it.Hint = miss, "add "+ev+" under Event Subscriptions, then reinstall the app"
				}
			default:
				if _, ok := in.Seen[ev]; !ok {
					it.Status, it.Hint = StatusPending, "not seen yet — mention the bot or send it a DM to confirm"
				}
			}
			r.Events = append(r.Events, it)
		}
		r.Status = worst(active, r.Scopes, r.Events)
		rows = append(rows, r)
	}
	return rows
}

func worst(active bool, groups ...[]MatrixItem) string {
	if !active {
		return StatusOff
	}
	out := StatusOK
	for _, g := range groups {
		for _, it := range g {
			switch {
			case it.Status == StatusError:
				return StatusError
			case it.Status == StatusWarn:
				out = StatusWarn
			}
		}
	}
	return out
}

// ParseOAuthScopes splits an X-OAuth-Scopes header value.
func ParseOAuthScopes(h string) []string {
	out := []string{}
	for _, s := range strings.Split(h, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// scopeRecorder keeps the X-OAuth-Scopes header of the last response.
type scopeRecorder struct {
	base   http.RoundTripper
	mu     sync.Mutex
	scopes string
}

func (r *scopeRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := r.base.RoundTrip(req)
	if err == nil {
		if h := resp.Header.Get("X-OAuth-Scopes"); h != "" {
			r.mu.Lock()
			r.scopes = h
			r.mu.Unlock()
		}
	}
	return resp, err
}

// TokenScopes runs auth.test with token and returns the scopes Slack says
// it carries. apiURL "" = Slack itself.
func TokenScopes(token, apiURL string) ([]string, error) {
	rec := &scopeRecorder{base: http.DefaultTransport}
	opts := []slackgo.Option{slackgo.OptionHTTPClient(&http.Client{Transport: rec, Timeout: 10 * time.Second})}
	if apiURL != "" {
		opts = append(opts, slackgo.OptionAPIURL(apiURL))
	}
	if _, err := slackgo.New(token, opts...).AuthTest(); err != nil {
		return nil, err
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return ParseOAuthScopes(rec.scopes), nil
}

// observedEventName is the subscription name of an inner Events API event:
// message events are split by where they were posted.
func observedEventName(innerType, channelType string) string {
	if innerType != "message" {
		return innerType
	}
	switch channelType {
	case "im":
		return "message.im"
	case "mpim":
		return "message.mpim"
	case "group":
		return "message.groups"
	default:
		return "message.channels"
	}
}

// observeEvent records that this instance received an event type.
func (s *Channel) observeEvent(name string) {
	if name != "" {
		s.eventsSeen.Store(name, time.Now())
	}
}

// SeenEvents is a snapshot of every event type received since boot.
func (s *Channel) SeenEvents() map[string]time.Time {
	out := map[string]time.Time{}
	s.eventsSeen.Range(func(k, v any) bool {
		out[k.(string)] = v.(time.Time)
		return true
	})
	return out
}

// FeatureMatrix judges this instance. agentView marks a Team agent's own
// bot, where the agent view is a live feature.
func (s *Channel) FeatureMatrix(agentView bool) []MatrixRow {
	cfg := s.snapshot()
	in := MatrixInput{
		Seen: s.SeenEvents(),
		Active: func(key string) bool {
			if key == FeatureInstant {
				return s.ServesInstant()
			}
			return FeatureActive(key, cfg, agentView)
		},
	}
	if cfg.BotToken != "" {
		if sc, err := TokenScopes(cfg.BotToken, s.apiURLOverride()); err == nil {
			in.TokenScopes = sc
		}
	}
	return BuildMatrix(in)
}

func (s *Channel) apiURLOverride() string {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	return s.matrixAPIURL
}
