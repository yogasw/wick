package manager

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/internal/plugins/source"
)

func newSourcesHandler(t *testing.T) *PluginSourcesHandler {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	postgres.Migrate(db)
	t.Setenv("WICK_PLUGINS_ROOT", t.TempDir())
	return &PluginSourcesHandler{Sources: &source.Manager{DB: db, Client: &source.Client{},
		Encrypt: func(p string) (string, error) { return "wick_cenc_" + strings.Repeat("x", len(p)), nil }}}
}

func TestPluginSourcesAPINeverReturnsPAT(t *testing.T) {
	h := newSourcesHandler(t)
	body := `{"type":"github","repo":"acme/plugins","private":true,"pat":"ghp_supersecret"}`
	rec := httptest.NewRecorder()
	h.apiCreate(rec, httptest.NewRequest(http.MethodPost, "/manager/api/plugin-sources", strings.NewReader(body)))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"has_pat":true`) {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	h.apiList(rec, httptest.NewRequest(http.MethodGet, "/manager/api/plugin-sources", nil))
	if out := rec.Body.String(); strings.Contains(out, "ghp_") || strings.Contains(out, "wick_cenc_") || !strings.Contains(out, `"auto_update":false`) {
		t.Fatalf("list leaks PAT or wrong defaults: %s", out)
	}
}

func TestPluginUploadRejectsInvalidZip(t *testing.T) {
	h := newSourcesHandler(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "bogus-1.0.0-linux-amd64.zip")
	_, _ = fw.Write([]byte("not a zip"))
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/manager/api/plugins/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.apiUpload(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid zip accepted: %d %s", rec.Code, rec.Body)
	}
	rows, _ := h.Sources.Audit(5)
	if len(rows) != 1 || rows[0].Action != "upload.fail" {
		t.Fatalf("audit: %+v", rows)
	}
}
