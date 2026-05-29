package admin

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/yogasw/wick/internal/pkg/config"
	"github.com/yogasw/wick/internal/pkg/lan"
)

// lanIPs returns the JSON payload the Variables page's "Detect LAN IPs"
// button consumes. It is intentionally read-only — the admin still has
// to paste each suggested URL into the allowed_origins kvlist and hit
// Save. We do not silently mutate the allowlist because a phone on
// public Wi-Fi shouldn't expose the manager to every other device on
// the SSID without an explicit opt-in.
//
// Shape:
//
//	{"port": 9425, "urls": ["http://192.168.1.42:9425", ...]}
func (h *Handler) lanIPs(w http.ResponseWriter, r *http.Request) {
	port := config.Load().App.Port
	if port == 0 {
		port = 9425
	}
	urls := make([]string, 0)
	for _, ip := range lan.DiscoverPrivateIPv4() {
		urls = append(urls, fmt.Sprintf("http://%s:%d", ip, port))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"port": port,
		"urls": urls,
	})
}

