// Package runtime owns the per-AI-CLI runtime layer for agents:
//
//   - the supported-type list (claude / codex / gemini)
//   - per-instance config (named profile, binary override, extra args,
//     extra env) read from userconfig
//   - PATH detect + `--version` probes
//   - spawn-log files used by the Backends UI page
//
// "runtime" = how a single AI CLI invocation is configured + observed.
// A user can have multiple instances of the same type ("claude/work"
// + "claude/personal") so this package returns lists, not singletons.
//
// Why not in `agent/`: `agent/` drives one subprocess (stdin/stdout
// loop, idle timer). This package answers "which binary, with what
// flags + env, and is it healthy?" — orthogonal concerns kept apart
// so `agent/` stays CLI-agnostic.
package provider

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/userconfig"
)

// Type is the AI CLI kind. Adding a new type = add a constant here +
// teach Spawn how to wire its argv + auto-seed on bootstrap.
type Type string

const (
	TypeClaude Type = "claude"
	TypeCodex  Type = "codex"
	TypeGemini Type = "gemini"
)

// SupportedTypes returns all CLI types the agents module knows how to
// spawn. Order is the UI display order.
func SupportedTypes() []Type {
	return []Type{TypeClaude, TypeCodex, TypeGemini}
}

// Instance is the in-memory view of one configured runtime instance —
// merged from userconfig + supported-type defaults. The Backends UI
// page renders one card per Instance.
type Instance struct {
	Type      Type
	Name      string
	Binary    string   // override path; empty = use Type as PATH name
	ExtraArgs []string
	Env       []string
	Disabled  bool
}

// Bin returns the binary the spawner should execute: override path
// when set, else the canonical type name (resolved later via PATH).
func (i Instance) Bin() string {
	if i.Binary != "" {
		return i.Binary
	}
	return string(i.Type)
}

// Status is the live health of one instance, as shown in the UI.
type Status struct {
	Instance   Instance
	ResolvedAt time.Time
	Path       string // result of LookPath / override
	PathFound  bool
	Version    string // first line of `<bin> --version`
	VersionErr string // error message when version probe failed
}

// ── Config helpers ────────────────────────────────────────────────────

// AppName is the userconfig project name the agents module reads/writes
// under. Wired by the bootstrap: a server with APP_NAME=foo stores its
// agents config in ~/.foo/config.json.
var AppName = ""

// Load returns every configured instance across all supported types,
// auto-seeding the per-type default entry when its list is empty so
// the UI always has at least one row per supported runtime.
func Load() ([]Instance, error) {
	cfg, err := userconfig.Load(AppName)
	if err != nil {
		return nil, err
	}
	return mergeWithDefaults(cfg.Providers), nil
}

// Find resolves an instance by {type, name}. Empty name resolves to
// the per-type default whose Name equals the type itself.
func Find(t Type, name string) (Instance, error) {
	if name == "" {
		name = string(t)
	}
	all, err := Load()
	if err != nil {
		return Instance{}, err
	}
	for _, ins := range all {
		if ins.Type == t && ins.Name == name {
			return ins, nil
		}
	}
	return Instance{}, fmt.Errorf("runtime %s/%s not configured", t, name)
}

// Save persists a new or updated instance. Empty Name is rejected.
// Replaces any existing entry with the same {Type, Name}.
func Save(ins Instance) error {
	if ins.Name == "" {
		return errors.New("instance name required")
	}
	if !isSupported(ins.Type) {
		return fmt.Errorf("unsupported runtime type %q", ins.Type)
	}
	cfg, err := userconfig.Load(AppName)
	if err != nil {
		return err
	}
	list := pickList(&cfg.Providers, ins.Type)
	updated := false
	for i := range *list {
		if (*list)[i].Name == ins.Name {
			(*list)[i] = toUserInstance(ins)
			updated = true
			break
		}
	}
	if !updated {
		*list = append(*list, toUserInstance(ins))
	}
	return userconfig.Save(AppName, cfg)
}

// Delete removes an instance. Removing the last instance for a type
// is allowed — Load will auto-seed the default again on the next read.
func Delete(t Type, name string) error {
	if name == "" {
		return errors.New("instance name required")
	}
	cfg, err := userconfig.Load(AppName)
	if err != nil {
		return err
	}
	list := pickList(&cfg.Providers, t)
	for i := range *list {
		if (*list)[i].Name == name {
			*list = append((*list)[:i], (*list)[i+1:]...)
			return userconfig.Save(AppName, cfg)
		}
	}
	return nil
}

// ── Detect / version probing ──────────────────────────────────────────

// Probe resolves the binary path and runs `--version` for one
// instance. Disabled instances skip the spawn but still report the
// resolved path so the UI can show what wick would have run.
//
// ctx bounds the version probe; HTTP handlers should pass a 3s timeout.
func Probe(ctx context.Context, ins Instance) Status {
	st := Status{Instance: ins, ResolvedAt: time.Now()}
	if ins.Binary != "" {
		st.Path = ins.Binary
		if _, err := exec.LookPath(ins.Binary); err == nil {
			st.PathFound = true
		}
	} else {
		path, err := exec.LookPath(string(ins.Type))
		if err == nil {
			st.Path = path
			st.PathFound = true
		}
	}
	if !st.PathFound {
		return st
	}
	if ins.Disabled {
		return st
	}
	cmd := exec.CommandContext(ctx, st.Path, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		st.VersionErr = err.Error()
		return st
	}
	st.Version = firstLine(strings.TrimSpace(string(out)))
	return st
}

// ProbeAll runs Probe on every configured instance in parallel,
// honouring ctx as the total timeout (per-probe is bounded by ctx).
func ProbeAll(ctx context.Context) ([]Status, error) {
	all, err := Load()
	if err != nil {
		return nil, err
	}
	out := make([]Status, len(all))
	var wg sync.WaitGroup
	for i := range all {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = Probe(ctx, all[i])
		}()
	}
	wg.Wait()
	return out, nil
}

// ── internal ──────────────────────────────────────────────────────────

func mergeWithDefaults(c userconfig.ProvidersConfig) []Instance {
	out := make([]Instance, 0, 3)
	for _, t := range SupportedTypes() {
		list := readList(c, t)
		if len(list) == 0 {
			out = append(out, Instance{Type: t, Name: string(t)})
			continue
		}
		for _, raw := range list {
			out = append(out, Instance{
				Type:      t,
				Name:      raw.Name,
				Binary:    raw.BinaryPath,
				ExtraArgs: raw.ExtraArgs,
				Env:       raw.Env,
				Disabled:  raw.Disabled,
			})
		}
	}
	return out
}

func readList(c userconfig.ProvidersConfig, t Type) []userconfig.ProviderInstance {
	switch t {
	case TypeClaude:
		return c.Claude
	case TypeCodex:
		return c.Codex
	case TypeGemini:
		return c.Gemini
	}
	return nil
}

func pickList(c *userconfig.ProvidersConfig, t Type) *[]userconfig.ProviderInstance {
	switch t {
	case TypeClaude:
		return &c.Claude
	case TypeCodex:
		return &c.Codex
	case TypeGemini:
		return &c.Gemini
	}
	return nil
}

func toUserInstance(ins Instance) userconfig.ProviderInstance {
	return userconfig.ProviderInstance{
		Name:       ins.Name,
		BinaryPath: ins.Binary,
		Disabled:   ins.Disabled,
		ExtraArgs:  ins.ExtraArgs,
		Env:        ins.Env,
	}
}

func isSupported(t Type) bool {
	for _, s := range SupportedTypes() {
		if s == t {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
