package provider

import (
	"errors"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
)

// Pool is the subset of pool.Pool that Switch needs.
type Pool interface {
	Kill(sessionID, agentName string) error
	Send(ctx context.Context, sessionID, agentName, source, role, text string) error
}

// SwitchOptions controls optional behaviour after a provider switch.
type SwitchOptions struct {
	// Greeting, if non-empty, is sent as a user message to the new agent
	// immediately after the switch so it can introduce itself.
	Greeting string
	// Source is the transport label written into conversation.jsonl ("ui", "slack", etc.).
	Source string
	// UserText is the original raw message (e.g. "#codex") to record as a
	// user turn in conversation.jsonl before the switch events.
	UserText string
	// Notify, if set, is called after the system turn is written so the
	// caller can push a realtime event (e.g. SSE) to connected clients.
	// tag is the new provider tag; steps are the trace lines.
	Notify func(tag string, steps []string)
	// Reply, if set, is called with the confirmation text so every channel
	// (UI SSE, Slack postReply, REST response, etc.) can deliver it back to
	// the user without forwarding to the provider.
	Reply func(text string)
	// ModelID pins a wick instance's WickModel.ID for the next spawn. Only
	// meaningful when tag resolves to a wick instance; ignored otherwise.
	// Empty = use the instance's Default model. Picking a different model
	// on the SAME already-active wick instance is not a no-op switch (it
	// still needs to persist + kill so the next spawn picks it up) even
	// though the provider tag itself is unchanged.
	ModelID string
	// Failover marks an automatic switch along the home's failover chain
	// (PickTarget): the turn is a KindProviderFailover notice, the home is
	// kept and the run's Tried/Hops advance. false = a manual switch,
	// which makes the target the new home and forgets any run.
	Failover bool
	// FailoverReason says why the previous provider stopped ("usage limit
	// reached"); shown on the failover notice. Failover only.
	FailoverReason string
	// ContinueText is what wick sends the target to carry the turn on
	// (ContinueMessage), kept in the notice's extras. Failover only.
	ContinueText string
	// ContinueFor, when set, builds ContinueText from the switch's result
	// — the continue message depends on whether history carried over,
	// which only the switch knows. Its answer is also SwitchResult.Continue.
	// Failover only.
	ContinueFor func(SwitchResult) string
	// MsgKey is FailoverMsgKey of the user turn the run is for. A key
	// other than the stored one (or "") starts a new run (RunFor).
	// Failover only.
	MsgKey string
	// ReturnHome marks the move back up the home's chain at a turn
	// boundary (PickTarget in ReturnHome mode): the home stays, the run
	// is over, and the notice says "back to …". Exclusive with Failover.
	ReturnHome bool
	// ExpectFrom, when set, makes the switch conditional: it is refused
	// with ErrSwitchedMeanwhile unless the agent is still on this
	// instance ("type/name" or bare type). Checked under the agents lock,
	// so a failover and a manual switch cannot both land.
	ExpectFrom string
}

// ErrSwitchedMeanwhile is ApplySwitch's refusal when the agent no longer
// runs on SwitchOptions.ExpectFrom.
var ErrSwitchedMeanwhile = errors.New("provider was switched meanwhile")

// failoverAware is a Pool that can say an agent is being failed over.
type failoverAware interface {
	FailingOver(sessionID, agentName string) bool
}

// SwitchResult is what ApplySwitch did.
type SwitchResult struct {
	// From / To are the "type/name" keys the agent moved between.
	From, To string
	// HistoryCarries: the target resumes this conversation (same type,
	// a resume id it can read). False for another type, a same-type
	// target that cannot reach the transcript, or no transcript yet — the
	// target then starts fresh and a continue must recap the turn.
	HistoryCarries bool
	// Home is the agent's home after the switch.
	Home string
	// Continue is the continue text recorded (ContinueFor / ContinueText).
	Continue string
}

// SwitchGuard, when set, decides whether a switch asked for outside the
// UI's switch endpoint — a "#tag" message on any channel, agentctl — may
// move sessionID to tag. A non-nil error refuses it, and its text says
// why. The agents tool wires it: it knows the session's agent and owner.
var SwitchGuard func(ctx context.Context, sessionID, tag string) error

