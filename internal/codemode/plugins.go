package codemode

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/services/plugin"
)

// PluginRunners lists the codemode_* service plugins of host. A runner's
// languages come from its /status, fetched once per installed version (the
// first run wakes it anyway).
func PluginRunners(host func() *plugin.Host) Runners {
	var (
		mu    sync.Mutex
		langs = map[string][]string{} // key@version → langs
	)
	return func() []Runner {
		h := host()
		if h == nil {
			return nil
		}
		var out []Runner
		for _, s := range h.List() {
			if !strings.HasPrefix(s.Key, RunnerPrefix) {
				continue
			}
			s := s
			id := s.Key + "@" + s.Version
			mu.Lock()
			ls, ok := langs[id]
			mu.Unlock()
			if !ok {
				ls = fetchLangs(s.Sup.Transport)
				if len(ls) > 0 {
					mu.Lock()
					langs[id] = ls
					mu.Unlock()
				}
			}
			out = append(out, Runner{Key: s.Key, Langs: ls, Transport: s.Sup.Transport})
		}
		return out
	}
}

func fetchLangs(transport func() (http.RoundTripper, error)) []string {
	rt, err := transport()
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://plugin/_wick/codemode/status", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var st struct {
		Langs []string `json:"langs"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&st) != nil {
		return nil
	}
	return st.Langs
}
