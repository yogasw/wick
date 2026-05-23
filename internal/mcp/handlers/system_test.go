package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// captureResponder builds a Responder that stores the result into
// *out for assertion. Errors aren't expected in WickInfo so the
// WriteError closure just fails the test.
func captureResponder(t *testing.T, out *ToolCallResult) Responder {
	t.Helper()
	return Responder{
		WriteResult: func(_ http.ResponseWriter, _ json.RawMessage, result any) {
			r, ok := result.(ToolCallResult)
			if !ok {
				t.Fatalf("WriteResult got %T, want ToolCallResult", result)
			}
			*out = r
		},
		WriteError: func(_ http.ResponseWriter, _ json.RawMessage, code int, message string, _ any) {
			t.Fatalf("unexpected WriteError code=%d msg=%q", code, message)
		},
	}
}

// callWickInfo invokes WickInfo and returns the parsed info map.
func callWickInfo(t *testing.T, version, commit, buildTime, wickRoot string, db *gorm.DB) map[string]string {
	t.Helper()
	var got ToolCallResult
	WickInfo(httptest.NewRecorder(), RPCRequest{}, captureResponder(t, &got), version, commit, buildTime, wickRoot, db)
	if got.IsError {
		t.Fatalf("WickInfo returned isError=true")
	}
	if len(got.Content) != 1 {
		t.Fatalf("WickInfo content len=%d, want 1", len(got.Content))
	}
	var info map[string]string
	if err := json.Unmarshal([]byte(got.Content[0].Text), &info); err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	return info
}

func newMemoryDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

func TestWickInfoHTTPMode(t *testing.T) {
	info := callWickInfo(t, "1.2.3", "abcdef12", "2025-01-01", "", nil)

	if info["access_type"] != "http" {
		t.Fatalf("access_type = %q, want http", info["access_type"])
	}
	if info["wick_root"] != "" {
		t.Fatalf("wick_root = %q, want empty in http mode", info["wick_root"])
	}
	if info["wick_version"] != "1.2.3" {
		t.Fatalf("wick_version = %q, want 1.2.3", info["wick_version"])
	}
	if info["db_type"] != "none" {
		t.Fatalf("db_type = %q, want none", info["db_type"])
	}
	if info["db_status"] != "disabled" {
		t.Fatalf("db_status = %q, want disabled", info["db_status"])
	}
}

func TestWickInfoCLIMode(t *testing.T) {
	info := callWickInfo(t, "dev", "", "unknown", "/tmp/myproject", nil)

	if info["access_type"] != "cli" {
		t.Fatalf("access_type = %q, want cli", info["access_type"])
	}
	if info["wick_root"] != "/tmp/myproject" {
		t.Fatalf("wick_root = %q, want /tmp/myproject", info["wick_root"])
	}
}

// TestWickInfoDBConnected verifies db_type / db_status reflect a live DB.
func TestWickInfoDBConnected(t *testing.T) {
	info := callWickInfo(t, "dev", "", "unknown", "", newMemoryDB(t))

	if info["db_type"] != "sqlite" {
		t.Fatalf("db_type = %q, want sqlite", info["db_type"])
	}
	if info["db_status"] != "connected" {
		t.Fatalf("db_status = %q, want connected", info["db_status"])
	}
	if _, present := info["db_source"]; present {
		t.Fatalf("db_source must not be exposed; got %q", info["db_source"])
	}
}

// TestWickInfoDBError verifies db_status surfaces a ping error when the
// underlying sql.DB is closed (simulating a runtime connection drop).
func TestWickInfoDBError(t *testing.T) {
	db := newMemoryDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlDB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	info := callWickInfo(t, "dev", "", "unknown", "", db)

	if info["db_type"] != "sqlite" {
		t.Fatalf("db_type = %q, want sqlite", info["db_type"])
	}
	if !strings.HasPrefix(info["db_status"], "error:") {
		t.Fatalf("db_status = %q, want error: prefix", info["db_status"])
	}
}
