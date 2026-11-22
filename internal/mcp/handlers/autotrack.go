package handlers

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/schedule"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

// noTrackParam is the per-call opt-out an agent may add to a tracked op's
// params. It is stripped before the op runs, so connectors never see it.
const noTrackParam = "no_track"

var (
	autoTrackStore *schedule.Store
	// AutoTrackTodoNotify tells a session's open panels its checklist
	// changed. Wired by the server; nil = panels catch up on next read.
	AutoTrackTodoNotify func(sessionID string)
)

// SetAutoTrackStore wires the schedule store the automatic job follow-up
// writes its watch into. nil (stdio, tests) disables the follow-up.
func SetAutoTrackStore(s *schedule.Store) { autoTrackStore = s }

// popNoTrack removes the opt-out flag from params and reports it.
func popNoTrack(params map[string]any) (map[string]any, bool) {
	v, ok := params[noTrackParam]
	if !ok {
		return params, false
	}
	out := make(map[string]any, len(params))
	for k, x := range params {
		if k != noTrackParam {
			out[k] = x
		}
	}
	switch t := v.(type) {
	case bool:
		return out, t
	case string:
		return out, strings.EqualFold(strings.TrimSpace(t), "true")
	}
	return out, false
}

// autoTrack follows a job an op just started, when that op carries a Track
// spec: a watch polls the status op into a checklist item of the calling
// session and wakes it once when the job ends. It never fails the call that
// already succeeded — any problem comes back as a note on the result.
// Returns "" when the op is not tracked.
func autoTrack(ctx context.Context, svc *connectors.Service, layout agentconfig.Layout, toolID string, params map[string]any, result, sessionID string, user *entity.User) string {
	if autoTrackStore == nil || svc == nil || user == nil {
		return ""
	}
	connectorID, opKey, accountID, err := ParseToolIDFull(toolID)
	if err != nil || strings.HasPrefix(connectorID, "sw_") {
		return ""
	}
	spec, op := svc.TrackFor(ctx, connectorID, opKey)
	if spec == nil {
		return ""
	}
	if sessionID == "" {
		return "auto-track skipped: no session to report into (call from an agent session to get a checklist item and a wake-up when the job ends)"
	}
	plan, err := planTrack(*spec, op, connectorID, accountID, params, result)
	if err != nil {
		return "auto-track skipped: " + err.Error() + " — follow the job yourself (wick_schedule_message type=watch)"
	}

	every := spec.EveryDuration()
	now := time.Now()
	enc, until, err := autoTrackStore.PrepareWatch(ctx, schedule.WatchCreate{
		Steps:          plan.steps,
		Spec:           schedule.Spec{Recurring: true, IntervalMs: every.Milliseconds(), FirstRunAt: now.Add(every)},
		Timeout:        spec.TimeoutString(),
		OwnerUserID:    user.ID,
		CreatorUserID:  user.ID,
		CreatorIsAdmin: user.IsAdmin(),
		SessionIDs:     []string{sessionID},
	}, now)
	if err == nil {
		enc, err = schedule.AttachWatchTodo(enc, schedule.WatchTodo{Item: plan.item, Title: plan.title})
	}
	if err != nil {
		return "auto-track skipped: " + err.Error()
	}
	row, err := autoTrackStore.Create(ctx, &entity.ScheduledMessage{
		SessionID:       sessionID,
		OwnerUserID:     user.ID,
		CreatedBy:       entity.ScheduledByAI,
		SourceSessionID: sessionID,
		AgentName:       "auto-track",
		Message: fmt.Sprintf("Job started by %s has ended (followed automatically by wick). "+
			"Read the result below and continue the task.", op.Key),
		RunAt:      now.Add(every),
		Type:       entity.ScheduledTypeWatch,
		Kind:       entity.ScheduledKindRecurring,
		IntervalMs: every.Milliseconds(),
		Steps:      enc,
		EndsAt:     until,
	})
	if err != nil {
		return "auto-track skipped: create watch: " + err.Error()
	}

	if _, err := session.PatchTodoItem(layout, sessionID, session.TodoPatch{
		Select: plan.item,
		Title:  plan.title,
		Status: "in_progress",
		Detail: &session.TodoDetail{Format: "text", Body: fmt.Sprintf("started · polling %s every %s\nwatch %s", spec.StatusOp, every, row.ID)},
	}); err != nil {
		log.Warn().Err(err).Str("session", sessionID).Msg("auto-track: checklist item not written")
	} else if AutoTrackTodoNotify != nil {
		AutoTrackTodoNotify(sessionID)
	}
	return fmt.Sprintf("auto-tracked: wick polls %s every %s (watch %s, time limit %s); checklist item %q moves on every poll and this session gets ONE message when the job ends. Do not poll it yourself.",
		spec.StatusOp, every, row.ID, spec.TimeoutString(), plan.title)
}

