// Package pluginremote is the remote.Source of a service plugin that
// declares remote_source: Send / Receive / Done go to the plugin's internal
// /_wick/remote/* paths over its unix socket, and events arrive as SSE in
// the v1 event schema, so nothing is converted.
package pluginremote

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/remote"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/entity"
	wickentity "github.com/yogasw/wick/pkg/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// Persona kind, adapter kind, provider key, and agent_channels row type.
const (
	Kind        = "plugin-remote"
	AdapterKind = "plugin"
	ProviderKey = "plugin-remote/plugin-remote"
	RowType     = "plugin-remote"
)

func init() {
	if wickplugin.RemoteEventSchema != remote.SchemaVersion {
		panic("pluginremote: plugin event schema differs from the runner's")
	}
	remote.Register(remote.Adapter{Kind: AdapterKind, Label: "Plugin", Schema: remote.SchemaVersion,
		Listen: []remote.ListenMode{remote.ListenPush}})
}

// Transport resolves the running plugin's socket transport.
type Transport func(key string) (http.RoundTripper, error)

// Source is one agent's plugin adapter.
type Source struct {
	Key string
	// AgentID and Config ride on every turn: the remote agent and its
	// per-agent config in plaintext. Set them before the first Send.
	AgentID   string
	Config    map[string]string
	transport Transport
	limits    remote.Limits

	// injMu guards inject and cancel: whether the plugin's describe said
	// "inject": true / "cancel": true, asked once (a failed ask is asked
	// again).
	injMu    sync.Mutex
	injKnown bool
	inject   bool
	cancel   bool
	fields   bool
}

// pluginCeiling bounds a plugin turn that keeps showing life. A plugin
// remote can work far past Max; while it still sends text or a working
// status the turn goes on, so its answer is not cut off and lost at Max.
const pluginCeiling = 2 * time.Hour

// pluginBusyFresh is how long a plugin's working status counts without a
// repeat: a plugin that polls its remote says it again every poll, one
// that hung stops saying it and its turn ends at Max.
const pluginBusyFresh = 5 * time.Minute

// NewSource wraps plugin key.
func NewSource(key string, t Transport) *Source {
	return &Source{Key: key, transport: t, limits: remote.Limits{
		Max: 10 * time.Minute, Ceiling: pluginCeiling, BusyFresh: pluginBusyFresh}}
}

func (s *Source) Kind() string                { return AdapterKind }
func (s *Source) Label() string               { return "plugin-remote (" + s.Key + ")" }
func (s *Source) Listen() []remote.ListenMode { return []remote.ListenMode{remote.ListenPush} }
func (s *Source) Limits() remote.Limits       { return s.limits }

func (s *Source) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	rt, err := s.transport(s.Key)
	if err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://plugin"+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Transport: rt}).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		return nil, fmt.Errorf("plugin %s: %s %s: %d %s", s.Key, method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return resp, nil
}

type state struct {
	ContextID string `json:"context_id"`
	// Options are the session's SessionField values: the user's answers
	// until the remote session exists, then the values the plugin reports
	// it used (falling back to the answers sent).
	Options map[string]string `json:"options,omitempty"`
}

func statePath(dir string) string { return filepath.Join(dir, "plugin-remote.json") }

func loadState(dir string) state {
	var st state
	if dir != "" {
		if b, err := os.ReadFile(statePath(dir)); err == nil {
			_ = json.Unmarshal(b, &st)
		}
	}
	return st
}

func saveState(dir string, st state) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(st)
	return os.WriteFile(statePath(dir), b, 0o600)
}

// ErrSessionStarted is returned by SaveSessionOptions once the remote
// session exists: its values can no longer change (start a new chat).
var ErrSessionStarted = errors.New("the remote session has started; start a new chat to change these values")

// SessionOptions returns the session's SessionField values and whether
// they are locked because the remote session already exists.
func SessionOptions(dir string) (opts map[string]string, locked bool) {
	st := loadState(dir)
	return st.Options, st.ContextID != ""
}

// SaveSessionOptions keeps opts (empty values dropped) as the answers the
// session's first turn sends, until the remote session exists.
func SaveSessionOptions(dir string, opts map[string]string) error {
	if dir == "" {
		return errors.New("no session directory")
	}
	st := loadState(dir)
	if st.ContextID != "" {
		return ErrSessionStarted
	}
	st.Options = map[string]string{}
	for k, v := range opts {
		if v = strings.TrimSpace(v); v != "" {
			st.Options[k] = v
		}
	}
	return saveState(dir, st)
}

