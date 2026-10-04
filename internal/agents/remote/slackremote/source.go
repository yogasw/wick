package slackremote

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/remote"
)

func init() {
	remote.Register(remote.Adapter{
		Kind: AdapterKind, Label: "Slack", Schema: remote.SchemaVersion,
		// Events wick already receives first; conversations.replies polling
		// covers what the app cannot see (a DM sent as a user, a channel
		// the app is not in).
		Listen: []remote.ListenMode{remote.ListenPush, remote.ListenPull},
	})
}

// Deps is what a Source needs from the rest of wick.
type Deps struct {
	// API posts and reads with the token of the agent's identity.
	API API
	// WickIDs lists the user and bot ids of every Slack identity wick
	// posts as, so a turn never takes wick's own message — or another wick
	// agent's — for a reply.
	WickIDs func() []string
	// Router is the shared listener; Shared when nil.
	Router *Router
}

// Source is the Slack adapter of one agent.
type Source struct {
	cfg  Config
	deps Deps

	mu       sync.Mutex
	selfUser string
	selfBot  string
	cur      *tracker
	dir      string
}

// NewSource wraps cfg.
func NewSource(cfg Config, deps Deps) *Source {
	if deps.Router == nil {
		deps.Router = Shared
	}
	if deps.WickIDs == nil {
		deps.WickIDs = WickIDs
	}
	return &Source{cfg: cfg, deps: deps}
}

func (s *Source) Kind() string { return AdapterKind }
func (s *Source) Label() string {
	return "slack-remote (" + firstNonEmpty(s.cfg.TargetName, s.cfg.Channel, s.cfg.User) + ")"
}
func (s *Source) Listen() []remote.ListenMode {
	return []remote.ListenMode{remote.ListenPush, remote.ListenPull}
}
func (s *Source) Limits() remote.Limits { return remote.Limits{Max: s.cfg.Max(), Idle: s.cfg.Idle()} }