type trackPlan struct {
	steps []schedule.Step
	item  string
	title string
}

// planTrack turns a Track spec and one finished start call into watch steps
// plus the checklist item. Pure: no store, no clock.
func planTrack(spec connector.Track, op connector.Operation, connectorID, accountID string, in map[string]any, result string) (trackPlan, error) {
	var out any
	if strings.TrimSpace(result) != "" {
		_ = json.Unmarshal([]byte(result), &out)
	}
	resolve := func(src string) (any, error) {
		switch {
		case strings.HasPrefix(src, "out."):
			if out == nil {
				return nil, fmt.Errorf("%s: the start result is not JSON", src)
			}
			v, ok := schedule.LookupPath(out, strings.TrimPrefix(src, "out."))
			if !ok || v == nil {
				return nil, fmt.Errorf("%s not found in the start result", src)
			}
			return v, nil
		case strings.HasPrefix(src, "in."):
			v, ok := in[strings.TrimPrefix(src, "in.")]
			if !ok || v == nil {
				return nil, fmt.Errorf("%s was not passed to %s", src, op.Key)
			}
			return v, nil
		}
		return src, nil
	}

	keys := make([]string, 0, len(spec.Params))
	for k := range spec.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	params := make(map[string]any, len(keys))
	var firstVal string
	for _, k := range keys {
		src := strings.TrimSpace(spec.Params[k])
		optional := strings.HasSuffix(src, "?") && (strings.HasPrefix(src, "in.") || strings.HasPrefix(src, "out."))
		v, err := resolve(strings.TrimSuffix(src, "?"))
		if err != nil {
			if optional {
				continue
			}
			return trackPlan{}, fmt.Errorf("status param %s: %w", k, err)
		}
		params[k] = v
		if firstVal == "" {
			firstVal = fmt.Sprint(v)
		}
	}

	toolID := "conn:" + connectorID + "/" + spec.StatusOp
	if accountID != "" {
		toolID += "@" + accountID
	}
	check := schedule.Step{
		Name:    "job state",
		Kind:    schedule.StepKindCheck,
		Rules:   []schedule.Rule{{Path: spec.StatePath, Op: schedule.OpIn, Value: anyList(spec.Done)}},
		Extract: map[string]string{schedule.WatchTodoStateKey: spec.StatePath},
	}
	if len(spec.Failed) > 0 {
		check.FailRules = []schedule.Rule{{Path: spec.StatePath, Op: schedule.OpIn, Value: anyList(spec.Failed)}}
	}
	if spec.DetailPath != "" {
		check.Extract[schedule.WatchTodoDetailKey] = spec.DetailPath
	}
	steps := []schedule.Step{
		{Name: spec.StatusOp, Kind: schedule.StepKindConnector, ToolID: toolID, Params: params},
		check,
	}

	title := strings.TrimSpace(spec.Label)
	if title != "" {
		title = fillLabel(title, func(src string) string {
			if v, err := resolve(src); err == nil {
				return fmt.Sprint(v)
			}
			return "?"
		})
	} else {
		name := op.Name
		if name == "" {
			name = op.Key
		}
		title = name
		if firstVal != "" {
			title += " " + firstVal
		}
	}
	sum := sha1.Sum([]byte(toolID + "|" + mustJSON(params)))
	return trackPlan{steps: steps, item: "track-" + hex.EncodeToString(sum[:])[:10], title: title}, nil
}

var labelVar = regexp.MustCompile(`\{((?:in|out)\.[^{}]+)\}`)

func fillLabel(s string, val func(string) string) string {
	return labelVar.ReplaceAllStringFunc(s, func(m string) string {
		return val(m[1 : len(m)-1])
	})
}

func anyList(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
