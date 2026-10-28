package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/plugin/toolplugin"
)

const mrMerged = `{"object_kind":"merge_request","user":{"username":"dev"},
"project":{"path_with_namespace":"acme/web","web_url":"https://gitlab.example.com/acme/web"},
"object_attributes":{"iid":12,"title":"Add login","url":"https://gitlab.example.com/acme/web/-/merge_requests/12",
"action":"merge","state":"merged","source_branch":"feat/login","target_branch":"main","merge_commit_sha":"abc123"}}`

func TestParseEvent(t *testing.T) {
	ev, err := ParseEvent([]byte(mrMerged))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != "merge_request" || ev.Project != "acme/web" || ev.MRIID != 12 || ev.Action != "merge" || ev.SHA != "abc123" {
		t.Fatalf("unexpected event: %+v", ev)
	}
	pe, err := ParseEvent([]byte(`{"object_kind":"pipeline","project":{"path_with_namespace":"acme/web","web_url":"https://gitlab.example.com/acme/web"},"object_attributes":{"id":99,"ref":"main","status":"failed"}}`))
	if err != nil || pe.Status != "failed" || pe.URL != "https://gitlab.example.com/acme/web/-/pipelines/99" {
		t.Fatalf("unexpected pipeline event: %+v %v", pe, err)
	}
	if _, err := ParseEvent([]byte(`{"object_kind":"push"}`)); err == nil {
		t.Fatal("an event without a project must be refused")
	}
}

func TestReceive(t *testing.T) {
	var gotTopic string
	publish = func(_ context.Context, topic string, ev Event) (int, error) {
		gotTopic = topic
		return 1, nil
	}
	h := toolplugin.Handler(Module(), map[string]string{"webhook_secret": "s3cret"})
	post := func(token, uuid string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/tools/gitlab_webhook/webhook/gitlab", strings.NewReader(mrMerged))
		req.Header.Set("X-Gitlab-Token", token)
		req.Header.Set("X-Gitlab-Event-UUID", uuid)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := post("wrong", "u1"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: %d", rec.Code)
	}
	if rec := post("s3cret", "u2"); rec.Code != http.StatusOK || gotTopic != "acme/web" || !strings.Contains(rec.Body.String(), `"woken":1`) {
		t.Fatalf("good token: %d %s topic=%q", rec.Code, rec.Body.String(), gotTopic)
	}
	gotTopic = ""
	if rec := post("s3cret", "u2"); !strings.Contains(rec.Body.String(), "duplicate") || gotTopic != "" {
		t.Fatalf("a retried delivery must not publish twice: %s", rec.Body.String())
	}
}
