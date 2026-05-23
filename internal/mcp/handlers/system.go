package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/appname"
	"gorm.io/gorm"
)

// WickInfo handles the wick_info tool. All system-info derivation
// (access_type from wickRoot, db_type/db_status from the live db
// handle) happens here — Handler just hands over the raw state.
//
// db may be nil (tests, smoke mode); in that case db_type is "none"
// and db_status is "disabled". The DSN is intentionally not exposed —
// hostname and user are sensitive infra info.
func WickInfo(w http.ResponseWriter, req RPCRequest, rsp Responder, version, commit, buildTime, wickRoot string, db *gorm.DB) {
	accessType := "http"
	if wickRoot != "" {
		accessType = "cli"
	}
	dbType, dbStatus := dbInfo(db)
	info := map[string]string{
		"app_name":          appname.Resolve(),
		"app_version":       appname.BuildAppVersion,
		"wick_version":      version,
		"server_build_time": buildTime,
		"server_commit":     commit,
		"access_type":       accessType,
		"wick_root":         wickRoot,
		"db_type":           dbType,
		"db_status":         dbStatus,
	}
	b, _ := json.Marshal(info)
	rsp.WriteResult(w, req.ID, ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: string(b)}},
	})
}

// dbInfo returns (type, status) derived from a live gorm handle.
// Type is the dialector name ("postgres" / "sqlite") or "none" when
// db is nil. Status is "connected", "error: <err>", or "disabled".
func dbInfo(db *gorm.DB) (dbType, dbStatus string) {
	if db == nil {
		return "none", "disabled"
	}
	dbType = db.Dialector.Name()
	sqlDB, err := db.DB()
	if err != nil {
		return dbType, "error: " + err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		return dbType, "error: " + err.Error()
	}
	return dbType, "connected"
}

// WickEncrypt handles the wick_encrypt tool — redirect only, no crypto over MCP.
func WickEncrypt(w http.ResponseWriter, req RPCRequest, rsp Responder, encfieldsURL func(string) string) {
	rsp.ToolJSON(w, req.ID, map[string]string{
		"message": "Encryption must be done via the Wick UI. Open the URL, log in, paste the plaintext, and copy the wick_enc_ token back into the conversation.",
		"url":     encfieldsURL("encrypt"),
	})
}

// WickDecrypt handles the wick_decrypt tool — redirect only, no crypto over MCP.
func WickDecrypt(w http.ResponseWriter, req RPCRequest, rsp Responder, encfieldsURL func(string) string) {
	rsp.ToolJSON(w, req.ID, map[string]string{
		"message": "Decryption must be done via the Wick UI. Per-user keys mean only the user who issued a wick_enc_ token can reveal its plaintext.",
		"url":     encfieldsURL("decrypt"),
	})
}

// EncfieldsURL builds the absolute URL for the encrypt/decrypt UI page.
func EncfieldsURL(appURL func() string, suffix string) string {
	base := ""
	if appURL != nil {
		base = strings.TrimRight(appURL(), "/")
	}
	if suffix == "encrypt" {
		return base + "/tools/encfields"
	}
	return base + "/tools/encfields/" + suffix
}
