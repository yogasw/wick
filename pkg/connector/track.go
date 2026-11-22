package connector

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Track marks an operation that STARTS long-running work somewhere else —
// a pipeline, an image build, a deploy — and says how to follow it to the
// end. When an agent calls such an op through wick_execute and it succeeds,
// wick follows the job by itself: a checklist item in the calling session
// that moves on every poll, and one message into that session when the job
// ends. An agent that forgets to check back still hears how it went.
//
// The follow-up is a watch schedule (no LLM per poll): StatusOp on the SAME
// connector instance is called every Every with Params, StatePath is read
// from its JSON, a value in Done ends it as success, one in Failed as
// failure, anything else keeps waiting until Timeout.
//
// Built-in connectors set Operation.Track in code; any connector (an MCP
// server, a custom definition) can get one per instance through the op's
// curated prefs (op_pref_set track=…), which win over the code default.
type Track struct {
	// StatusOp is the op key that reads the job's state (get_pipeline,
	// get_build, get_image_build_tool, …).
	StatusOp string `json:"status_op"`
	// Params maps each StatusOp param to where its value comes from:
	//   "out.<path>" — the start op's JSON result (dotted; a number segment indexes an array: items.0.id)
	//   "in.<name>"  — the param the start op was called with
	//   anything else — a literal value
	// A trailing "?" on an out./in. source makes it optional: the param is
	// left out when the value is missing (in.workspace?).
	Params map[string]string `json:"params,omitempty"`
	// StatePath is the dotted path of the state in StatusOp's result.
	StatePath string `json:"state_path"`
	// Done / Failed are the terminal state values, matched exactly (list
	// every spelling the API uses).
	Done   []string `json:"done"`
	Failed []string `json:"failed,omitempty"`
	// DetailPath is an optional path shown under the checklist item on every
	// poll (a log tail, a progress string).
	DetailPath string `json:"detail_path,omitempty"`
	// Every is the poll cadence (Go duration, ≥10s). Default 20s.
	Every string `json:"every,omitempty"`
	// Timeout ends the follow-up when the job never reaches a terminal
	// state. Default 2h.
	Timeout string `json:"timeout,omitempty"`
	// Label names the checklist item. "{in.<name>}" / "{out.<path>}" are
	// filled in; empty = the op's name plus the first resolved param.
	Label string `json:"label,omitempty"`
}

// Track defaults.
const (
	TrackDefaultEvery   = 20 * time.Second
	TrackDefaultTimeout = 2 * time.Hour
	trackMinEvery       = 10 * time.Second
)

// EveryDuration is the poll cadence, defaulted and floored.
func (t Track) EveryDuration() time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(t.Every))
	if err != nil || d <= 0 {
		return TrackDefaultEvery
	}
	if d < trackMinEvery {
		return trackMinEvery
	}
	return d
}

// TimeoutString is the watch time limit in the form the schedule store
// takes ("2h0m0s"), defaulted.
func (t Track) TimeoutString() string {
	d, err := time.ParseDuration(strings.TrimSpace(t.Timeout))
	if err != nil || d <= 0 {
		d = TrackDefaultTimeout
	}
	return d.String()
}

// Validate reports what is missing or malformed, worded for whoever has to
// fix it (usually an AI that just called op_pref_set).
func (t Track) Validate() error {
	var errs []string
	if strings.TrimSpace(t.StatusOp) == "" {
		errs = append(errs, `status_op is required (the op that reads the job state, e.g. "get_pipeline")`)
	}
	if strings.TrimSpace(t.StatePath) == "" {
		errs = append(errs, `state_path is required (dotted path of the state in status_op's result, e.g. "state.result.name")`)
	}
	if len(nonEmpty(t.Done)) == 0 {
		errs = append(errs, `done needs at least one terminal success value, e.g. ["SUCCESSFUL"]`)
	}
	for k, v := range t.Params {
		if strings.TrimSpace(k) == "" {
			errs = append(errs, "params: empty param name")
		}
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Sprintf("params.%s: empty source; use out.<path>, in.<name> or a literal", k))
		}
	}
	if s := strings.TrimSpace(t.Every); s != "" {
		if d, err := time.ParseDuration(s); err != nil || d <= 0 {
			errs = append(errs, fmt.Sprintf("every: %q is not a duration (e.g. 20s, 1m)", s))
		}
	}
	if s := strings.TrimSpace(t.Timeout); s != "" {
		if d, err := time.ParseDuration(s); err != nil || d <= 0 {
			errs = append(errs, fmt.Sprintf("timeout: %q is not a duration (e.g. 30m, 2h)", s))
		}
	}
	if len(errs) > 0 {
		return errors.New("track: " + strings.Join(errs, "; "))
	}
	return nil
}

// ParseTrack decodes a stored/passed track spec and validates it. Empty
// input is (nil, nil): no tracking.
func ParseTrack(raw string) (*Track, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var t Track
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return nil, fmt.Errorf(`track: not valid JSON (%v); example: {"status_op":"get_pipeline","params":{"uuid":"out.uuid"},"state_path":"state.result.name","done":["SUCCESSFUL"],"failed":["FAILED","ERROR","STOPPED"]}`, err)
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	return &t, nil
}

func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}
