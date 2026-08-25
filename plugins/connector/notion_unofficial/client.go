package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/pkg/connector"
)

// This is a hand-rolled client for Notion's PRIVATE web API (api/v3). It exists
// because the kjk/notionapi library can no longer parse Notion's current
// response shape: recordMap now carries a numeric "__version__":3 field and
// records nest as value.value{…}, and the library's json-iterator decode
// (map[string]*Record over the whole recordMap) blows up with
// `ReadMapCB: expect { or n, but found 3`. Rather than depend on a broken lib
// for an undocumented API, we parse the pieces we need tolerantly:
//   - skip the "__version__" key when ranging recordMap sub-maps,
//   - unwrap both value{…} and value.value{…} record shapes.
// Requests carry c.Context() so the host can cancel them (the lib couldn't).

const privateBase = "https://www.notion.so/api/v3"

// Defaults for the browser-mimicking headers. The api/v3 endpoints are the
// browser's private API; a default "Go-http-client" User-Agent stands out and
// risks being flagged, so we present as a normal browser by default. Both are
// overridable per instance via the user_agent / notion_client_version configs.
const (
	defaultUserAgent           = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	defaultNotionClientVersion = "23.13.0.0"
)

type v3Client struct {
	c         *connector.Ctx
	token     string
	user      string
	userAgent string
	clientVer string

	// cached identity for write ops (space_id + user_id), resolved lazily.
	spaceID    string
	resolvedU  string
	identityOK bool
}

func newClient(c *connector.Ctx) (*v3Client, error) {
	// Credentials live in individual config fields. The easy way to fill them is
	// the import widget (import_form / import_curl_extract), which parses a pasted
	// Copy-as-cURL and writes token_v2 + user_agent + notion_client_version +
	// active_user_id here — but an operator can also fill them by hand.
	token := strings.TrimSpace(c.Cfg("token_v2"))
	if token == "" {
		return nil, errors.New("no token_v2 — use the import widget (paste a Copy-as-cURL) or fill token_v2")
	}
	ua := firstNonEmpty(strings.TrimSpace(c.Cfg("user_agent")), defaultUserAgent)
	ver := firstNonEmpty(strings.TrimSpace(c.Cfg("notion_client_version")), defaultNotionClientVersion)

	return &v3Client{
		c:         c,
		token:     token,
		user:      strings.TrimSpace(c.Cfg("active_user_id")),
		userAgent: ua,
		clientVer: ver,
	}, nil
}

// firstNonEmpty returns the first trimmed-non-empty argument, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// identity resolves the workspace space_id and current user_id, needed by write
// transactions. It calls loadUserContent once and caches the result. The
// configured active_user_id (if any) wins for the user id.
func (cl *v3Client) identity() (spaceID, userID string, err error) {
	if cl.identityOK {
		return cl.spaceID, cl.resolvedU, nil
	}
	// A configured/curl-provided user id is the resolvedU; a curl space id may
	// already be seeded on cl.spaceID. If BOTH are present we can skip the
	// network round-trip entirely.
	if cl.user != "" {
		cl.resolvedU = cl.user
	}
	if cl.spaceID != "" && cl.resolvedU != "" {
		cl.identityOK = true
		return cl.spaceID, cl.resolvedU, nil
	}

	rm, err := cl.loadUserContent()
	if err != nil {
		return "", "", err
	}
	if cl.spaceID == "" {
		cl.spaceID = firstKey(rm.Space)
	}
	if cl.resolvedU == "" {
		cl.resolvedU = firstKey(rm.NotionUser)
	}
	if cl.spaceID == "" || cl.resolvedU == "" {
		return "", "", errors.New("could not resolve workspace/user identity from token")
	}
	cl.identityOK = true
	return cl.spaceID, cl.resolvedU, nil
}

// maxRetries429 bounds the rate-limit retry loop.
const maxRetries429 = 3

