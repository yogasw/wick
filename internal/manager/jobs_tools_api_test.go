package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/pkg/job"
	"github.com/yogasw/wick/pkg/tool"
)

func newJobsAPIDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: postgres.NewLogLevel("silent"),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	postgres.Migrate(db)
	return db
}

// newJobsHandler wires a Handler backed by a real manager Service + configs
// service on an in-memory DB. The job "report" is bootstrapped with a runner
// that completes instantly, and a config schema is declared via EnsureOwned
// so the detail projection + config setter have something to act on. A tool
// "echo" is registered for the tool endpoints. Touches no SPA embed.
func newJobsHandler(t *testing.T) *Handler {
	t.Helper()
	db := newJobsAPIDB(t)
	cfgsSvc := configs.NewService(db)
	if err := cfgsSvc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("configs bootstrap: %v", err)
	}
	connSvc := connectors.NewServiceFromDB(db)
	connSvc.SetConfigs(cfgsSvc)
	if err := connSvc.Bootstrap(context.Background(), nil); err != nil {
		t.Fatalf("connectors bootstrap: %v", err)
	}

	svc := NewServiceFromDB(db)
	mod := job.Module{
		Meta: job.Meta{
			Key:         "report",
			Name:        "Daily Report",
			Description: "Sends a daily report.",
			Icon:        "📊",
			DefaultCron: "0 9 * * *",
		},
		Run: func(context.Context) (string, error) { return "# done", nil },
	}
	if err := svc.Bootstrap(context.Background(), []job.Module{mod}); err != nil {
		t.Fatalf("manager bootstrap: %v", err)
	}

	if err := cfgsSvc.EnsureOwned(context.Background(), "report",
		entity.Config{Key: "endpoint", Type: "url", Required: true, Description: "report sink"},
		entity.Config{Key: "api_key", Type: "text", IsSecret: true},
	); err != nil {
		t.Fatalf("declare job configs: %v", err)
	}
	if err := cfgsSvc.EnsureOwned(context.Background(), "echo",
		entity.Config{Key: "prefix", Type: "text"},
	); err != nil {
		t.Fatalf("declare tool configs: %v", err)
	}

	tools := []tool.Tool{{Key: "echo", Name: "Echo", Description: "Echoes input.", Icon: "🔁"}}
	return &Handler{svc: svc, configs: cfgsSvc, connectors: connSvc, tools: tools}
}

func adminUser() *entity.User { return &entity.User{ID: "u-admin", Role: entity.RoleAdmin} }

func jobReq(method, path string, body any) *http.Request {
	var r *http.Request
	if body != nil {
		raw, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(raw))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	return r.WithContext(login.WithUser(r.Context(), adminUser(), nil))
}

func TestAPIJobDetail(t *testing.T) {
	h := newJobsHandler(t)

	cases := []struct {
		name       string
		key        string
		wantStatus int
		wantKey    string
		wantFields int
	}{
		{name: "known job projects settings + visible configs", key: "report", wantStatus: http.StatusOK, wantKey: "report", wantFields: 2},
		{name: "unknown job 404s", key: "nope", wantStatus: http.StatusNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := jobReq(http.MethodGet, "/manager/api/jobs/"+tc.key, nil)
			req.SetPathValue("key", tc.key)
			rec := httptest.NewRecorder()
			h.apiJobDetail(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			var got jobDetailJSON
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Key != tc.wantKey {
				t.Errorf("key = %q, want %q", got.Key, tc.wantKey)
			}
			if got.Schedule != "0 9 * * *" {
				t.Errorf("schedule = %q, want default cron", got.Schedule)
			}
			if got.MaxTimeoutMin != 30 {
				t.Errorf("max_timeout_min = %d, want defaulted 30", got.MaxTimeoutMin)
			}
			if len(got.Fields) != tc.wantFields {
				t.Fatalf("fields = %d, want %d", len(got.Fields), tc.wantFields)
			}
			for _, f := range got.Fields {
				if f.IsSecret && f.Value != "" {
					t.Errorf("secret field %q leaked value %q", f.Key, f.Value)
				}
			}
		})
	}
}

func TestAPIUpdateJobSettings(t *testing.T) {
	h := newJobsHandler(t)

	req := jobReq(http.MethodPost, "/manager/api/jobs/report/settings", map[string]any{
		"schedule":        "*/5 * * * *",
		"enabled":         true,
		"max_runs":        7,
		"max_timeout_min": 0,
	})
	req.SetPathValue("key", "report")
	rec := httptest.NewRecorder()
	h.apiUpdateJobSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	j, err := h.svc.GetJob(context.Background(), "report")
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if j.Schedule != "*/5 * * * *" || !j.Enabled || j.MaxRuns != 7 {
		t.Errorf("persisted settings = {%q,%v,%d}, want {*/5,true,7}", j.Schedule, j.Enabled, j.MaxRuns)
	}
	if j.MaxTimeoutMin != 30 {
		t.Errorf("max_timeout_min = %d, want defaulted 30", j.MaxTimeoutMin)
	}
}