// CheckSwitch runs SwitchGuard; nil when none is wired.
func CheckSwitch(ctx context.Context, sessionID, tag string) error {
	if SwitchGuard == nil {
		return nil
	}
	return SwitchGuard(ctx, sessionID, tag)
}

// normalizeProviderKey returns "type/name" form. Bare "type" becomes "type/type".
func normalizeProviderKey(key string) string {
	if strings.Contains(key, "/") {
		return key
	}
	return key + "/" + key
}

// Switch changes the provider for sessionID+agentName, persists the change to
// agents.json, records a system turn in conversation.jsonl (with step trace),
// kills the running agent, and optionally sends a greeting to the new agent.
func Switch(layout config.Layout, pool Pool, sessionID, agentName, tag string, opts SwitchOptions) error {
	if _, err := ApplySwitch(layout, sessionID, agentName, tag, opts); err != nil {
		return err
	}

	// Kill async — file already updated, next Send will spawn with new provider.
	// Not while a failover runs: it already stopped the old process, and
	// re-sends the turn to whatever agents.json names — this switch.
	if fa, ok := pool.(failoverAware); !ok || !fa.FailingOver(sessionID, agentName) {
		go func() { _ = pool.Kill(sessionID, agentName) }()
	}

	// Optional greeting to warm up the new agent.
	if opts.Greeting != "" {
		source := opts.Source
		if source == "" {
			source = "system"
		}
		go func() {
			_ = pool.Send(context.Background(), sessionID, agentName, source, "user", opts.Greeting)
		}()
	}

	return nil
}

