package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/yogasw/wick/internal/codemode"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// stubRunner speaks the runner wire: every "CALL <tool_id>" line of the code
// becomes one call event; done returns the last call's result.
type stubRunner struct {
	mu      sync.Mutex
	results map[int64]chan json.RawMessage
}

func (f *stubRunner) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/_wick/codemode/result":
		var res struct {
			ID     int64           `json:"id"`
			OK     bool            `json:"ok"`
			Result json.RawMessage `json:"result"`
			Error  string          `json:"error"`
		}
		_ = json.NewDecoder(r.Body).Decode(&res)
		if !res.OK {
			res.Result, _ = json.Marshal("ERR " + res.Error)
		}
		f.mu.Lock()
		ch := f.results[res.ID]
		f.mu.Unlock()
		ch <- res.Result
	case "/_wick/codemode/run":
		var in struct{ Code string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		w.WriteHeader(http.StatusOK)
		var last json.RawMessage = []byte("null")
		var id int64
		for _, line := range strings.Split(in.Code, "\n") {
			tid, ok := strings.CutPrefix(strings.TrimSpace(line), "CALL ")
			if !ok {
				continue
			}
			id++
			ch := make(chan json.RawMessage, 1)
			f.mu.Lock()
			f.results[id] = ch
			f.mu.Unlock()
			b, _ := json.Marshal(map[string]any{"type": "call", "id": id, "tool_id": tid, "params": map[string]any{}})
			_, _ = w.Write(append(b, '\n'))
			w.(http.Flusher).Flush()
			last = <-ch
		}
		b, _ := json.Marshal(map[string]any{"type": "done", "ok": true, "result": last})
		_, _ = w.Write(append(b, '\n'))
	}
}

type stubRewrite struct{ base string }

func (r stubRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	out, _ := http.NewRequestWithContext(req.Context(), req.Method, r.base+req.URL.Path, req.Body)
	out.Header = req.Header
	return http.DefaultTransport.RoundTrip(out)
}

func TestRunCodeAsCallsAsTheGivenUser(t *testing.T) {
	srv := httptest.NewServer(&stubRunner{results: map[int64]chan json.RawMessage{}})
	t.Cleanup(srv.Close)
	prev := CodeModeRunners
	CodeModeRunners = func() []codemode.Runner {
		return []codemode.Runner{{Key: "codemode_stub", Langs: []string{"ts"}, Transport: func() (http.RoundTripper, error) { return stubRewrite{base: srv.URL}, nil }}}
	}
	t.Cleanup(func() { CodeModeRunners = prev })

	exec := func(c *connector.Ctx) (any, error) { return map[string]string{"caller": c.CallerUserID()}, nil }
	svc := newVWSessionSvcWith(t, connector.Module{
		Meta:       connector.Meta{Key: "acme", Name: "Acme", Description: "test", Fixed: true},
		Operations: []connector.Category{connector.Cat("Main", "", connector.Op("list_rows", "List", "list rows", struct{}{}, exec, wickdocs.Docs{}))},
	})
	rows, err := svc.ListByKey(context.Background(), "acme")
	if err != nil || len(rows) == 0 {
		t.Fatalf("no acme row: %v", err)
	}
	layout, sid := newTestLayout(t)
	user := &entity.User{ID: "u-runas", Role: entity.RoleAdmin, Approved: true}
	tool := "conn:" + rows[0].ID + "/list_rows"

	res, err := RunCodeAs(context.Background(), svc, layout, codemode.Request{Code: "CALL " + tool}, sid, user, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || !strings.Contains(string(res.Result), `"caller":"u-runas"`) || len(res.Calls) != 1 {
		t.Fatalf("result: %+v %s", res, res.Result)
	}

	// A tool_id the caller cannot use stops the run before any call.
	res, err = RunCodeAs(context.Background(), svc, layout, codemode.Request{Code: "CALL conn:missing-1/list_rows"}, sid, user, nil)
	if err != nil || res.OK || len(res.Calls) != 0 || !strings.Contains(res.Error, "conn:missing-1/list_rows") {
		t.Fatalf("pre-flight: err=%v res=%+v", err, res)
	}

	if _, err := RunCodeAs(context.Background(), svc, layout, codemode.Request{Code: "CALL " + tool}, sid, nil, nil); err == nil {
		t.Fatal("no user must be refused")
	}
}
