package slack

import (
	"encoding/json"
	"fmt"
	"strings"

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
	"list_channels":      {{"channels:read", "groups:read", "im:read", "mpim:read"}},
	"search_channels":    {{"channels:read", "groups:read", "im:read", "mpim:read"}},
	"get_channel_info":   {{"channels:read", "groups:read", "im:read", "mpim:read"}},
	"get_channel_history": {{"channels:history", "groups:history", "im:history", "mpim:history"}},
	"get_thread_replies":  {{"channels:history", "groups:history", "im:history", "mpim:history"}},
	"list_users":         {{"users:read"}},
	"get_user_info":      {{"users:read"}},
	"get_user_by_email":  {{"users:read", "users:read.email"}},
	"get_permalink":      {{"chat:write"}},
	"send_message":       {{"chat:write"}},
	"send_ephemeral":     {{"chat:write"}},
	"update_message":     {{"chat:write"}},
	"delete_message":     {{"chat:write"}},
	"add_reaction":       {{"reactions:write"}},
	"remove_reaction":    {{"reactions:write"}},
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
