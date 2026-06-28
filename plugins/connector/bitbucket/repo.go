package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

func fetchJSON(c *connector.Ctx, p requestParams) (any, error) {
	req, err := http.NewRequestWithContext(c.Context(), p.Method, p.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	applyAuth(c, req)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call bitbucket: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, bitbucketError(resp.StatusCode, raw)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{"ok": true}, nil
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, truncate(string(raw), 400))
	}
	return decoded, nil
}

func fetchText(c *connector.Ctx, p requestParams) (any, error) {
	req, err := http.NewRequestWithContext(c.Context(), p.Method, p.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	applyAuth(c, req)
	req.Header.Set("Accept", "text/plain")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call bitbucket: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, bitbucketError(resp.StatusCode, raw)
	}
	return map[string]any{
		"content_type": resp.Header.Get("Content-Type"),
		"diff":         string(raw),
	}, nil
}

func sendJSON(c *connector.Ctx, p requestParams, body map[string]any) (any, error) {
	rawBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(c.Context(), p.Method, p.URL, bytes.NewReader(rawBody))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	applyAuth(c, req)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call bitbucket: %w", err)
	}
	defer resp.Body.Close()
	return decodeMutationResponse(resp)
}

func sendMultipart(c *connector.Ctx, p requestParams, form fileCommitForm) (any, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("message", form.CommitMessage); err != nil {
		return nil, fmt.Errorf("write message field: %w", err)
	}
	if err := writer.WriteField("branch", form.Branch); err != nil {
		return nil, fmt.Errorf("write branch field: %w", err)
	}
	part, err := writer.CreateFormFile(form.Path, form.Path)
	if err != nil {
		return nil, fmt.Errorf("write file field: %w", err)
	}
	if _, err := part.Write([]byte(form.Content)); err != nil {
		return nil, fmt.Errorf("write file content: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart body: %w", err)
	}

	req, err := http.NewRequestWithContext(c.Context(), p.Method, p.URL, &body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	applyAuth(c, req)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call bitbucket: %w", err)
	}
	defer resp.Body.Close()
	return decodeMutationResponse(resp)
}

func decodeMutationResponse(resp *http.Response) (any, error) {
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, bitbucketError(resp.StatusCode, raw)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{"ok": true, "status": resp.StatusCode}, nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, truncate(string(raw), 400))
	}
	return decoded, nil
}

func applyAuth(c *connector.Ctx, req *http.Request) {
	creds := c.Cfg("email") + ":" + c.Cfg("api_token")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(creds)))
}

func bitbucketError(status int, body []byte) error {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Detail  string `json:"detail"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		msg := strings.TrimSpace(envelope.Error.Message)
		if detail := strings.TrimSpace(envelope.Error.Detail); detail != "" {
			if msg != "" {
				msg += ": "
			}
			msg += detail
		}
		if msg != "" {
			return fmt.Errorf("bitbucket %d: %s", status, msg)
		}
	}
	if msg := strings.TrimSpace(string(body)); msg != "" {
		return fmt.Errorf("bitbucket %d: %s", status, truncate(msg, 800))
	}
	return fmt.Errorf("bitbucket %d", status)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
