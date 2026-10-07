package opencode

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
)

// A `opencode run` turn has no serve to ask GET /provider for the model's
// limit.context, so the meter had no scale there. opencode itself keeps
// the models.dev catalog it loads models from in
// $XDG_CACHE_HOME/opencode/models.json (Global.Path.cache, shared by every
// instance: wick moves only the data and config dirs); the spawn reads the
// model's limit there and puts wick's context line ahead of the stream —
// the line the serve path emits. A model the file does not list (a custom
// provider from config) gets no line, as before.

// modelsCache is one decoded models.json, kept while its mtime holds.
var modelsCache struct {
	sync.Mutex
	path    string
	mod     time.Time
	windows map[string]int // "provider/model" → limit.context
}

// modelsCachePath is opencode's models.dev cache file.
func modelsCachePath(env []string) string {
	dir := envLookup(env, "XDG_CACHE_HOME")
	if dir == "" {
		dir = os.Getenv("XDG_CACHE_HOME")
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".cache")
	}
	return filepath.Join(dir, "opencode", "models.json")
}

// envLookup is key's value in env (the last one wins, as exec does).
func envLookup(env []string, key string) string {
	v := ""
	for _, kv := range env {
		if k, val, ok := strings.Cut(kv, "="); ok && k == key {
			v = val
		}
	}
	return v
}

// cachedWindow is model's ("provider/model") limit.context from the
// models.dev cache at path, 0 when unknown.
func cachedWindow(path, model string) int {
	if path == "" || !strings.Contains(model, "/") {
		return 0
	}
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	modelsCache.Lock()
	defer modelsCache.Unlock()
	if modelsCache.path != path || !modelsCache.mod.Equal(st.ModTime()) {
		b, err := os.ReadFile(path)
		if err != nil {
			return 0
		}
		var doc map[string]struct {
			Models map[string]struct {
				Limit struct {
					Context int `json:"context"`
				} `json:"limit"`
			} `json:"models"`
		}
		if json.Unmarshal(b, &doc) != nil {
			return 0
		}
		w := map[string]int{}
		for prov, p := range doc {
			for id, m := range p.Models {
				if m.Limit.Context > 0 {
					w[prov+"/"+id] = m.Limit.Context
				}
			}
		}
		modelsCache.path, modelsCache.mod, modelsCache.windows = path, st.ModTime(), w
	}
	return modelsCache.windows[model]
}

// withContextLine puts wick's context line for window ahead of r.
func withContextLine(r io.ReadCloser, window int) io.ReadCloser {
	if window <= 0 {
		return r
	}
	return struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(event.ContextLine(window)), r), r}
}