// ApplySwitch is Switch without touching the running agent: it persists
// the new provider to agents.json (under the session's agents lock) and
// records the switch turn, and says what it did. The caller stops the
// old process — Switch kills it async; a failover kills it synchronously
// before re-sending, so the re-send cannot reach the account that just
// failed. opts.Greeting is Switch's alone and ignored here.
func ApplySwitch(layout config.Layout, sessionID, agentName, tag string, opts SwitchOptions) (SwitchResult, error) {
	if opts.Failover && opts.ReturnHome {
		return SwitchResult{}, fmt.Errorf("a switch is either a failover or a return home, not both")
	}
	instances, err := Load()
	if err != nil {
		return SwitchResult{}, fmt.Errorf("load providers: %w", err)
	}
	// tag may be a bare type ("codex") or a named instance ("codex/gemini_flash").
	// Bare type resolves to the per-type default instance (Name == type).
	wantType, wantName := tag, tag
	if i := strings.IndexByte(tag, '/'); i >= 0 {
		wantType, wantName = tag[:i], tag[i+1:]
	}
	found := false
	var toIns Instance
	for _, ins := range instances {
		if string(ins.Type) == wantType && ins.Name == wantName && !ins.Disabled {
			found, toIns = true, ins
			break
		}
	}
	if !found {
		names := make([]string, 0, len(instances))
		for _, ins := range instances {
			if ins.Disabled {
				continue
			}
			// Default instance (Name == type) shows as "#codex"; a named
			// instance shows as "#codex/gemini_flash".
			label := string(ins.Type)
			if ins.Name != string(ins.Type) {
				label += "/" + ins.Name
			}
			names = append(names, "#"+label)
		}
		return SwitchResult{}, fmt.Errorf("unknown provider %q — available: %s", tag, strings.Join(names, ", "))
	}

	// 1. Persist new provider to agents.json.
	now := time.Now().UTC()
	newKey := wantType + "/" + wantName
	// targetHasHistory reports whether the target provider already ran in this
	// session (has a stored resume id), so the notice can say whether it will
	// pick up its own past turns or start cold.
	targetHasHistory := false
	// sameScope reports whether the switch stays inside one provider type,
	// where the transcript store is shared and the conversation carries over.
	sameScope := false
	// carry is the target type's answer for a same-type switch: whether
	// (and how) the conversation reaches the target instance.
	var carry HistoryCarry
	fromKey := "" // provider active before this switch — recorded in the turn extras
	historyCarries := false
	var home *session.FailoverState // the agent's failover state after the switch
	var refused error               // the switch's own refusal, as opposed to an I/O error
	err = session.UpdateAgents(layout, sessionID, func(agents []session.AgentEntry) ([]session.AgentEntry, bool, error) {
		loaded := session.Session{Agents: agents}
		for i, a := range loaded.Agents {
			if a.Name == agentName {
				fromKey = normalizeProviderKey(a.Provider)
				// A failover moves the agent off the instance that failed,
				// and only that one: anything else means a switch landed
				// after the failure (a person's pick), which wins.
				if opts.ExpectFrom != "" && a.Provider != "" && fromKey != normalizeProviderKey(opts.ExpectFrom) {
					refused = ErrSwitchedMeanwhile
					return nil, false, refused
				}
				sameInstance := normalizeProviderKey(a.Provider) == newKey
				// Reject a no-op switch: same instance AND same (or no) model pin
				// requested. Picking a different model on the same wick instance
				// still needs to persist + kill, so it is NOT treated as a no-op.
				if sameInstance && opts.ModelID == a.ModelID {
					short := wantType
					if wantName != wantType {
						short = newKey
					}
					refused = fmt.Errorf("already using %s", short)
					return nil, false, refused
				}
				fromScope := session.ProviderScope(a.Provider)
				toScope := session.ProviderScope(newKey)
				sameScope = fromScope == toScope
				if a.CLISessionID != "" {
					if loaded.Agents[i].ProviderSessions == nil {
						loaded.Agents[i].ProviderSessions = map[string]string{}
					}
					// Park the outgoing id under its provider TYPE: any instance
					// of that type reads the same transcript store, so any of
					// them can resume it later.
					loaded.Agents[i].ProviderSessions[fromScope] = a.CLISessionID
				}
				loaded.Agents[i].Provider = newKey
				resumeID := session.ResumeIDForScope(loaded.Agents[i].ProviderSessions, toScope)
				targetHasHistory = resumeID != ""
				// Within one type the id carries over — the conversation
				// continues. Across types it becomes whatever the target type
				// recorded before, or empty for a fresh start: handing codex an
				// id claude minted only buys a failed spawn, since neither can
				// read the other's store. Never blanked on a same-scope miss —
				// an empty CLISessionID drops --resume from the next spawn and
				// abandons a conversation still on disk.
				if resumeID != "" && sameScope {
					carry = carriesOver(instanceFor(instances, fromKey), toIns, resumeID)
					if carry.Reason != "" {
						// Say so, and start clean: a resume the target cannot
						// find only buys a failed turn.
						resumeID, targetHasHistory = "", false
						loaded.Agents[i].CLISessionID = ""
					}
				}
				historyCarries = sameScope && resumeID != ""
				if resumeID != "" {
					loaded.Agents[i].CLISessionID = resumeID
				} else if !sameScope {
					loaded.Agents[i].CLISessionID = ""
				}
				// Pin the requested model. All provider types now honor a model
				// pin: wick maps it to a WickModel.ID, the CLI providers pass it
				// to their binary via --model. Empty clears any stale pin (so a
				// switch back doesn't resurrect a foreign instance's choice).
				loaded.Agents[i].ModelID = opts.ModelID
				switch {
				case opts.Failover:
					loaded.Agents[i].Failover = advanceFailover(a, fromKey, opts.MsgKey, now)
				case opts.ReturnHome:
					// Back up the home's chain: the home stays, the run is over.
					if a.Failover != nil {
						st := *a.Failover
						st.EndRun()
						loaded.Agents[i].Failover = &st
					}
				default:
					// A manual pick is the new home; whatever run was in
					// flight is over.
					loaded.Agents[i].Failover = &session.FailoverState{HomeProvider: newKey, HomeModelID: opts.ModelID}
				}
				home = loaded.Agents[i].Failover
				break
			}
		}
		return loaded.Agents, true, nil
	})
	if err != nil {
		if err == refused {
			return SwitchResult{}, err
		}
		return SwitchResult{}, fmt.Errorf("save agents: %w", err)
	}
	res := SwitchResult{From: fromKey, To: newKey, HistoryCarries: historyCarries}
	if home != nil {
		res.Home = home.HomeProvider
	}
	if opts.Failover {
		res.Continue = opts.ContinueText
		if opts.ContinueFor != nil {
			res.Continue = opts.ContinueFor(res)
		}
	}

	// Collapse a run of back-to-back switches: if the tail of the history is
	// nothing but switch artifacts (no real chat since the last switch), drop
	// them so only this switch remains. Keeps the transcript clean when the
	// user flips providers a few times before actually sending a message.
	// Never for a failover: it happens mid-turn, and the manual switch
	// notices before it are part of the story it tells.
	convPath := layout.SessionConversation(sessionID)
	if !opts.Failover && !opts.ReturnHome {
		pruneTrailingSwitchTurns(convPath, sessionID)
	}

	// 2. Record user turn (the raw "#provider" message) so it appears in history.
	source := opts.Source
	if source == "" {
		source = "system"
	}
	if opts.UserText != "" {
		_ = storage.AppendJSONL(
			layout.SessionConversation(sessionID),
			"wick-conv-v1",
			sessionID,
			store.ConversationTurn{
				Timestamp: now,
				Role:      "user",
				Source:    source,
				Text:      opts.UserText,
			},
		)
	}

	// Context carries over between instances of one provider type (shared
	// transcript store, same resume id) but not across types. Say which of
	// the three cases this switch is, so the user knows what the next turn
	// will remember.
	contextNote := switchNote(tag, carry, sameScope, targetHasHistory)

	switch {
	case opts.Failover:
		recordFailoverTurn(layout, sessionID, source, res, tag, contextNote, now, opts)
		return res, nil
	case opts.ReturnHome:
		recordReturnTurn(layout, sessionID, source, res, tag, contextNote, now, opts)
		return res, nil
	}

	steps := []string{
		"Saved provider to agents.json",
		"Killing running agent process",
		"Next message spawns with provider: " + tag,
		contextNote,
	}

	// 3. Record ONE structured system turn — no separate assistant bubble.
	// kind + extras let the UI render it as a switch card and let future
	// features attach their own data to a system turn without a schema change.
	events := make([]store.TurnEvent, 0, len(steps))
	for _, s := range steps {
		events = append(events, store.TurnEvent{Type: store.KindProviderSwitch, Text: s, At: now})
	}
	_ = storage.AppendJSONL(
		layout.SessionConversation(sessionID),
		"wick-conv-v1",
		sessionID,
		store.ConversationTurn{
			Timestamp: now,
			Role:      "system",
			Source:    source,
			Kind:      store.KindProviderSwitch,
			Text:      SwitchChipText(tag, opts.ModelID),
			Extras: map[string]string{
				"from": fromKey,
				"to":   newKey,
				"note": contextNote,
			},
			Events: events,
		},
	)

	// 4. Notify caller (e.g. push SSE) so the UI appends the system turn live.
	if opts.Notify != nil {
		opts.Notify(tag, steps)
	}

	// 5. Reply is for channels with no system-turn concept (Slack, Telegram):
	// they get a plain text confirmation. The web UI passes no Reply — its
	// system turn already carries everything, so no assistant bubble is made.
	replyText := "Switched to " + tag + ". Your next message will use " + tag + ".\n\n" + contextNote
	if opts.Reply != nil {
		opts.Reply(replyText)
	}

	return res, nil
}

