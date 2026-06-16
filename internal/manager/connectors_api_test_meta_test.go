package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/tool"
)

// testMetaModule mirrors apiDetailModule but gives each op a small input
// schema so the test-meta projection (input fields) can be asserted.
func testMetaModule(key string) connector.Module {
	return connector.Module{
		Meta: connector.Meta{
			Key:         key,
			Name:        "Slack",
			Icon:        "💬",
			DefaultTags: []tool.DefaultTag{},
		},
		Operations: []connector.Operation{
			{
				Key:         "send",
				Name:        "Send",
				Description: "Send a message",
				Input: []entity.Config{
					{Key: "channel", Type: "text", Required: true, Description: "target channel"},
					{Key: "text", Type: "textarea"},
				},
				Execute: noopExec,
			},
			{
				Key:         "del",
				Name:        "Delete",
				Description: "Delete a message",
				Destructive: true,
				Execute:     noopExec,
			},
		},
	}
}

func newTestMetaHandler(t *testing.T) (*Handler, *connectors.Service) {
	t.Helper()
	svc := newConnectorsSvcForAPI(t, []connector.Module{testMetaModule("slack")})
	return &Handler{connectors: svc}, svc
}

func TestAPIConnectorTestMeta(t *testing.T) {
	h, svc := newTestMetaHandler(t)
	row, err := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	req := adminReq(t, http.MethodGet, "/manager/api/connectors/slack/"+row.ID+"/test-meta", nil)
	req.SetPathValue("key", "slack")
	req.SetPathValue("id", row.ID)
	rec := httptest.NewRecorder()
	h.apiConnectorTestMeta(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got testMetaJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Key != "slack" || got.ID != row.ID || got.Label != "Prod" {
		t.Errorf("identity wrong: %+v", got)
	}
	if len(got.Ops) != 2 {
		t.Fatalf("ops len = %d, want 2", len(got.Ops))
	}
	byKey := map[string]testOpJSON{}
	for _, op := range got.Ops {
		byKey[op.Key] = op
	}
	send, ok := byKey["send"]
	if !ok {
		t.Fatalf("send op missing")
	}
	if len(send.Input) != 2 {
		t.Fatalf("send input len = %d, want 2: %+v", len(send.Input), send.Input)
	}
	if send.Input[0].Key != "channel" || !send.Input[0].Required {
		t.Errorf("channel input wrong: %+v", send.Input[0])
	}
	if send.Input[1].Type != "textarea" {
		t.Errorf("text input type = %q, want textarea", send.Input[1].Type)
	}
	del, ok := byKey["del"]
	if !ok {
		t.Fatalf("del op missing")
	}
	if !del.Destructive {
		t.Errorf("del op should be destructive")
	}
	if len(del.Input) != 0 {
		t.Errorf("del op should have no input: %+v", del.Input)
	}
}

func TestAPIConnectorTestMetaUnknownKey(t *testing.T) {
	h, _ := newTestMetaHandler(t)
	req := adminReq(t, http.MethodGet, "/manager/api/connectors/nope/x/test-meta", nil)
	req.SetPathValue("key", "nope")
	req.SetPathValue("id", "x")
	rec := httptest.NewRecorder()
	h.apiConnectorTestMeta(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAPIConnectorTestMetaRowNotFound(t *testing.T) {
	h, _ := newTestMetaHandler(t)
	req := adminReq(t, http.MethodGet, "/manager/api/connectors/slack/missing/test-meta", nil)
	req.SetPathValue("key", "slack")
	req.SetPathValue("id", "missing")
	rec := httptest.NewRecorder()
	h.apiConnectorTestMeta(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
