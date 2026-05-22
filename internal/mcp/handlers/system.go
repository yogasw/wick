package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/yogasw/wick/internal/appname"
)

// WickInfo handles the wick_info tool.
func WickInfo(w http.ResponseWriter, req RPCRequest, rsp Responder, version, commit, buildTime, wickRoot string) {
	accessType := "http"
	if wickRoot != "" {
		accessType = "cli"
	}
	info := map[string]string{
		"app_name":          appname.Resolve(),
		"app_version":       appname.BuildAppVersion,
		"wick_version":      version,
		"server_build_time": buildTime,
		"server_commit":     commit,
		"access_type":       accessType,
		"wick_root":         wickRoot,
	}
	b, _ := json.Marshal(info)
	rsp.WriteResult(w, req.ID, ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: string(b)}},
	})
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
