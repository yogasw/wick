package slack

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yogasw/wick/internal/appname"
	"github.com/yogasw/wick/pkg/connector"
)

// pickToken resolves the active OAuth token based on auth_mode. Empty
// auth_mode defaults to bot_token (matches the dropdown default). Falls
// back to a legacy `token` field for rows that were seeded before the
// auth_mode split.
func pickToken(c *connector.Ctx) (string, error) {
	mode := strings.TrimSpace(c.Cfg("auth_mode"))
	if mode == "" {
		mode = "bot_token"
	}
	var token string
	switch mode {
	case "user_token":
		token = strings.TrimSpace(c.Cfg("user_token"))
	case "bot_token":
		token = strings.TrimSpace(c.Cfg("bot_token"))
	default:
		return "", fmt.Errorf("unknown auth_mode %q", mode)
	}
	if token == "" {
		// Legacy fallback for rows that still carry the old single `token` key.
		token = strings.TrimSpace(c.Cfg("token"))
	}
	if token == "" {
		return "", fmt.Errorf("slack %s is not configured for this connector instance", mode)
	}
	return token, nil
}

// baseURLOverride is non-empty only in tests — see slack_test.go's
// withBaseURL helper. Production code uses defaultBaseURL.
var baseURLOverride string

func buildURL(c *connector.Ctx, method string) string {
	base := defaultBaseURL
	if baseURLOverride != "" {
		base = baseURLOverride
	}
	return base + "/" + method
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func clampInt(v, min, max, def int) int {
	if v <= 0 {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func boolForm(v bool, def bool) string {
	if !v {
		if def {
			return "true"
		}
		return "false"
	}
	return "true"
}

// parseBlocks decodes a JSON-encoded Block Kit array supplied as a
// string input. Slack expects the array shape; we accept both an array
// and a single object (wrapped into a one-element array).
func parseBlocks(raw string) (any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var arr []any
	if err := json.Unmarshal([]byte(raw), &arr); err == nil {
		return arr, nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err == nil {
		return []any{obj}, nil
	}
	return nil, fmt.Errorf("blocks must be a JSON array or object: %s", truncate(raw, 80))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ── Response shaping ─────────────────────────────────────────────────
//
// Slack envelopes carry a lot of noise (team metadata, latest_reply
// blobs, response_metadata). We project each response down to the
// fields an LLM is most likely to need so the schema stays stable
// across upstream cosmetic changes.

func shapeChannelList(raw any, nameFilter string) any {
	m, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	channels, _ := m["channels"].([]any)
	out := make([]map[string]any, 0, len(channels))
	for _, ch := range channels {
		shaped := shapeOneChannel(ch)
		if nameFilter != "" {
			name, _ := shaped["name"].(string)
			if !strings.Contains(strings.ToLower(name), nameFilter) {
				continue
			}
		}
		out = append(out, shaped)
	}
	resp := map[string]any{"channels": out}
	if cursor := cursorFrom(m); cursor != "" {
		resp["next_cursor"] = cursor
	}
	return resp
}

func shapeChannelSearch(raw any, q string, limit int) any {
	m, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	channels, _ := m["channels"].([]any)
	matches := make([]map[string]any, 0, limit)
	for _, ch := range channels {
		shaped := shapeOneChannel(ch)
		name, _ := shaped["name"].(string)
		if !strings.Contains(strings.ToLower(name), q) {
			continue
		}
		matches = append(matches, shaped)
		if len(matches) >= limit {
			break
		}
	}
	return map[string]any{"matches": matches, "query": q}
}

func shapeChannelInfo(raw any) any {
	m, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	if ch, ok := m["channel"].(map[string]any); ok {
		return shapeOneChannel(ch)
	}
	return raw
}

func shapeOneChannel(in any) map[string]any {
	m, ok := in.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	out := map[string]any{
		"id":          m["id"],
		"name":        m["name"],
		"is_private":  m["is_private"],
		"is_archived": m["is_archived"],
		"is_im":       m["is_im"],
		"is_mpim":     m["is_mpim"],
		"created":     m["created"],
		"creator":     m["creator"],
	}
	if topic, ok := m["topic"].(map[string]any); ok {
		out["topic"] = topic["value"]
	}
	if purpose, ok := m["purpose"].(map[string]any); ok {
		out["purpose"] = purpose["value"]
	}
	if n, ok := m["num_members"]; ok {
		out["num_members"] = n
	}
	return out
}

func shapeMessageList(raw any) any {
	m, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	msgs, _ := m["messages"].([]any)
	out := make([]map[string]any, 0, len(msgs))
	for _, msg := range msgs {
		out = append(out, shapeOneMessage(msg))
	}
	resp := map[string]any{"messages": out}
	if hm, ok := m["has_more"]; ok {
		resp["has_more"] = hm
	}
	if cursor := cursorFrom(m); cursor != "" {
		resp["next_cursor"] = cursor
	}
	return resp
}

func shapeOneMessage(in any) map[string]any {
	m, ok := in.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	out := map[string]any{
		"ts":   m["ts"],
		"user": m["user"],
		"text": m["text"],
		"type": m["type"],
	}
	if v, ok := m["bot_id"]; ok {
		out["bot_id"] = v
	}
	if v, ok := m["thread_ts"]; ok {
		out["thread_ts"] = v
	}
	if v, ok := m["reply_count"]; ok {
		out["reply_count"] = v
	}
	if v, ok := m["subtype"]; ok {
		out["subtype"] = v
	}
	if reactions, ok := m["reactions"].([]any); ok && len(reactions) > 0 {
		shapedReacts := make([]map[string]any, 0, len(reactions))
		for _, r := range reactions {
			if rm, ok := r.(map[string]any); ok {
				shapedReacts = append(shapedReacts, map[string]any{
					"name":  rm["name"],
					"count": rm["count"],
					"users": rm["users"],
				})
			}
		}
		out["reactions"] = shapedReacts
	}
	return out
}

func shapeUserList(raw any, includeDeleted bool) any {
	m, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	members, _ := m["members"].([]any)
	out := make([]map[string]any, 0, len(members))
	for _, u := range members {
		shaped := shapeOneUser(u)
		if !includeDeleted {
			if d, _ := shaped["deleted"].(bool); d {
				continue
			}
		}
		out = append(out, shaped)
	}
	resp := map[string]any{"users": out}
	if cursor := cursorFrom(m); cursor != "" {
		resp["next_cursor"] = cursor
	}
	return resp
}

func shapeUserSingle(raw any) any {
	m, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	if u, ok := m["user"].(map[string]any); ok {
		return shapeOneUser(u)
	}
	return shapeOneUser(m)
}

func shapeOneUser(in any) map[string]any {
	m, ok := in.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	out := map[string]any{
		"id":       m["id"],
		"name":     m["name"],
		"deleted":  m["deleted"],
		"is_bot":   m["is_bot"],
		"is_admin": m["is_admin"],
		"team_id":  m["team_id"],
		"tz":       m["tz"],
	}
	if profile, ok := m["profile"].(map[string]any); ok {
		out["real_name"] = profile["real_name"]
		out["display_name"] = profile["display_name"]
		out["email"] = profile["email"]
		out["title"] = profile["title"]
	}
	return out
}

func shapePostResult(raw any) any {
	m, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	out := map[string]any{
		"channel": m["channel"],
		"ts":      m["ts"],
	}
	if msg, ok := m["message"].(map[string]any); ok {
		out["text"] = msg["text"]
		if v, ok := msg["thread_ts"]; ok {
			out["thread_ts"] = v
		}
	}
	return out
}

// maxUploadBytes caps a single upload_file call. Slack itself allows up to
// 1 GB, but the connector holds the whole file in memory across three HTTP
// steps, so keep it to something the daemon can absorb without being
// knocked over by one call.
const maxUploadBytes = 64 << 20

// uploadSandboxRoot is the only tree a path= upload will read from: wick's
// own agents dir, which is where session, project, and workspace files live
// — the report an agent just generated, the screenshot it downloaded. A
// wider root would turn one Slack write scope into "read any file on the
// host and post it to a channel" (~/.ssh/id_rsa, ~/.aws/credentials, a
// service .env), so the check is deliberate, not incidental. Indirected
// through a var so tests can point it at a temp dir.
var uploadSandboxRoot = appname.AgentsDir

// resolveUploadSource turns upload_file's three mutually exclusive content
// inputs into the bytes to send and the name to send them under. Exactly one
// of path / content_base64 / content must be set:
//
//   - path          — read from disk, the only way to send something large;
//     also supplies the filename when the caller omits one
//   - content_base64 — arbitrary bytes held inline (PDF, PNG, zip)
//   - content       — a UTF-8 string, for text files
//
// Returns the bytes and the resolved filename.
func resolveUploadSource(path, contentB64, content, filename string) ([]byte, string, error) {
	path = strings.TrimSpace(path)
	contentB64 = strings.TrimSpace(contentB64)
	filename = strings.TrimSpace(filename)

	set := make([]string, 0, 3)
	if path != "" {
		set = append(set, "path")
	}
	if contentB64 != "" {
		set = append(set, "content_base64")
	}
	if content != "" {
		set = append(set, "content")
	}
	switch {
	case len(set) == 0:
		return nil, "", fmt.Errorf("one of path, content_base64, or content is required")
	case len(set) > 1:
		return nil, "", fmt.Errorf("path, content_base64, and content are mutually exclusive — got %s", strings.Join(set, " + "))
	}

	switch set[0] {
	case "path":
		abs, err := resolveUploadPath(path)
		if err != nil {
			return nil, "", err
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			return nil, "", fmt.Errorf("read path: %w", err)
		}
		if filename == "" {
			filename = filepath.Base(abs)
		}
		return b, filename, nil

	case "content_base64":
		b, err := decodeUploadBase64(contentB64)
		if err != nil {
			return nil, "", err
		}
		if len(b) > maxUploadBytes {
			return nil, "", fmt.Errorf("content_base64 decodes to %d bytes, over the %d-byte limit", len(b), maxUploadBytes)
		}
		if filename == "" {
			return nil, "", fmt.Errorf("filename is required when uploading content_base64")
		}
		return b, filename, nil

	default:
		if len(content) > maxUploadBytes {
			return nil, "", fmt.Errorf("content is %d bytes, over the %d-byte limit", len(content), maxUploadBytes)
		}
		if filename == "" {
			return nil, "", fmt.Errorf("filename is required when uploading content")
		}
		return []byte(content), filename, nil
	}
}

// decodeUploadBase64 accepts both standard and URL-safe base64, with or
// without padding, because callers paste whatever their tool produced.
func decodeUploadBase64(s string) ([]byte, error) {
	s = strings.Join(strings.Fields(s), "") // strip the newlines a wrapped blob carries
	encodings := []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	}
	for _, enc := range encodings {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("content_base64 is not valid base64")
}

// resolveUploadPath validates a path= upload against uploadSandboxRoot and
// returns the absolute, symlink-resolved file to read. Symlinks are resolved
// BEFORE the containment check so a link planted inside the sandbox cannot
// point at something outside it.
func resolveUploadPath(path string) (string, error) {
	root := uploadSandboxRoot()
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be absolute (got %q) — pass the full path, e.g. %s/projects/<project-id>/files/report.pdf", path, root)
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	} else {
		root = filepath.Clean(root)
	}
	abs, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("path: %w", err)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside %s — upload_file only reads files from wick's agents dir (session, project, and workspace files)", path, root)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("path: %w", err)
	}
	if st.IsDir() {
		return "", fmt.Errorf("path %q is a directory, not a file", path)
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("path %q is not a regular file", path)
	}
	if st.Size() > maxUploadBytes {
		return "", fmt.Errorf("path %q is %d bytes, over the %d-byte limit", path, st.Size(), maxUploadBytes)
	}
	return abs, nil
}

func shapeUploadResult(raw any) (any, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return raw, nil
	}
	files, _ := m["files"].([]any)
	if len(files) == 0 {
		return raw, nil
	}
	f, _ := files[0].(map[string]any)
	out := map[string]any{
		"file_id":   f["id"],
		"name":      f["name"],
		"title":     f["title"],
		"permalink": f["permalink"],
	}
	if ch, ok := f["channels"].([]any); ok && len(ch) > 0 {
		out["channel"] = ch[0]
	}
	return out, nil
}

// strAt reads a string field from a decoded JSON map, empty if absent or
// not a string.
func strAt(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func shapeReactions(raw any) any {
	m, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	// reactions.get nests the list under the target type (message/file);
	// pull whichever carries the reactions array.
	var reactions []any
	if msg, ok := m["message"].(map[string]any); ok {
		reactions, _ = msg["reactions"].([]any)
	}
	if reactions == nil {
		if f, ok := m["file"].(map[string]any); ok {
			reactions, _ = f["reactions"].([]any)
		}
	}
	if reactions == nil {
		reactions, _ = m["reactions"].([]any)
	}
	out := make([]map[string]any, 0, len(reactions))
	for _, r := range reactions {
		if rm, ok := r.(map[string]any); ok {
			out = append(out, map[string]any{
				"name":  rm["name"],
				"count": rm["count"],
				"users": rm["users"],
			})
		}
	}
	return map[string]any{"reactions": out}
}

func shapeFileList(raw any) any {
	m, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	files, _ := m["files"].([]any)
	out := make([]map[string]any, 0, len(files))
	for _, f := range files {
		out = append(out, shapeOneFile(f))
	}
	resp := map[string]any{"files": out}
	if paging, ok := m["paging"]; ok {
		resp["paging"] = paging
	}
	return resp
}

func shapeOneFile(in any) map[string]any {
	m, ok := in.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return map[string]any{
		"id":                   m["id"],
		"name":                 m["name"],
		"title":                m["title"],
		"mimetype":             m["mimetype"],
		"filetype":             m["filetype"],
		"size":                 m["size"],
		"user":                 m["user"],
		"created":              m["created"],
		"channels":             m["channels"],
		"is_external":          m["is_external"],
		"url_private_download": m["url_private_download"],
		"permalink":            m["permalink"],
	}
}

// shapeReadFile turns downloaded bytes into an LLM-friendly response:
// UTF-8 text inline when the mimetype is text-like and the bytes are valid
// UTF-8, base64 otherwise (images, PDFs, anything binary).
func shapeReadFile(fileID, name, mimetype string, body []byte) any {
	out := map[string]any{
		"file_id":  fileID,
		"name":     name,
		"mimetype": mimetype,
		"size":     len(body),
	}
	// Text when the mimetype says so, OR when Slack reported no mimetype at
	// all but the bytes are valid UTF-8 (a .txt Slack didn't type). Binary
	// (images, PDFs, invalid UTF-8) falls through to base64.
	textLike := (isTextMimetype(mimetype) || strings.TrimSpace(mimetype) == "") && utf8.Valid(body)
	if textLike {
		out["is_text"] = true
		out["content"] = string(body)
	} else {
		out["is_text"] = false
		out["content_base64"] = base64.StdEncoding.EncodeToString(body)
	}
	return out
}

// isTextMimetype reports whether a mimetype's bytes are safe to return as a
// UTF-8 string. text/* plus a handful of known-text application subtypes.
func isTextMimetype(mt string) bool {
	mt = strings.ToLower(strings.TrimSpace(mt))
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	if strings.HasPrefix(mt, "text/") {
		return true
	}
	switch mt {
	case "application/json",
		"application/xml",
		"application/xhtml+xml",
		"application/javascript",
		"application/x-ndjson",
		"application/x-sh",
		"application/x-yaml",
		"application/yaml",
		"application/csv":
		return true
	}
	return false
}

// ── Permission check ─────────────────────────────────────────────────

// opScopes lists, for each operation, the set of OAuth scopes that
// satisfy it. Inner slice = ANY-OF (at least one scope is enough).
// Outer slice = ALL-OF (every group must have at least one match).
//
// Slack often allows multiple scopes for the same call (e.g. reading
// channel history works with channels:history for public channels OR
// groups:history for private). We treat each parenthesised group as
// ANY-OF so a bot with only public scopes still passes ops that don't
// require private access.
var opScopes = map[string][][]string{
	"list_channels":          {{"channels:read", "groups:read", "im:read", "mpim:read"}},
	"search_channels":        {{"channels:read", "groups:read", "im:read", "mpim:read"}},
	"get_channel_info":       {{"channels:read", "groups:read", "im:read", "mpim:read"}},
	"get_channel_history":    {{"channels:history", "groups:history", "im:history", "mpim:history"}},
	"get_thread_replies":     {{"channels:history", "groups:history", "im:history", "mpim:history"}},
	"list_users":             {{"users:read"}},
	"get_user_info":          {{"users:read"}},
	"get_user_by_email":      {{"users:read", "users:read.email"}},
	"get_permalink":          {{"chat:write"}},
	"send_message":           {{"chat:write"}},
	"send_ephemeral":         {{"chat:write"}},
	"update_message":         {{"chat:write"}},
	"delete_message":         {{"chat:write"}},
	"add_reaction":           {{"reactions:write"}},
	"remove_reaction":        {{"reactions:write"}},
	"get_reactions":          {{"reactions:read"}},
	"list_files":             {{"files:read"}},
	"get_file_info":          {{"files:read"}},
	"read_file":              {{"files:read"}},
	"create_canvas":          {{"canvases:write"}},
	"create_channel_canvas":  {{"canvases:write"}},
	"edit_canvas":            {{"canvases:write"}},
	"lookup_canvas_sections": {{"canvases:read"}},
	"set_canvas_access":      {{"canvases:write"}},
	"upload_file":            {{"files:write"}},
	// Lists. list_lists rides on files.list (Slack has no slackLists.list),
	// so it needs files:read while the rest need the lists:* scopes.
	"list_lists":       {{"files:read"}},
	"get_list":         {{"lists:read"}},
	"list_list_items":  {{"lists:read"}},
	"get_list_item":    {{"lists:read"}},
	"create_list":      {{"lists:write"}},
	"create_list_item": {{"lists:write"}},
	"update_list_item": {{"lists:write"}},
	"delete_list_item": {{"lists:write"}},
	// custom_api_call is deliberately absent: the scopes it needs depend
	// on whichever method the caller names, so any static rule here would
	// either system-disable a working escape hatch or vouch for scopes it
	// does not have. Slack's own missing_scope error is the check instead.
}

// runHealthCheck makes one auth.test call, reads the granted scopes
// from X-OAuth-Scopes, and projects them onto opScopes to build the
// per-operation report the framework reconciles into system_disabled
// flags. Errors from auth.test (invalid token, network failure) abort
// the whole check — we never partially flip flags.
func runHealthCheck(c *connector.Ctx) ([]connector.OpHealth, error) {
	_, header, err := slackGetWithHeaders(c, "auth.test", nil)
	if err != nil {
		return nil, err
	}
	granted := parseScopeHeader(header.Get("X-OAuth-Scopes"))
	grantedSet := make(map[string]struct{}, len(granted))
	for _, s := range granted {
		grantedSet[s] = struct{}{}
	}
	out := make([]connector.OpHealth, 0, len(opScopes))
	for opKey, groups := range opScopes {
		ok, missing := evalScopeRule(groups, grantedSet)
		h := connector.OpHealth{Key: opKey, OK: ok}
		if !ok {
			h.Reason = formatMissingScopes(missing)
		}
		out = append(out, h)
	}
	return out, nil
}

// formatMissingScopes renders the unsatisfied any-of groups into a
// terse human reason — "needs scope: chat:write" for single-scope
// groups, "needs one of: a, b" when a group has multiple alternatives.
func formatMissingScopes(missing [][]string) string {
	if len(missing) == 0 {
		return "permission check failed"
	}
	parts := make([]string, 0, len(missing))
	for _, group := range missing {
		if len(group) == 1 {
			parts = append(parts, group[0])
		} else {
			parts = append(parts, "one of: "+strings.Join(group, ", "))
		}
	}
	return "needs scope: " + strings.Join(parts, "; also ")
}

func parseScopeHeader(h string) []string {
	if h == "" {
		return nil
	}
	parts := strings.Split(h, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// evalScopeRule returns (ok, missingGroups). missingGroups lists the
// any-of groups that were unsatisfied — surfaced to the admin so they
// know exactly which scope to add.
func evalScopeRule(rule [][]string, granted map[string]struct{}) (bool, [][]string) {
	if len(rule) == 0 {
		return true, nil
	}
	missing := make([][]string, 0)
	for _, group := range rule {
		hit := false
		for _, scope := range group {
			if _, ok := granted[scope]; ok {
				hit = true
				break
			}
		}
		if !hit {
			missing = append(missing, group)
		}
	}
	return len(missing) == 0, missing
}

// signedFooterBlock builds the Block Kit context block appended to every
// send_message / update_message — "Sent using <@BotID>".
//
// The bot is the one that OWNS this call's session (c.OwnerBotID()),
// resolved by the framework from the channel registry. This makes the
// footer name the session owner's bot no matter which connector instance
// sends — the whole point: "pick any connector, the 'Sent using' is the
// same". For sessions that aren't channel-backed (cron, UI, REST) there is
// no owner bot, so the footer falls back to the app name.
func signedFooterBlock(c *connector.Ctx) map[string]any {
	botID := c.OwnerBotID()
	var footerText string
	if botID != "" {
		footerText = "Sent using <@" + botID + ">"
	} else {
		footerText = "Sent using *" + appname.Resolve() + "*"
	}
	return map[string]any{
		"type": "context",
		"elements": []any{
			map[string]any{
				"type": "mrkdwn",
				"text": footerText,
			},
		},
	}
}

func cursorFrom(m map[string]any) string {
	rm, ok := m["response_metadata"].(map[string]any)
	if !ok {
		return ""
	}
	if cur, ok := rm["next_cursor"].(string); ok {
		return cur
	}
	return ""
}

// ── Lists shaping ────────────────────────────────────────────────────

// shapeListsList trims a files.list response down to the fields that
// identify a List, dropping the file-centric noise (mimetype, size,
// thumbnails) that never applies to Lists.
func shapeListsList(raw any) map[string]any {
	out := map[string]any{"ok": true, "lists": []any{}}
	m, ok := raw.(map[string]any)
	if !ok {
		return out
	}
	if paging, ok := m["paging"]; ok {
		out["paging"] = paging
	}
	files, _ := m["files"].([]any)
	lists := make([]any, 0, len(files))
	for _, f := range files {
		fm, ok := f.(map[string]any)
		if !ok {
			continue
		}
		entry := map[string]any{}
		for _, k := range []string{"id", "name", "title", "user", "created", "permalink", "channels", "updated"} {
			if v, ok := fm[k]; ok {
				entry[k] = v
			}
		}
		lists = append(lists, entry)
	}
	out["lists"] = lists
	return out
}

// parseJSONArray decodes a caller-supplied JSON array field (schema,
// cells, initial_fields). Slack rejects these silently or with an
// opaque error when they arrive as an object or a bare string, so we
// name the offending field up front.
func parseJSONArray(raw, field string) ([]any, error) {
	var out []any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("%s must be a JSON array: %w", field, err)
	}
	return out, nil
}

// ── Custom API gating ────────────────────────────────────────────────

// normalizeAPIMethod validates a caller-supplied Slack method name.
// Accepts the documented bare form ("conversations.members") and
// tolerates a leading slash; rejects anything carrying a scheme, a
// host, a query string, or path traversal so the method can never
// steer the request off the connector's base URL.
func normalizeAPIMethod(raw string) (string, error) {
	method := strings.TrimSpace(raw)
	if method == "" {
		return "", fmt.Errorf("method is required")
	}
	method = strings.TrimPrefix(method, "/")
	if strings.Contains(method, "://") || strings.ContainsAny(method, "?#") || strings.Contains(method, "..") || strings.Contains(method, "/") {
		return "", fmt.Errorf("method must be a bare Slack API method name such as conversations.members, not a URL or path")
	}
	for _, r := range method {
		isAllowed := r == '.' || r == '_' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !isAllowed {
			return "", fmt.Errorf("method contains an unexpected character %q — Slack method names are letters, digits, dots, and underscores", r)
		}
	}
	return method, nil
}

// checkMethodAllowed enforces the per-instance custom API policy.
// Mode "all" waves everything through; the default "allowlist" mode
// permits only the configured method names, where a trailing * matches
// any method sharing that prefix.
func checkMethodAllowed(c *connector.Ctx, method string) error {
	mode := strings.TrimSpace(c.Cfg("custom_api_mode"))
	if mode == "" {
		mode = "allowlist"
	}
	if mode == "all" {
		return nil
	}
	if mode != "allowlist" {
		return fmt.Errorf("unknown custom_api_mode %q — expected allowlist or all", mode)
	}

	allowed := parseAllowlist(c.Cfg("custom_api_allowlist"))
	if len(allowed) == 0 {
		return fmt.Errorf("custom_api_call is in allowlist mode but the allowlist is empty — add %q to this connector's Custom API allowlist, or switch the mode to 'all'", method)
	}
	for _, pattern := range allowed {
		if matchMethodPattern(pattern, method) {
			return nil
		}
	}
	return fmt.Errorf("method %q is not in this connector's Custom API allowlist (allowed: %s) — add it there, or switch the mode to 'all'", method, strings.Join(allowed, ", "))
}

// parseAllowlist reads the kvlist-backed allowlist config. The widget
// stores a JSON array of single-key rows; a hand-edited comma or
// newline separated string is accepted too so the value stays usable
// when set outside the admin UI.
func parseAllowlist(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var rows []map[string]string
	if err := json.Unmarshal([]byte(raw), &rows); err == nil {
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			for _, v := range row {
				if entry := strings.TrimSpace(v); entry != "" {
					out = append(out, entry)
				}
			}
		}
		return out
	}
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' }) {
		if entry := strings.TrimSpace(part); entry != "" {
			out = append(out, entry)
		}
	}
	return out
}

// matchMethodPattern compares one allowlist entry against a method
// name, case-insensitively. A trailing * makes the entry a prefix rule
// ("admin.*" covers every admin method); everything else is exact.
func matchMethodPattern(pattern, method string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	method = strings.ToLower(method)
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(method, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == method
}

// parseAPIParams decodes the caller's params object. Slack arguments
// are always a flat object, so an array or scalar is a caller mistake
// worth naming. A token key is refused outright — the connector
// attaches its own credential and must stay the only source of auth.
func parseAPIParams(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return nil, fmt.Errorf("params must be a JSON object: %w", err)
	}
	for k := range params {
		if strings.EqualFold(strings.TrimSpace(k), "token") {
			return nil, fmt.Errorf("params must not contain a token — this connector's own credential is attached automatically")
		}
	}
	return params, nil
}

// readShapedSuffixes are the Slack method suffixes that read state and
// are therefore served over GET when http_method is left on auto.
var readShapedSuffixes = []string{".list", ".info", ".history", ".replies", ".members"}

// resolveHTTPVerb turns the http_method input into a concrete verb.
// On auto, read-shaped methods go out as GET and everything else as
// POST — the convention Slack's own docs follow.
func resolveHTTPVerb(choice, method string) string {
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "get":
		return http.MethodGet
	case "post":
		return http.MethodPost
	}
	lower := strings.ToLower(method)
	for _, suffix := range readShapedSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return http.MethodGet
		}
	}
	return http.MethodPost
}

// stringifyParams flattens a params object into the string form the
// GET query builder takes. Nested objects and arrays are re-encoded as
// JSON, which is how Slack expects structured arguments on GET.
func stringifyParams(params map[string]any) map[string]string {
	out := make(map[string]string, len(params))
	for k, v := range params {
		switch t := v.(type) {
		case nil:
			continue
		case string:
			out[k] = t
		case bool:
			out[k] = strconv.FormatBool(t)
		case float64:
			out[k] = strconv.FormatFloat(t, 'f', -1, 64)
		default:
			if encoded, err := json.Marshal(t); err == nil {
				out[k] = string(encoded)
			}
		}
	}
	return out
}