// post sends a JSON body to an api/v3 endpoint and returns the raw response.
// On HTTP 429 it retries with backoff (honoring Retry-After and the request
// context), up to maxRetries429 — the private API rate-limits at ~3 req/s.
func (cl *v3Client) post(path string, body any) ([]byte, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(cl.c.Context(), http.MethodPost, privateBase+path, bytes.NewReader(b))
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("cookie", "token_v2="+cl.token)
		// The api/v3 endpoints are the browser's private API, not a public API. A
		// default "Go-http-client" User-Agent stands out and risks being flagged
		// or blocked, so we present as a normal browser (and send the
		// client-version header the web app sends). Cosmetic to the payload but
		// keep the request looking like the session it borrows the cookie from.
		req.Header.Set("User-Agent", cl.userAgent)
		req.Header.Set("Notion-Client-Version", cl.clientVer)
		if cl.user != "" {
			req.Header.Set("x-notion-active-user-header", cl.user)
		}

		resp, err := cl.c.HTTP.Do(req)
		if err != nil {
			return nil, fmt.Errorf("call notion: %w", err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRetries429 {
			wait := retryAfter(resp.Header.Get("Retry-After"), attempt)
			select {
			case <-cl.c.Context().Done():
				return nil, cl.c.Context().Err()
			case <-time.After(wait):
				continue
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, privateError(resp.StatusCode, raw)
		}
		return raw, nil
	}
}

// retryAfter returns how long to wait before a 429 retry: the server's
// Retry-After (seconds) when present, else exponential backoff (1s, 2s, 4s).
func retryAfter(header string, attempt int) time.Duration {
	if header != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return time.Duration(1<<attempt) * time.Second
}

// saveTransactions is the write endpoint (replaces the old, now-404
// submitTransaction). ops is a list of operation objects, each already carrying
// its pointer{table,id,spaceId}, path, command, args. They run in one
// transaction scoped to spaceID.
func (cl *v3Client) saveTransactions(spaceID string, ops []map[string]any) error {
	body := map[string]any{
		"requestId": newUUID(),
		"transactions": []any{
			map[string]any{
				"id":         newUUID(),
				"spaceId":    spaceID,
				"operations": ops,
			},
		},
	}
	_, err := cl.post("/saveTransactions", body)
	return err
}

// op builds one saveTransactions operation.
func op(table, id, spaceID string, path []any, command string, args any) map[string]any {
	return map[string]any{
		"pointer": map[string]any{"table": table, "id": id, "spaceId": spaceID},
		"path":    path,
		"command": command,
		"args":    args,
	}
}

// newUUID returns a random v4 UUID string (for new block/discussion/comment ids
// and the transaction/request ids the write endpoint expects).
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func nowMillis() int64 { return time.Now().UnixMilli() }

func privateError(status int, body []byte) error {
	// 401 almost always means the token_v2 cookie expired or was invalidated
	// (logout / password change / rotation). The private API has no refresh, so
	// point the operator at the fix instead of a bare "401".
	if status == http.StatusUnauthorized {
		return errors.New("notion 401: not authenticated — token_v2 expired or invalid. Re-import a fresh Copy-as-cURL (or paste a new token_v2) from a logged-in notion.so session")
	}
	var env struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &env) == nil && env.Message != "" {
		return fmt.Errorf("notion %d: %s", status, env.Message)
	}
	if msg := strings.TrimSpace(string(body)); msg != "" {
		return fmt.Errorf("notion %d: %s", status, shorten(msg, 300))
	}
	return fmt.Errorf("notion %d", status)
}

// --- recordMap parsing (tolerant) ---

// recordMap is the shared envelope: a map of table → (id → record), plus the
// numeric __version__ we skip. We keep sub-records as raw JSON and unwrap them
// on demand via recordValue.
type recordMap struct {
	Block          map[string]json.RawMessage `json:"block"`
	Collection     map[string]json.RawMessage `json:"collection"`
	CollectionView map[string]json.RawMessage `json:"collection_view"`
	NotionUser     map[string]json.RawMessage `json:"notion_user"`
	Space          map[string]json.RawMessage `json:"space"`
}

// recordValue unwraps a record entry to its inner value object. Records arrive
// as {"value":{…}} on older shapes and {"value":{"value":{…}}} on the current
// one; we handle both. Returns nil if the entry has no usable value.
func recordValue(raw json.RawMessage) map[string]json.RawMessage {
	var wrap struct {
		Value json.RawMessage `json:"value"`
	}
	if json.Unmarshal(raw, &wrap) != nil || len(wrap.Value) == 0 {
		return nil
	}
	// Peek: is Value itself a {"value":{…}} wrapper?
	var inner struct {
		Value json.RawMessage `json:"value"`
	}
	if json.Unmarshal(wrap.Value, &inner) == nil && len(inner.Value) > 0 {
		// Ambiguous: a real block also has no top-level "value" inside its value.
		// Only unwrap again if the inner "value" looks like an object with an id.
		var probe struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(inner.Value, &probe) == nil && probe.ID != "" {
			var m map[string]json.RawMessage
			if json.Unmarshal(inner.Value, &m) == nil {
				return m
			}
		}
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(wrap.Value, &m) == nil {
		return m
	}
	return nil
}

// firstRecord returns the unwrapped value of the first entry in a table map,
// skipping any non-record keys. Handy for single-record tables (space, user).
func firstRecord(tbl map[string]json.RawMessage) map[string]json.RawMessage {
	for _, raw := range tbl {
		if v := recordValue(raw); v != nil {
			return v
		}
	}
	return nil
}

// strField reads a string field from an unwrapped record.
func strField(rec map[string]json.RawMessage, key string) string {
	if rec == nil {
		return ""
	}
	raw, ok := rec[key]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}
