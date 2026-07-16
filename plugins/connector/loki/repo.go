package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
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
