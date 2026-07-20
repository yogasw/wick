package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/pkg/connector"
)

// LogEntry is one log line with its metadata, flattened for LLM consumption.
type LogEntry struct {
	Timestamp string            `json:"timestamp"` // RFC3339Nano, UTC
	Labels    map[string]string `json:"labels"`
	Line      string            `json:"line"`
}

// QueryResult wraps the entries with a count so the LLM doesn't need to len().
type QueryResult struct {
	Count   int        `json:"count"`
	Entries []LogEntry `json:"entries"`
}

func fetchQueryRange(c *connector.Ctx, p queryParams) (*QueryResult, error) {
	u, err := url.Parse(p.URL)
	if err != nil {
		return nil, fmt.Errorf("build url: %w", err)
	}
	q := u.Query()
	q.Set("query", p.Query)
	q.Set("start", p.Start)
	q.Set("end", p.End)
	q.Set("limit", strconv.Itoa(p.Limit))
	q.Set("direction", p.Direction)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	applyAuth(c, req)
	applyOrgID(c, req)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call loki: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, lokiError(resp.StatusCode, raw)
	}

	var envelope struct {
		Data struct {
			Result []struct {
				Stream map[string]string `json:"stream"`
				Values [][2]string       `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	entries := make([]LogEntry, 0)
	for _, stream := range envelope.Data.Result {
		for _, v := range stream.Values {
			entries = append(entries, LogEntry{
				Timestamp: nanoToRFC3339(v[0]),
				Labels:    stream.Stream,
				Line:      v[1],
			})
		}
	}
	return &QueryResult{Count: len(entries), Entries: entries}, nil
}

func fetchStringList(c *connector.Ctx, rawURL string) ([]string, error) {
	req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	applyAuth(c, req)
	applyOrgID(c, req)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call loki: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, lokiError(resp.StatusCode, raw)
	}

	var envelope struct {
		Data []string `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if envelope.Data == nil {
		return []string{}, nil
	}
	return envelope.Data, nil
}

func applyAuth(c *connector.Ctx, req *http.Request) {
	if strings.EqualFold(c.Cfg("auth_mode"), "basic") {
		creds := c.Cfg("username") + ":" + c.Cfg("password")
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(creds)))
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.Cfg("token"))
}

func applyOrgID(c *connector.Ctx, req *http.Request) {
	if org := strings.TrimSpace(c.Cfg("org_id")); org != "" {
		req.Header.Set("X-Grafana-Org-Id", org)
	}
}

func lokiError(status int, body []byte) error {
	var env struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &env) == nil && env.Error != "" {
		return fmt.Errorf("loki %d: %s", status, env.Error)
	}
	if msg := strings.TrimSpace(string(body)); msg != "" {
		return fmt.Errorf("loki %d: %s", status, msg)
	}
	return fmt.Errorf("loki %d", status)
}

func nanoToRFC3339(ns string) string {
	n, err := strconv.ParseInt(ns, 10, 64)
	if err != nil {
		return ns
	}
	return time.Unix(0, n).UTC().Format(time.RFC3339Nano)
}

// pickerItem is one selectable row in an html= picker: Value is stored on click
// (via data-op="__select"), Label + Sub are shown. Shared by the org and
// datasource pickers.
type pickerItem struct {
	Value string
	Label string
	Sub   string
}

