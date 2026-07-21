package manager

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/yogasw/wick/internal/login"
)

// Extension management for the playwright_browser connector (manager UI).
//
// Extensions are stored + unpacked by the plugin (under its sessionDir); core is
// just the HTTP edge: it receives the upload (multipart) or fetches a .crx from
// the Chrome Web Store by id, then hands the bytes to the plugin's
// extension_install op as base64. list / remove are thin op wrappers.

// extSlugRE sanitizes a caller-derived extension id (from a filename or a store
// id) into a safe folder slug. Anything else is replaced with '-'.
var extSlugRE = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// storeIDRE matches a Chrome Web Store extension id: 32 lowercase a–p letters.
var storeIDRE = regexp.MustCompile(`^[a-p]{32}$`)

const maxExtensionUpload = 64 << 20 // 64 MiB cap
const extTooLargeMsg = "extension too large (max 64 MB)"

func extSlug(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ".zip")
	s = strings.TrimSuffix(s, ".crx")
	s = extSlugRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-.")
	if s == "" {
		s = "ext"
	}
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// apiBrowserExtensions lists installed extensions (extension_list op).
func (h *Handler) apiBrowserExtensions(w http.ResponseWriter, r *http.Request) {
	row, ok := h.resolveBrowserSession(w, r)
	if !ok {
		return
	}
	var out map[string]any
	if errMsg := h.execBrowserOp(r.Context(), login.GetUser(r.Context()), r, row.ID, "extension_list", map[string]string{}, &out); errMsg != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": errMsg})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// apiBrowserExtensionUpload receives a multipart .zip/.crx and installs it via
// the plugin (extension_install with base64 bytes).
func (h *Handler) apiBrowserExtensionUpload(w http.ResponseWriter, r *http.Request) {
	row, ok := h.resolveBrowserSession(w, r)
	if !ok {
		return
	}
	// Cap the whole request body so a large upload can't exhaust memory.
	r.Body = http.MaxBytesReader(w, r.Body, maxExtensionUpload+1024) // + slop for multipart framing
	if err := r.ParseMultipartForm(maxExtensionUpload); err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": extTooLargeMsg})
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no file field"})
		return
	}
	defer file.Close()
	if hdr.Size > maxExtensionUpload {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": extTooLargeMsg})
		return
	}
	// Read one extra byte so an over-limit file is rejected, not silently truncated.
	raw, err := io.ReadAll(io.LimitReader(file, maxExtensionUpload+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read upload: " + err.Error()})
		return
	}
	if len(raw) > maxExtensionUpload {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": extTooLargeMsg})
		return
	}
	id := extSlug(hdr.Filename)
	h.installExtensionBytes(w, r, row.ID, id, raw)
}

// apiBrowserExtensionFromStore fetches a .crx from the Chrome Web Store by id
// and installs it. Body: {"id": "<32-char store id>"}.
func (h *Handler) apiBrowserExtensionFromStore(w http.ResponseWriter, r *http.Request) {
	row, ok := h.resolveBrowserSession(w, r)
	if !ok {
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	storeID := strings.TrimSpace(body.ID)
	if !storeIDRE.MatchString(storeID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a valid Chrome Web Store id (32 letters a–p)"})
		return
	}
	raw, err := fetchWebStoreCRX(r, storeID)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	h.installExtensionBytes(w, r, row.ID, storeID, raw)
}

// apiBrowserExtensionRemove removes an installed extension (extension_remove).
func (h *Handler) apiBrowserExtensionRemove(w http.ResponseWriter, r *http.Request) {
	row, ok := h.resolveBrowserSession(w, r)
	if !ok {
		return
	}
	id := r.PathValue("extID")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "extension id required"})
		return
	}
	var out map[string]any
	if errMsg := h.execBrowserOp(r.Context(), login.GetUser(r.Context()), r, row.ID, "extension_remove", map[string]string{"id": id}, &out); errMsg != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": errMsg})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// installExtensionBytes hands raw archive bytes to the plugin's install op.
func (h *Handler) installExtensionBytes(w http.ResponseWriter, r *http.Request, rowID, id string, raw []byte) {
	if len(raw) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty extension archive"})
		return
	}
	var out map[string]any
	errMsg := h.execBrowserOp(r.Context(), login.GetUser(r.Context()), r, rowID, "extension_install", map[string]string{
		"id":   id,
		"data": base64.StdEncoding.EncodeToString(raw),
	}, &out)
	if errMsg != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": errMsg})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// fetchWebStoreCRX downloads a .crx for a Web Store id via the public update
// endpoint (follows the redirect to the CDN). prodversion is a recent-enough
// Chrome so the store serves a crx3.
func fetchWebStoreCRX(r *http.Request, storeID string) ([]byte, error) {
	q := url.Values{}
	q.Set("response", "redirect")
	q.Set("acceptformat", "crx2,crx3")
	q.Set("prodversion", "120.0")
	q.Set("x", "id="+storeID+"&installsource=ondemand&uc")
	dl := "https://clients2.google.com/service/update2/crx?" + q.Encode()

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, dl, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach Chrome Web Store: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Web Store returned %d for %s (id may not exist)", resp.StatusCode, storeID)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxExtensionUpload+1))
	if err != nil {
		return nil, fmt.Errorf("download crx: %w", err)
	}
	if len(raw) > maxExtensionUpload {
		return nil, errors.New(extTooLargeMsg)
	}
	if len(raw) < 16 || string(raw[0:4]) != "Cr24" {
		return nil, fmt.Errorf("download was not a .crx (id may be wrong or unavailable)")
	}
	return raw, nil
}