func TestAPISetJobConfig(t *testing.T) {
	h := newJobsHandler(t)

	cases := []struct {
		name       string
		configKey  string
		value      string
		wantStatus int
		wantStored string
	}{
		{name: "known key persists", configKey: "endpoint", value: "https://x", wantStatus: http.StatusOK, wantStored: "https://x"},
		{name: "unknown key 400s", configKey: "ghost", value: "v", wantStatus: http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := jobReq(http.MethodPost, "/manager/api/jobs/report/configs/"+tc.configKey, map[string]string{"value": tc.value})
			req.SetPathValue("key", "report")
			req.SetPathValue("configKey", tc.configKey)
			rec := httptest.NewRecorder()
			h.apiSetJobConfig(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == http.StatusOK {
				if got := h.configs.GetOwned("report", tc.configKey); got != tc.wantStored {
					t.Errorf("stored %q = %q, want %q", tc.configKey, got, tc.wantStored)
				}
			}
		})
	}
}

func TestAPIRunJobThenPoll(t *testing.T) {
	h := newJobsHandler(t)

	runReq := jobReq(http.MethodPost, "/manager/api/jobs/report/run", nil)
	runReq.SetPathValue("key", "report")
	runRec := httptest.NewRecorder()
	h.apiRunJob(runRec, runReq)
	if runRec.Code != http.StatusOK {
		t.Fatalf("run status = %d, want 200; body=%s", runRec.Code, runRec.Body.String())
	}
	var started struct {
		Status string `json:"status"`
		RunID  string `json:"run_id"`
	}
	if err := json.Unmarshal(runRec.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	if started.Status != "started" || started.RunID == "" {
		t.Fatalf("run response = %+v, want started + run_id", started)
	}

	// Poll until the background goroutine finalizes the run (it completes
	// instantly here; the deadline guards against a hang).
	deadline := time.Now().Add(5 * time.Second)
	var final map[string]any
	for time.Now().Before(deadline) {
		pollReq := jobReq(http.MethodGet, "/manager/api/jobs/report/runs/"+started.RunID, nil)
		pollReq.SetPathValue("key", "report")
		pollReq.SetPathValue("runID", started.RunID)
		pollRec := httptest.NewRecorder()
		h.apiJobRun(pollRec, pollReq)
		if pollRec.Code != http.StatusOK {
			t.Fatalf("poll status = %d, want 200; body=%s", pollRec.Code, pollRec.Body.String())
		}
		final = map[string]any{}
		if err := json.Unmarshal(pollRec.Body.Bytes(), &final); err != nil {
			t.Fatalf("decode poll: %v", err)
		}
		if final["status"] != "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if final["status"] != "success" {
		t.Fatalf("final status = %v, want success", final["status"])
	}
	if final["result"] != "# done" {
		t.Errorf("result = %v, want '# done'", final["result"])
	}
}

func TestAPIJobRunNotFound(t *testing.T) {
	h := newJobsHandler(t)
	req := jobReq(http.MethodGet, "/manager/api/jobs/report/runs/missing", nil)
	req.SetPathValue("key", "report")
	req.SetPathValue("runID", "missing")
	rec := httptest.NewRecorder()
	h.apiJobRun(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIToolDetail(t *testing.T) {
	h := newJobsHandler(t)

	cases := []struct {
		name       string
		key        string
		wantStatus int
		wantFields int
	}{
		{name: "known tool projects configs", key: "echo", wantStatus: http.StatusOK, wantFields: 1},
		{name: "unknown tool 404s", key: "nope", wantStatus: http.StatusNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := jobReq(http.MethodGet, "/manager/api/tools/"+tc.key, nil)
			req.SetPathValue("key", tc.key)
			rec := httptest.NewRecorder()
			h.apiToolDetail(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			var got toolDetailJSON
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Key != tc.key {
				t.Errorf("key = %q, want %q", got.Key, tc.key)
			}
			if len(got.Fields) != tc.wantFields {
				t.Errorf("fields = %d, want %d", len(got.Fields), tc.wantFields)
			}
		})
	}
}

func TestAPISetToolConfig(t *testing.T) {
	h := newJobsHandler(t)
	req := jobReq(http.MethodPost, "/manager/api/tools/echo/configs/prefix", map[string]string{"value": ">>"})
	req.SetPathValue("key", "echo")
	req.SetPathValue("configKey", "prefix")
	rec := httptest.NewRecorder()
	h.apiSetToolConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := h.configs.GetOwned("echo", "prefix"); got != ">>" {
		t.Errorf("stored prefix = %q, want >>", got)
	}
}