// grafanaDatasource is the subset of Grafana's GET /api/datasources rows the
// picker needs: the UID we store, a human name to show, and the type used to
// keep only Loki datasources.
type grafanaDatasource struct {
	UID  string `json:"uid"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// grafanaUserOrg is the subset of GET /api/user/orgs rows the org picker needs.
type grafanaUserOrg struct {
	OrgID int64  `json:"orgId"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

// grafanaHealth is the GET /api/health payload: version + db status.
type grafanaHealth struct {
	Version  string `json:"version"`
	Database string `json:"database"`
	Commit   string `json:"commit"`
}

// fetchStatusHTML probes GET /api/health and renders a read-only status card
// with the Grafana version + reachability, so the operator can confirm the
// connection while filling the form. Sends auth (some Grafana setups gate
// /api/health) but not the org header — it's instance-wide.
func fetchStatusHTML(c *connector.Ctx) (map[string]any, error) {
	raw, notice, err := grafanaGET(c, "/api/health", false)
	if err != nil {
		return nil, err
	}
	if notice != "" {
		return map[string]any{"html": statusCard(false, notice)}, nil
	}

	var h grafanaHealth
	if err := json.Unmarshal(raw, &h); err != nil {
		return map[string]any{"html": statusCard(true, "Reachable, but couldn't read version.")}, nil
	}

	parts := []string{"Reachable"}
	if h.Version != "" {
		parts = append(parts, "Grafana v"+html.EscapeString(h.Version))
	}
	if h.Database != "" {
		parts = append(parts, "db "+html.EscapeString(h.Database))
	}
	return map[string]any{"html": statusCard(true, strings.Join(parts, " · "))}, nil
}

// statusCard renders the connection-status widget: green when reachable, red
// otherwise, with a single-line detail (version + db, or the error hint).
func statusCard(ok bool, detail string) string {
	dot := `<span class="h-2 w-2 rounded-full bg-neg-400"></span>`
	ring := "border-neg-300 bg-neg-100 dark:bg-navy-800"
	text := "text-neg-400"
	if ok {
		dot = `<span class="h-2 w-2 rounded-full bg-pos-400"></span>`
		ring = "border-pos-300 bg-pos-100 dark:bg-navy-800"
		text = "text-pos-400"
	}
	return `<div class="flex items-center gap-2 rounded-lg border ` + ring + ` px-4 py-2.5">` +
		dot +
		`<span class="text-xs font-medium ` + text + `">` + detail + `</span>` +
		`</div>`
}

// fetchOrgsHTML lists the Grafana orgs the configured auth can access
// (GET /api/user/orgs) and renders them as the org picker's markup. Value stored
// is the numeric orgId (matching the X-Grafana-Org-Id header the other ops send).
// Does NOT send the org header — the list itself is what picks the org.
func fetchOrgsHTML(c *connector.Ctx) (map[string]any, error) {
	raw, notice, err := grafanaGET(c, "/api/user/orgs", false)
	if err != nil {
		return nil, err
	}
	if notice != "" {
		return map[string]any{"html": pickerNotice(notice)}, nil
	}

	var orgs []grafanaUserOrg
	if err := json.Unmarshal(raw, &orgs); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	items := make([]pickerItem, 0, len(orgs))
	for _, o := range orgs {
		id := strconv.FormatInt(o.OrgID, 10)
		items = append(items, pickerItem{Value: id, Label: o.Name, Sub: "org " + id})
	}
	return renderPicker(items, strings.TrimSpace(c.Input("browser")), "No Grafana orgs accessible with this auth."), nil
}

// fetchDatasourcesHTML lists the Grafana Loki datasources (scoped to the
// configured org header) and renders them as the datasource picker's markup.
// Value stored is the datasource UID used directly in resourceURL().
func fetchDatasourcesHTML(c *connector.Ctx) (map[string]any, error) {
	raw, notice, err := grafanaGET(c, "/api/datasources", true)
	if err != nil {
		return nil, err
	}
	if notice != "" {
		return map[string]any{"html": pickerNotice(notice)}, nil
	}

	var all []grafanaDatasource
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	selected := strings.TrimSpace(c.Input("browser"))
	items := make([]pickerItem, 0, len(all))
	for _, ds := range all {
		if !strings.EqualFold(ds.Type, "loki") {
			continue
		}
		items = append(items, pickerItem{Value: ds.UID, Label: ds.Name, Sub: ds.UID})
	}
	return renderPicker(items, selected, "No Loki datasources found in this Grafana org."), nil
}

// grafanaGET performs an authenticated GET against a Grafana API path. It
// returns the body on success; a non-empty notice string (for the picker to
// render inline) when base_url is missing or Grafana returns non-2xx; and a
// hard error only for build/transport failures the form can't recover from.
// withOrg controls whether the X-Grafana-Org-Id header is sent (org listing
// must not send it — it's what selects the org).
func grafanaGET(c *connector.Ctx, path string, withOrg bool) (body []byte, notice string, err error) {
	base := strings.TrimRight(strings.TrimSpace(c.Cfg("base_url")), "/")
	if base == "" {
		return nil, "Fill the Grafana base URL first, then reopen this list.", nil
	}
	req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, base+path, nil)
	if err != nil {
		return nil, "", fmt.Errorf("build request: %w", err)
	}
	applyAuth(c, req)
	if withOrg {
		applyOrgID(c, req)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("call grafana: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "Couldn't load: " + html.EscapeString(lokiError(resp.StatusCode, raw).Error()), nil
	}
	return raw, "", nil
}

// renderPicker builds the shared picker markup: one selectable card per item,
// the one matching `selected` highlighted. emptyMsg renders when there are no
// items. A single-item list is rendered normally — HtmlField auto-selects it.
func renderPicker(items []pickerItem, selected, emptyMsg string) map[string]any {
	if len(items) == 0 {
		return map[string]any{"html": pickerNotice(emptyMsg)}
	}
	rows := make([]string, 0, len(items))
	for _, it := range items {
		rows = append(rows, renderPickerRow(it, it.Value == selected))
	}
	return map[string]any{"html": `<div class="flex flex-col gap-2">` + strings.Join(rows, "") + `</div>`}
}

// renderPickerRow is one selectable card. The selected row gets a green ring +
// "selected" badge; the rest read "click to select".
func renderPickerRow(it pickerItem, selected bool) string {
	label := html.EscapeString(it.Label)
	value := html.EscapeString(it.Value)
	sub := html.EscapeString(it.Sub)

	ring := "border border-white-400 dark:border-navy-600 hover:border-green-400"
	if selected {
		ring = "border-2 border-green-500 ring-1 ring-green-200 dark:ring-green-800"
	}

	right := `<span class="text-[11px] text-black-700 dark:text-black-600">click to select</span>`
	if selected {
		right = `<span class="rounded-full bg-green-50 dark:bg-green-900 px-2 py-0.5 text-[11px] font-semibold text-green-700 dark:text-green-300">selected</span>`
	}

	return `<div class="flex items-center gap-3 rounded-lg cursor-pointer ` + ring +
		` px-4 py-2.5 bg-white-100 dark:bg-navy-800 transition-colors" data-op="__select" data-arg="` + value + `">` +
		`<span class="font-semibold text-sm text-black-900 dark:text-white-100">` + label + `</span>` +
		`<span class="font-mono text-[11px] text-black-700 dark:text-black-600">` + sub + `</span>` +
		`<span class="ml-auto">` + right + `</span>` +
		`</div>`
}

// pickerNotice wraps a short message in the same card shell the rows use, so an
// empty/error state doesn't look broken next to a populated list.
func pickerNotice(msg string) string {
	return `<div class="rounded-lg border border-white-400 dark:border-navy-600 px-4 py-2.5 bg-white-100 dark:bg-navy-800 text-xs text-black-700 dark:text-black-600">` + msg + `</div>`
}