// ResumeID names the session by the plugin's conversation id.
func (s *Source) ResumeID(dir string) string {
	if st := loadState(dir); st.ContextID != "" {
		return "plugin:" + s.Key + ":" + st.ContextID
	}
	return ""
}

// Send posts the turn (with the conversation id of earlier turns) and keeps
// the id the plugin answers with.
func (s *Source) Send(ctx context.Context, turn remote.Turn) (remote.Handle, error) {
	st := loadState(turn.SessionDir)
	resp, err := s.do(ctx, http.MethodPost, wickplugin.RemotePathSend,
		wickplugin.RemoteTurn{Text: store.StripSenderLines(turn.Text), SessionID: turn.SessionID, ContextID: st.ContextID,
			AgentID: s.AgentID, Config: s.Config, Options: st.Options})
	if err != nil {
		return remote.Handle{}, err
	}
	defer resp.Body.Close()
	var res wickplugin.RemoteSendResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil || res.Handle == "" {
		return remote.Handle{}, errors.New("plugin " + s.Key + ": send returned no handle")
	}
	if res.ContextID != "" && res.ContextID != st.ContextID && turn.SessionDir != "" {
		// The values the remote session was created with lock here; the
		// plugin's report wins over the answers sent (defaults resolved).
		if len(res.Options) > 0 {
			st.Options = res.Options
		}
		st.ContextID = res.ContextID
		_ = saveState(turn.SessionDir, st)
	}
	return remote.Handle{ID: res.Handle}, nil
}

// Receive streams h's events until the plugin closes the stream.
func (s *Source) Receive(ctx context.Context, h remote.Handle) (<-chan remote.Event, error) {
	resp, err := s.do(ctx, http.MethodGet, wickplugin.RemotePathEvents+"?handle="+h.ID, nil)
	if err != nil {
		return nil, err
	}
	ch := make(chan remote.Event, 16)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for sc.Scan() {
			data, ok := strings.CutPrefix(sc.Text(), "data: ")
			if !ok {
				continue
			}
			var ev remote.Event
			if json.Unmarshal([]byte(data), &ev) != nil || ev.Kind == "" {
				continue
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
			if ev.Terminal() {
				return
			}
		}
	}()
	return ch, nil
}

// Cancel stops h's turn on the remote's side (Stop in wick), when the
// plugin's describe says "cancel": true; otherwise there is nothing to
// call and the remote may keep working.
func (s *Source) Cancel(ctx context.Context, h remote.Handle) error {
	if !s.caps(ctx).cancel {
		return nil
	}
	resp, err := s.do(ctx, http.MethodPost, wickplugin.RemotePathCancel, map[string]string{"handle": h.ID})
	if err == nil {
		resp.Body.Close()
	}
	return err
}

// End releases h on the plugin side.
func (s *Source) End(h remote.Handle) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := s.do(ctx, http.MethodPost, wickplugin.RemotePathDone, map[string]string{"handle": h.ID})
	if err == nil {
		resp.Body.Close()
	}
}