// advanceFailover is a's failover state after one automatic hop away from
// fromKey, within the run for msgKey (RunFor: a new key starts a new run
// whose Since is now): the home is recorded on the first hop ever (the
// provider the session was on), fromKey joins Tried and Hops counts the
// hop. The home never moves here.
func advanceFailover(a session.AgentEntry, fromKey, msgKey string, now time.Time) *session.FailoverState {
	st := a.Failover.RunFor(msgKey, now)
	if st.HomeProvider == "" {
		st.HomeProvider, st.HomeModelID = fromKey, a.ModelID
	}
	seen := false
	for _, k := range st.Tried {
		seen = seen || k == fromKey
	}
	if !seen {
		st.Tried = append(st.Tried, fromKey)
	}
	st.Hops++
	return &st
}

// recordFailoverTurn writes the KindProviderFailover notice of an
// automatic switch and tells the caller's channels, like a manual
// switch's turn but saying why and that the turn carries on.
func recordFailoverTurn(layout config.Layout, sessionID, source string, res SwitchResult, tag, contextNote string, now time.Time, opts SwitchOptions) {
	fromKey, toKey, home := res.From, res.To, res.Home
	stopped := fromKey + " stopped"
	if opts.FailoverReason != "" {
		stopped += ": " + opts.FailoverReason
	}
	steps := []string{
		stopped,
		"Saved provider to agents.json (home stays " + home + ")",
		"Stopping " + fromKey,
		"Continuing this turn on " + tag,
		contextNote,
	}
	events := make([]store.TurnEvent, 0, len(steps))
	for _, s := range steps {
		events = append(events, store.TurnEvent{Type: store.KindProviderFailover, Text: s, At: now})
	}
	extras := map[string]string{
		"from": fromKey,
		"to":   toKey,
		"home": home,
		"note": contextNote,
	}
	if opts.FailoverReason != "" {
		extras["reason"] = opts.FailoverReason
	}
	if res.Continue != "" {
		extras["continue"] = res.Continue
	}
	_ = storage.AppendJSONL(
		layout.SessionConversation(sessionID),
		"wick-conv-v1",
		sessionID,
		store.ConversationTurn{
			Timestamp: now,
			Role:      "system",
			Source:    source,
			Kind:      store.KindProviderFailover,
			Text:      FailoverChipText(tag, opts.ModelID),
			Extras:    extras,
			Events:    events,
		},
	)
	if opts.Notify != nil {
		opts.Notify(tag, steps)
	}
	if opts.Reply != nil {
		opts.Reply(stopped + ". Switched to " + tag + " and continuing this turn.\n\n" + contextNote)
	}
}

