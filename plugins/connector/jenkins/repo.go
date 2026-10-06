package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

type jenkinsJob struct {
	Name string       `json:"name"`
	URL  string       `json:"url"`
	Jobs []jenkinsJob `json:"jobs"`
}

type jobsEnvelope struct {
	Jobs []jenkinsJob `json:"jobs"`
}

func fetchJobs(c *connector.Ctx, p listJobsParams) (*ListJobsResult, error) {
	req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, p.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	applyAuth(c, req)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call jenkins: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, jenkinsError(resp.StatusCode, raw)
	}

	var envelope jobsEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	jobs := flattenJobs(envelope.Jobs, p.Search, p.Limit)
	return &ListJobsResult{Count: len(jobs), Jobs: jobs}, nil
}

func fetchConfig(c *connector.Ctx, p getConfigParams) (*ConfigResult, error) {
	req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, p.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	applyAuth(c, req)
	req.Header.Set("Accept", "application/xml,text/xml")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call jenkins: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, jenkinsError(resp.StatusCode, raw)
	}

	result := parseConfig(p.Name, raw)
	return &result, nil
}

// fetchCrumb returns the CSRF crumb header name and value. Jenkins without
// CSRF protection (or with API-token auth) answers 404; that is not an error.
func fetchCrumb(c *connector.Ctx, base string) (string, string) {
	req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, base+"/crumbIssuer/api/json", nil)
	if err != nil {
		return "", ""
	}
	applyAuth(c, req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ""
	}
	var crumb struct {
		Field string `json:"crumbRequestField"`
		Value string `json:"crumb"`
	}
	raw, _ := io.ReadAll(resp.Body)
	if json.Unmarshal(raw, &crumb) != nil {
		return "", ""
	}
	return crumb.Field, crumb.Value
}

func triggerBuild(c *connector.Ctx, p buildJobParams) (*BuildResult, error) {
	var body io.Reader
	if len(p.Params) > 0 {
		body = strings.NewReader(p.Params.Encode())
	}
	req, err := http.NewRequestWithContext(c.Context(), http.MethodPost, p.URL, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	applyAuth(c, req)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if field, value := fetchCrumb(c, strings.TrimRight(strings.TrimSpace(c.Cfg("base_url")), "/")); field != "" {
		req.Header.Set(field, value)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call jenkins: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, jenkinsError(resp.StatusCode, raw)
	}
	return &BuildResult{Name: p.Name, Triggered: true, QueueURL: resp.Header.Get("Location")}, nil
}

func fetchBuild(c *connector.Ctx, p getBuildParams) (*BuildStatus, error) {
	req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, p.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	applyAuth(c, req)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call jenkins: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, jenkinsError(resp.StatusCode, raw)
	}
	var b struct {
		Number    int    `json:"number"`
		Building  bool   `json:"building"`
		Result    string `json:"result"`
		Duration  int64  `json:"duration"`
		Timestamp int64  `json:"timestamp"`
		URL       string `json:"url"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &BuildStatus{Name: p.Name, Number: b.Number, Building: b.Building, Result: b.Result,
		Duration: b.Duration, Timestamp: b.Timestamp, URL: b.URL}, nil
}

func applyAuth(c *connector.Ctx, req *http.Request) {
	creds := c.Cfg("username") + ":" + c.Cfg("password")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(creds)))
}

func jenkinsError(status int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		return fmt.Errorf("jenkins %d", status)
	}
	return fmt.Errorf("jenkins %d: %s", status, truncate(msg, 500))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