// ResumeID names the session by its thread.
func (s *Source) ResumeID(dir string) string {
	if st := LoadState(dir); st.ThreadTS != "" {
		return "slack:" + st.Channel + ":" + st.ThreadTS
	}
	return ""
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func newToken() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// identity resolves (once) who the token posts as.
func (s *Source) identity(ctx context.Context) (string, string, error) {
	s.mu.Lock()
	u, b := s.selfUser, s.selfBot
	s.mu.Unlock()
	if u != "" || b != "" {
		return u, b, nil
	}
	u, b, err := s.deps.API.AuthTest(ctx)
	if err != nil {
		return "", "", err
	}
	s.mu.Lock()
	s.selfUser, s.selfBot = u, b
	s.mu.Unlock()
	return u, b, nil
}

func (s *Source) ignoreSet(selfUser, selfBot string) map[string]bool {
	ig := map[string]bool{}
	for _, id := range []string{selfUser, selfBot} {
		if id != "" {
			ig[id] = true
		}
	}
	if s.deps.WickIDs != nil {
		for _, id := range s.deps.WickIDs() {
			if id != "" && id != s.cfg.User && id != s.cfg.MentionID {
				ig[id] = true
			}
		}
	}
	return ig
}

// accept is the "who is heard" filter: the target only, or anyone but
// wick. A target with no known id (a thread, a channel without a mention)
// hears anyone but wick.
func (s *Source) accept() func(Message) bool {
	want := s.cfg.User
	if s.cfg.Target != TargetDM {
		want = s.cfg.MentionID
	}
	if s.cfg.EffectiveListen() == ListenAnyone || want == "" {
		return func(Message) bool { return true }
	}
	return func(m Message) bool { return m.User == want || m.BotID == want }
}

// place is where this session's turns go: the channel and, after the
// first turn, the thread.
func (s *Source) place(ctx context.Context, st State) (string, string, error) {
	switch s.cfg.Target {
	case TargetDM:
		if st.Channel != "" {
			return st.Channel, st.ThreadTS, nil
		}
		ch, err := s.deps.API.OpenDM(ctx, s.cfg.User)
		return ch, "", err
	case TargetThread:
		return s.cfg.Channel, s.cfg.ThreadTS, nil
	default:
		return s.cfg.Channel, st.ThreadTS, nil
	}
}

// TimeoutHint says why a Slack turn may have gone unanswered: many bots
// answer only when @-mentioned.
func (s *Source) TimeoutHint() string {
	switch {
	case !s.cfg.MentionOn():
		return "Slack bots often reply only when @mentioned — turn on \"Always @mention the target\" in Settings › Remote."
	case s.cfg.TargetID() == "":
		return "Slack bots often reply only when @mentioned — set the bot's ID under Mention in Settings › Remote."
	}
	return ""
}

// outgoing is the text posted for a turn. With the mention on, every
// turn — the first and each follow-up in the thread — starts with the
// target's @-mention, unless the text already mentions it.
func (s *Source) outgoing(text, token string) string {
	if id := s.cfg.TargetID(); s.cfg.MentionOn() && id != "" && !strings.Contains(text, "<@"+id+">") {
		text = "<@" + id + "> " + text
	}
	if token != "" {
		text = strings.TrimRight(text, "\n") + "\n\n" + MarkerInstruction(token)
	}
	return text
}

// Send posts the turn: a new message for a session's first turn (its ts
// becomes the session's thread), a thread reply after that.
func (s *Source) Send(ctx context.Context, turn remote.Turn) (remote.Handle, error) {
	selfUser, selfBot, err := s.identity(ctx)
	if err != nil {
		return remote.Handle{}, errors.New("Slack: " + err.Error())
	}
	st := LoadState(turn.SessionDir)
	channel, threadTS, err := s.place(ctx, st)
	if err != nil {
		return remote.Handle{}, errors.New("Slack: " + err.Error())
	}
	token := ""
	if s.cfg.MarkerOn() {
		token = newToken()
	}
	ts, err := s.deps.API.Post(ctx, channel, s.outgoing(turn.Text, token), threadTS)
	if err != nil {
		return remote.Handle{}, errors.New("Slack: " + err.Error())
	}
	if threadTS == "" {
		threadTS = ts
	}
	st.Channel, st.ThreadTS, st.LastTS = channel, threadTS, ts
	saveState(turn.SessionDir, st)
	t := newTracker(channel, threadTS, ts, token, s.cfg.Target == TargetDM, s.ignoreSet(selfUser, selfBot), s.accept())
	s.mu.Lock()
	s.cur, s.dir = t, turn.SessionDir
	s.mu.Unlock()
	s.deps.Router.add(t)
	return remote.Handle{ID: threadKey(channel, threadTS), Cursor: ts}, nil
}

func (s *Source) current() *tracker {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

// Receive returns the events the shared router hands this turn.
func (s *Source) Receive(context.Context, remote.Handle) (<-chan remote.Event, error) {
	t := s.current()
	if t == nil {
		return nil, remote.ErrPushUnavailable
	}
	return t.events, nil
}

// Fetch polls the thread (and, for a DM, the channel) for what the events
// did not bring.
func (s *Source) Fetch(ctx context.Context, h remote.Handle) ([]remote.Event, remote.Handle, error) {
	t := s.current()
	if t == nil {
		return nil, h, nil
	}
	out, err := s.fetch(ctx, t)
	if err != nil {
		return nil, h, err
	}
	t.mu.Lock()
	h.Cursor = t.lastTS
	t.mu.Unlock()
	return out, h, nil
}

func (s *Source) fetch(ctx context.Context, t *tracker) ([]remote.Event, error) {
	msgs, err := s.deps.API.Replies(ctx, t.channel, t.threadTS, t.sentTS)
	if err != nil {
		return nil, err
	}
	if t.topLevel {
		top, err := s.deps.API.History(ctx, t.channel, t.sentTS)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, top...)
	}
	var out []remote.Event
	for _, m := range msgs {
		out = append(out, t.observe(m)...)
	}
	// Push may have delivered the same change; the tracker reports each
	// change once, so whatever is left in its buffer is drained here too.
	for {
		select {
		case ev := <-t.events:
			out = append(out, ev)
			continue
		default:
		}
		break
	}
	return out, nil
}

// Cancel: Slack has no way to stop another bot; the turn just stops
// listening.
func (s *Source) Cancel(context.Context, remote.Handle) error { return nil }

// End stops routing the turn's thread here.
func (s *Source) End(remote.Handle) {
	s.mu.Lock()
	t, dir := s.cur, s.dir
	s.cur = nil
	s.mu.Unlock()
	if t == nil {
		return
	}
	s.deps.Router.remove(t)
	if t.lastTS != "" {
		st := LoadState(dir)
		st.LastTS = t.lastTS
		saveState(dir, st)
	}
}

// ErrNotOwnAccount refuses posting as a Slack account that is not the
// agent creator's own.
var ErrNotOwnAccount = errors.New("you can only post as your own Slack account")

// CheckAccountOwner allows identity user only with an account connected
// by owner (the account's wick user).
func CheckAccountOwner(accountWickUserID, owner string) error {
	if owner == "" || accountWickUserID != owner {
		return ErrNotOwnAccount
	}
	return nil
}

// Describe is the outbound-data warning the wizard shows.
func (s *Source) Describe(context.Context) (remote.Description, error) {
	where := firstNonEmpty(s.cfg.TargetName, s.cfg.Channel, s.cfg.User)
	return remote.Description{
		Kind: AdapterKind, Name: where, Target: s.cfg.Target,
		Warning: "Messages you send to this agent, including other agents' output that mentions it, will be posted to " + where + " in Slack.",
	}, nil
}

// testTimeout bounds Test's wait for the reply.
var testTimeout = 30 * time.Second

// Test posts "ping" and waits for the first reply, by polling.
func (s *Source) Test(ctx context.Context) remote.TestResult {
	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	start := time.Now()
	selfUser, selfBot, err := s.identity(ctx)
	if err != nil {
		return remote.TestResult{State: "auth_failed", Error: err.Error()}
	}
	channel, threadTS, err := s.place(ctx, State{})
	if err != nil {
		return remote.TestResult{State: "send_failed", Error: err.Error()}
	}
	ts, err := s.deps.API.Post(ctx, channel, s.outgoing("ping", ""), threadTS)
	if err != nil {
		return remote.TestResult{State: "send_failed", Error: err.Error()}
	}
	if threadTS == "" {
		threadTS = ts
	}
	t := newTracker(channel, threadTS, ts, "", s.cfg.Target == TargetDM, s.ignoreSet(selfUser, selfBot), s.accept())
	step := 0
	steps := []time.Duration{time.Second, 2 * time.Second, 5 * time.Second}
	for {
		if evs, ferr := s.fetch(ctx, t); ferr == nil {
			for _, ev := range evs {
				if ev.Kind == remote.EventText && ev.Text != "" {
					reply := ev.Text
					if len(reply) > 500 {
						reply = reply[:500] + "…"
					}
					return remote.TestResult{OK: true, State: "replied", LatencyMS: time.Since(start).Milliseconds(), Reply: reply}
				}
			}
		}
		wait := steps[step]
		if step < len(steps)-1 {
			step++
		}
		select {
		case <-ctx.Done():
			return remote.TestResult{State: "no_reply", LatencyMS: time.Since(start).Milliseconds(), Error: "no reply within " + testTimeout.String()}
		case <-time.After(wait):
		}
	}
}