// recordReturnTurn writes the KindProviderReturn notice of a move back up
// the home's chain at a turn boundary.
func recordReturnTurn(layout config.Layout, sessionID, source string, res SwitchResult, tag, contextNote string, now time.Time, opts SwitchOptions) {
	head := "Back to home " + tag
	if res.Home != "" && res.To != res.Home {
		head = "Moved back up the failover chain of " + res.Home + " to " + tag
	}
	// The target resumes its own transcript, not the stand-in's: what was
	// said there is out of its sight. Say so rather than let the next
	// answer look forgetful.
	if !res.HistoryCarries && res.From != "" {
		contextNote = "Note: the turns answered on " + res.From + " while it stood in are not part of " + tag + "'s history. " + contextNote
	}
	steps := []string{
		head + " (" + res.From + " was a stand-in)",
		"Saved provider to agents.json",
		"Next message spawns with provider: " + tag,
		contextNote,
	}
	events := make([]store.TurnEvent, 0, len(steps))
	for _, s := range steps {
		events = append(events, store.TurnEvent{Type: store.KindProviderReturn, Text: s, At: now})
	}
	_ = storage.AppendJSONL(
		layout.SessionConversation(sessionID),
		"wick-conv-v1",
		sessionID,
		store.ConversationTurn{
			Timestamp: now,
			Role:      "system",
			Source:    source,
			Kind:      store.KindProviderReturn,
			Text:      ReturnChipText(tag, opts.ModelID),
			Extras: map[string]string{
				"from": res.From,
				"to":   res.To,
				"home": res.Home,
				"note": contextNote,
			},
			Events: events,
		},
	)
	if opts.Notify != nil {
		opts.Notify(tag, steps)
	}
	if opts.Reply != nil {
		opts.Reply(head + ". Your next message will use " + tag + ".\n\n" + contextNote)
	}
}

// ReturnChipText is SwitchChipText for a return home.
func ReturnChipText(tag, modelID string) string {
	s := "Provider back → " + tag
	if modelID != "" {
		s += " · " + modelID
	}
	return s
}

// FailoverChipText is SwitchChipText for an automatic switch.
func FailoverChipText(tag, modelID string) string {
	s := "Provider failed over → " + tag
	if modelID != "" {
		s += " · " + modelID
	}
	return s
}

// SwitchChipText builds the "Provider switched → <tag>" label, appending
// "· <model>" when a model was pinned so the user sees exactly which model
// the session now uses (not just the provider). One helper so every
// call-site (switcher + the HTTP handlers) renders the chip identically.
func SwitchChipText(tag, modelID string) string {
	s := "Provider switched → " + tag
	if modelID != "" {
		s += " · " + modelID
	}
	return s
}

// isSwitchArtifact reports whether a turn was written by a previous Switch
// call — the system trace turn (carries provider_switch events) or its paired
// assistant confirmation ("Switched to …"). User turns are never treated as
// artifacts: a "#tag" the user typed is real intent worth keeping, and this
// avoids ever eating a genuine one-word message.
func isSwitchArtifact(t store.ConversationTurn) bool {
	switch t.Role {
	case "system":
		for _, e := range t.Events {
			if e.Type == "provider_switch" {
				return true
			}
		}
	case "assistant":
		return strings.HasPrefix(t.Text, "Switched to ")
	}
	return false
}

