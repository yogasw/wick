package provider

import (
	"encoding/json"
	"strconv"
	"strings"

	pkgentity "github.com/yogasw/wick/pkg/entity"
)

// InstanceConfig is the wick-tag-annotated view of provider instance settings.
// Used to drive ConfigsTable rendering and per-key saves.
type InstanceConfig struct {
	Binary        string `wick:"key=binary;desc=Binary path override. Empty = auto-resolve from PATH."`
	ExtraArgs     string `wick:"key=extra_args;kvlist;desc=Extra CLI args passed to the binary on every spawn."`
	Env           string `wick:"key=env;kvlist=key|value;desc=Environment variables injected on every spawn."`
	MaxConcurrent int    `wick:"key=max_concurrent;desc=Max parallel spawns (0 = unlimited, follows global cap)."`
	// SendMode picks how a user message reaches the CLI. "default" follows
	// the provider type (claude → append, codex → queue). append = one
	// persistent process, CLI queues input itself. queue = one-shot per
	// turn, mid-turn messages wait then run in order (none lost). spawn =
	// one-shot, every message its own parallel process (no queue, contexts
	// independent — only safe where turns don't need shared history).
	SendMode string `wick:"key=send_mode;dropdown=default|append|queue|spawn;desc=How a message reaches the CLI.\ndefault — follow the provider type (claude=append, codex=queue).\nappend — one persistent process; the CLI queues input itself (claude).\nqueue — one process per turn; messages sent while busy wait, then run in order. Context continues (resume). Nothing is dropped.\nspawn — one process per message, all in parallel. No queue; each runs in its own session, so contexts do NOT share history."`
}

// SeedInstanceConfig returns populated entity.Config rows for an Instance.
func SeedInstanceConfig(ins Instance) []pkgentity.Config {
	sendMode := ins.SendMode
	if sendMode == "" {
		sendMode = "default" // empty = follow the provider type's default
	}
	rows := pkgentity.StructToConfigs(InstanceConfig{
		Binary:        ins.Binary,
		ExtraArgs:     argsToKVList(ins.ExtraArgs),
		Env:           envToKVList(ins.Env),
		MaxConcurrent: ins.MaxConcurrent,
		SendMode:      sendMode,
	})
	return rows
}

// ApplyInstanceConfigKey merges one saved key=value into an Instance.
func ApplyInstanceConfigKey(ins *Instance, key, value string) {
	switch key {
	case "binary":
		ins.Binary = strings.TrimSpace(value)
	case "extra_args":
		ins.ExtraArgs = kvListToArgs(value)
	case "env":
		ins.Env = kvListToEnv(value)
	case "max_concurrent":
		n, _ := strconv.Atoi(strings.TrimSpace(value))
		ins.MaxConcurrent = n
	case "send_mode":
		// "default" (or empty) means follow the provider-type default —
		// store as empty so ParseSendMode falls through to the type rule.
		v := strings.TrimSpace(strings.ToLower(value))
		if v == "default" {
			v = ""
		}
		ins.SendMode = v
	case "disabled":
		ins.Disabled = value == "true" || value == "on"
	}
}

// argsToKVList encodes []string → JSON [{"value":"arg"}, ...]
func argsToKVList(args []string) string {
	if len(args) == 0 {
		return ""
	}
	b := strings.Builder{}
	b.WriteString("[")
	for i, a := range args {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"value":`)
		b.WriteString(jsonString(a))
		b.WriteString("}")
	}
	b.WriteString("]")
	return b.String()
}

// kvListToArgs decodes JSON [{"value":"arg"}, ...] → []string
func kvListToArgs(s string) []string {
	var rows []map[string]string
	if err := json.Unmarshal([]byte(s), &rows); err != nil || len(rows) == 0 {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if v := r["value"]; v != "" {
			out = append(out, v)
		}
	}
	return out
}

// envToKVList encodes []string KEY=VALUE → JSON [{"key":"K","value":"V"}, ...]
func envToKVList(env []string) string {
	if len(env) == 0 {
		return ""
	}
	b := strings.Builder{}
	b.WriteString("[")
	for i, e := range env {
		k, v := e, ""
		if idx := strings.IndexByte(e, '='); idx >= 0 {
			k, v = e[:idx], e[idx+1:]
		}
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"key":`)
		b.WriteString(jsonString(k))
		b.WriteString(`,"value":`)
		b.WriteString(jsonString(v))
		b.WriteString("}")
	}
	b.WriteString("]")
	return b.String()
}

// kvListToEnv decodes JSON [{"key":"K","value":"V"}, ...] → []string KEY=VALUE
func kvListToEnv(s string) []string {
	var rows []map[string]string
	if err := json.Unmarshal([]byte(s), &rows); err != nil || len(rows) == 0 {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if k := r["key"]; k != "" {
			out = append(out, k+"="+r["value"])
		}
	}
	return out
}
