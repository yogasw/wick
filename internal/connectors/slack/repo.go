package slack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// slackGet calls a Slack Web API method with query-string params.
func slackGet(c *connector.Ctx, method string, form map[string]string) (any, error) {
	body, _, err := slackGetWithHeaders(c, method, form)
	return body, err
}

// slackGetWithHeaders is identical to slackGet but also returns the
// response headers. Used by auth.test to read X-OAuth-Scopes.
func slackGetWithHeaders(c *connector.Ctx, method string, form map[string]string) (any, http.Header, error) {
	u := buildURL(c, method)
	if len(form) > 0 {
		q := url.Values{}
		for k, v := range form {
			if v != "" {
				q.Set(k, v)
			}
		}
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	return doSlack(c, req, method)
}

// slackPost calls a Slack Web API method with a JSON body. Slack
// accepts JSON for all the methods we use here so long as the
// Content-Type header is set correctly.
func slackPost(c *connector.Ctx, method string, body map[string]any) (any, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}
	req, err := http.NewRequestWithContext(c.Context(), http.MethodPost, buildURL(c, method), bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, _, err := doSlack(c, req, method)
	return resp, err
}

// slackPostMultipart implements the Slack v2 three-step file upload flow:
// 1. POST files.getUploadURLExternal → upload_url + file_id
// 2. POST multipart/form-data to upload_url with file bytes
// 3. POST files.completeUploadExternal → share to channel
// Returns the decoded completeUploadExternal response.
func slackPostMultipart(c *connector.Ctx, filename string, content []byte, title, channelID, threadTS, initialComment string) (any, error) {
	// Step 1: get upload URL
	step1Resp, err := slackPost(c, "files.getUploadURLExternal", map[string]any{
		"filename": filename,
		"length":   len(content),
	})
	if err != nil {
		return nil, fmt.Errorf("get upload URL: %w", err)
	}
	step1Map, _ := step1Resp.(map[string]any)
	uploadURL, _ := step1Map["upload_url"].(string)
	fileID, _ := step1Map["file_id"].(string)
	if uploadURL == "" || fileID == "" {
		return nil, fmt.Errorf("get upload URL: missing upload_url or file_id in response")
	}

	// Step 2: upload file bytes as multipart/form-data
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := fw.Write(content); err != nil {
		return nil, fmt.Errorf("write file content: %w", err)
	}
	mw.Close()

	token, err := pickToken(c)
	if err != nil {
		return nil, err
	}
	uploadReq, err := http.NewRequestWithContext(c.Context(), http.MethodPost, uploadURL, &buf)
	if err != nil {
		return nil, fmt.Errorf("build upload request: %w", err)
	}
	uploadReq.Header.Set("Authorization", "Bearer "+token)
	uploadReq.Header.Set("Content-Type", mw.FormDataContentType())
	uploadResp, err := c.HTTP.Do(uploadReq)
	if err != nil {
		return nil, fmt.Errorf("upload file content: %w", err)
	}
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode < 200 || uploadResp.StatusCode >= 300 {
		return nil, fmt.Errorf("upload file content: HTTP %d", uploadResp.StatusCode)
	}

	// Step 3: complete upload and share to channel
	completeBody := map[string]any{
		"files": []map[string]any{{"id": fileID, "title": title}},
	}
	if channelID != "" {
		completeBody["channel_id"] = channelID
	}
	if threadTS != "" {
		completeBody["thread_ts"] = threadTS
	}
	if initialComment != "" {
		completeBody["initial_comment"] = initialComment
	}
	result, err := slackPost(c, "files.completeUploadExternal", completeBody)
	if err != nil {
		return nil, fmt.Errorf("complete upload: %w", err)
	}
	return result, nil
}

// slackDownload fetches raw bytes from a Slack url_private / url_private_download
// URL using the connector's token as a Bearer header. This is the step
// files.info cannot do for you: the download URLs are auth-gated, and a
// tokenless GET silently returns Slack's HTML sign-in page instead of the
// file. Enforces maxBytes and treats an HTML body on a non-file mimetype as
// the tell-tale "bot can't see this file" case.
func slackDownload(c *connector.Ctx, downloadURL string, maxBytes int) ([]byte, error) {
	token, err := pickToken(c)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build download request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download file: HTTP %d", resp.StatusCode)
	}

	// Read one byte past the cap so we can tell "exactly at cap" from "over".
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("read file body: %w", err)
	}
	if len(body) > maxBytes {
		return nil, fmt.Errorf("file exceeds max_bytes (%d) — raise max_bytes or fetch it another way", maxBytes)
	}

	// Slack answers an unauthorized download with 200 + an HTML login page
	// rather than an error. Detect that so the caller gets a clear reason
	// instead of base64'd HTML.
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(ct)), "text/html") &&
		bytes.Contains(bytes.ToLower(body), []byte("<!doctype html")) {
		return nil, fmt.Errorf("download returned an HTML page, not the file — the bot is not a member of any channel this file was shared to (needs files:read + channel access)")
	}
	return body, nil
}

// doSlack adds auth, dispatches, and decodes a Slack Web API response.
// Slack always returns HTTP 200 — success is signalled by `ok: true` in
// the body. Non-2xx is therefore always a transport/infra failure.
// Returns the decoded body and the response headers (callers that need
// X-OAuth-Scopes inspect the header set).
func doSlack(c *connector.Ctx, req *http.Request, method string) (any, http.Header, error) {
	token, err := pickToken(c)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("slack %s: %w", method, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.Header, fmt.Errorf("slack %s HTTP %d: %s", method, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, resp.Header, fmt.Errorf("slack %s: empty response", method)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, resp.Header, fmt.Errorf("decode response: %w", err)
	}

	if ok, _ := decoded["ok"].(bool); !ok {
		slackErr, _ := decoded["error"].(string)
		if slackErr == "" {
			slackErr = "unknown_error"
		}
		if warn, _ := decoded["warning"].(string); warn != "" {
			return nil, resp.Header, fmt.Errorf("slack %s: %s (warning: %s)", method, slackErr, warn)
		}
		return nil, resp.Header, fmt.Errorf("slack %s: %s", method, slackErr)
	}
	return decoded, resp.Header, nil
}