func (s *Source) Describe(ctx context.Context) (remote.Description, error) {
	d := remote.Description{Kind: AdapterKind, Target: s.Key, Name: s.Key}
	resp, err := s.do(ctx, http.MethodGet, wickplugin.RemotePathDescribe, nil)
	if err != nil {
		return d, err
	}
	defer resp.Body.Close()
	var out struct {
		Name, Detail  string
		Inject        bool
		Cancel        bool
		SessionFields bool `json:"session_fields"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	s.injMu.Lock()
	s.injKnown, s.inject, s.cancel, s.fields = true, out.Inject, out.Cancel, out.SessionFields
	s.injMu.Unlock()
	if out.Name != "" {
		d.Name = out.Name
	}
	d.Detail = out.Detail
	return d, nil
}

// capsOf is what the plugin's describe declares beyond the base RPC.
type capsOf struct{ inject, cancel, fields bool }

// caps asks describe once what the plugin takes beyond the base RPC; a
// plugin that does not declare inject / cancel never gets those paths.
func (s *Source) caps(ctx context.Context) capsOf {
	s.injMu.Lock()
	defer s.injMu.Unlock()
	if s.injKnown {
		return capsOf{s.inject, s.cancel, s.fields}
	}
	resp, err := s.do(ctx, http.MethodGet, wickplugin.RemotePathDescribe, nil)
	if err != nil {
		return capsOf{}
	}
	defer resp.Body.Close()
	var out struct {
		Inject        bool `json:"inject"`
		Cancel        bool `json:"cancel"`
		SessionFields bool `json:"session_fields"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	s.injKnown, s.inject, s.cancel, s.fields = true, out.Inject, out.Cancel, out.SessionFields
	return capsOf{s.inject, s.cancel, s.fields}
}

// SessionFields asks the plugin which values a new session of this agent
// takes (defaults resolved from the agent's Config); nil when the plugin
// declares none.
func (s *Source) SessionFields(ctx context.Context) ([]wickplugin.SessionField, error) {
	if !s.caps(ctx).fields {
		return nil, nil
	}
	resp, err := s.do(ctx, http.MethodPost, wickplugin.RemotePathSessionFields,
		wickplugin.RemoteSessionFieldsRequest{AgentID: s.AgentID, Config: s.Config})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Fields []wickplugin.SessionField `json:"fields"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Fields, nil
}

func (s *Source) Inject(ctx context.Context, h remote.Handle, turn remote.Turn) error {
	if !s.caps(ctx).inject {
		return remote.ErrInjectUnsupported
	}
	resp, err := s.do(ctx, http.MethodPost, wickplugin.RemotePathInject,
		wickplugin.RemoteInject{Handle: h.ID, Text: store.StripSenderLines(turn.Text)})
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Test pings Describe.
func (s *Source) Test(ctx context.Context) remote.TestResult {
	start := time.Now()
	_, err := s.Describe(ctx)
	r := remote.TestResult{OK: err == nil, State: "ok", LatencyMS: time.Since(start).Milliseconds()}
	if err != nil {
		r.State, r.Error = "error", err.Error()
	}
	return r
}

// Config is a plugin remote agent's settings row.
type Config struct {
	AgentID     string `json:"agent_id"`
	OwnerUserID string `json:"owner_user_id"`
	PluginKey   string `json:"plugin_key"`
	// Usage is UsageByMention once the agent's mention policy decides who
	// may reach it; "" for a row saved before, which took no agent's turn.
	Usage string `json:"usage,omitempty"`
	// Values are the agent's RemoteConfigs answers by key. A secret's value
	// is stored encrypted (wick_cenc_); see SealValues / OpenValues.
	Values map[string]string `json:"values,omitempty"`
}

// Codec encrypts secret values at rest — the configs codec connector
// secrets use.
type Codec interface {
	EncryptSecret(plain string) (string, error)
	DecryptSecret(token string) (string, error)
}

// SealValues encrypts the values of the secret keys of decl.
func SealValues(codec Codec, decl []wickentity.Config, values map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(values))
	for k, v := range values {
		out[k] = v
	}
	if codec == nil {
		return out, nil
	}
	for _, d := range decl {
		if v := out[d.Key]; d.IsSecret && v != "" {
			sealed, err := codec.EncryptSecret(v)
			if err != nil {
				return nil, fmt.Errorf("encrypt %s: %w", d.Key, err)
			}
			out[d.Key] = sealed
		}
	}
	return out, nil
}

// OpenValues decrypts stored values for the plugin; a token that is not
// encrypted passes through.
func OpenValues(codec Codec, values map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(values))
	for k, v := range values {
		if codec != nil && v != "" {
			plain, err := codec.DecryptSecret(v)
			if err != nil {
				return nil, fmt.Errorf("decrypt %s: %w", k, err)
			}
			v = plain
		}
		out[k] = v
	}
	return out, nil
}

// UsageByMention marks a plugin remote agent whose mention policy decides
// who may reach it.
const UsageByMention = "mention"

// Store keeps Config in agent_channels (type plugin-remote, name agent id).
type Store struct{ db *gorm.DB }

// NewStore wraps db.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// Load returns agentID's settings; ok=false when it has none.
func (s *Store) Load(agentID string) (Config, bool, error) {
	var rows []entity.AgentChannel
	if err := s.db.Where("type = ? AND name = ?", RowType, agentID).Limit(1).Find(&rows).Error; err != nil || len(rows) == 0 {
		return Config{}, false, err
	}
	var c Config
	err := json.Unmarshal([]byte(rows[0].Config), &c)
	return c, err == nil, err
}

// Update rewrites an existing row's settings.
func (s *Store) Update(c Config) error {
	data, _ := json.Marshal(c)
	return s.db.Model(&entity.AgentChannel{}).Where("type = ? AND name = ?", RowType, c.AgentID).
		Updates(map[string]any{"config": string(data), "updated_at": time.Now()}).Error
}

// Save writes c.
func (s *Store) Save(c Config) error {
	data, _ := json.Marshal(c)
	now := time.Now()
	owner := c.OwnerUserID
	return s.db.Create(&entity.AgentChannel{
		ID: RowType + ":" + c.AgentID, Type: RowType, Name: c.AgentID, UserID: &owner,
		Enabled: true, Config: string(data), CreatedAt: now, UpdatedAt: now,
	}).Error
}