// pruneTrailingSwitchTurns rewrites conversation.jsonl without the run of
// switch-artifact turns at its tail. No-op (and best-effort) when there are
// none or on any read/write error — a failed prune must never block a switch.
func pruneTrailingSwitchTurns(convPath, sessionID string) {
	var turns []store.ConversationTurn
	if err := storage.ReadJSONL(convPath, func(line []byte) bool {
		var t store.ConversationTurn
		if err := json.Unmarshal(line, &t); err == nil {
			turns = append(turns, t)
		}
		return true
	}); err != nil || len(turns) == 0 {
		return
	}

	keep := len(turns)
	for keep > 0 && isSwitchArtifact(turns[keep-1]) {
		keep--
	}
	if keep == len(turns) {
		return // nothing trailing to drop
	}

	if err := storage.TruncateJSONL(convPath, "wick-conv-v1", sessionID); err != nil {
		return
	}
	for _, t := range turns[:keep] {
		_ = storage.AppendJSONL(convPath, "wick-conv-v1", sessionID, t)
	}
}

// switchNote says what the next turn on tag will remember: the same
// conversation (same type, history reachable — shared, or copied over at
// the next spawn), a fresh start with the reason (same type, history
// unreachable), the target's own earlier turns, or nothing from other
// providers.
func switchNote(tag string, carry HistoryCarry, sameScope, targetHasHistory bool) string {
	switch {
	case carry.Reason != "":
		return "Note: starting fresh on " + tag + " — " + carry.Reason + ". It won't see this session's earlier turns."
	case sameScope && targetHasHistory && carry.Copied:
		return "Note: continuing the same conversation — this session's history is copied into " + tag + " before its next turn."
	case sameScope && targetHasHistory:
		return "Note: continuing the same conversation — " + tag + " resumes this session's transcript."
	case targetHasHistory:
		return "Note: resuming " + tag + "'s own earlier turns; it still won't see turns from other providers."
	}
	return "Note: " + tag + " won't see earlier turns from other providers in this session — each provider keeps its own context."
}

// instanceFor finds the instance a "type/name" key names (zero if none).
func instanceFor(instances []Instance, key string) Instance {
	for _, ins := range instances {
		if string(ins.Type)+"/"+ins.Name == key {
			return ins
		}
	}
	typ, name, _ := strings.Cut(key, "/")
	return Instance{Type: Type(typ), Name: name}
}

// HistoryCarry is a type's answer to "can a conversation another instance
// of mine wrote be resumed on this one".
type HistoryCarry struct {
	// Reason, when set, is why it cannot (the switch then starts fresh).
	Reason string
	// Copied: it can, but only once the next spawn copies the history
	// over (the note says so).
	Copied bool
}

// HistoryCarrier answers HistoryCarry for one provider type. Types whose
// instances keep separate stores (omp per profile, opencode per data
// folder) register one from their own package (RegisterHistoryCarrier in
// init); a type without one shares a single store, so its history always
// carries (claude, codex).
type HistoryCarrier func(from, to Instance, resumeID string) HistoryCarry

var (
	historyCarriersMu sync.RWMutex
	historyCarriers   = map[Type]HistoryCarrier{}
)

// RegisterHistoryCarrier installs t's carrier; nil removes it (tests).
func RegisterHistoryCarrier(t Type, c HistoryCarrier) {
	historyCarriersMu.Lock()
	defer historyCarriersMu.Unlock()
	if c == nil {
		delete(historyCarriers, t)
		return
	}
	historyCarriers[t] = c
}

// carriesOver is the registered carrier's answer for a same-type switch
// from → to; the same instance, or a type with no carrier, always carries.
func carriesOver(from, to Instance, resumeID string) HistoryCarry {
	if from.Name == to.Name {
		return HistoryCarry{}
	}
	historyCarriersMu.RLock()
	c := historyCarriers[to.Type]
	historyCarriersMu.RUnlock()
	if c == nil {
		return HistoryCarry{}
	}
	return c(from, to, resumeID)
}
