// Package googledrive — repo.go: Outbound HTTP calls to the Google Drive REST API v3.
//
// Purpose: All network I/O for the Google Drive connector. Implements lazy token
// refresh (retry on 401 with refresh_token) via doWithRefresh.
//
// Caller:   connector.go handler functions
// Dependencies: connector.Ctx (HTTP client + config reads), service.go types
// Side Effects: outbound HTTPS calls to googleapis.com
package googledrive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

const driveBaseURL = "https://www.googleapis.com/drive/v3"

// driveGet performs a GET request to the Drive API with lazy token refresh.
func driveGet(c *connector.Ctx, path string, params url.Values) ([]byte, error) {
	return doWithRefresh(c, func(token string) (*http.Request, error) {
		u := driveBaseURL + path
		if len(params) > 0 {
			u += "?" + params.Encode()
		}
		req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return req, nil
	})
}

// drivePatch performs a PATCH request to the Drive API with lazy token refresh.
func drivePatch(c *connector.Ctx, path string, body any) ([]byte, error) {
	return doWithRefresh(c, func(token string) (*http.Request, error) {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal patch body: %w", err)
		}
		req, err := http.NewRequestWithContext(c.Context(), http.MethodPatch, driveBaseURL+path, bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
}

// drivePost performs a POST request to the Drive API with lazy token refresh.
func drivePost(c *connector.Ctx, path string, body any) ([]byte, error) {
	return doWithRefresh(c, func(token string) (*http.Request, error) {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal post body: %w", err)
		}
		req, err := http.NewRequestWithContext(c.Context(), http.MethodPost, driveBaseURL+path, bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
}

// doWithRefresh executes a Drive API request with a lazy token refresh on 401.
// Attempts the call with the stored user_token; on 401, refreshes via refresh_token
// and retries once. Returns the response body on success.
func doWithRefresh(c *connector.Ctx, buildReq func(token string) (*http.Request, error)) ([]byte, error) {
	token := c.Cfg("user_token")
	for attempt := 0; attempt < 2; attempt++ {
		req, err := buildReq(token)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request: %w", err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			token, err = refreshAccessToken(c)
			if err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode >= 300 {
			return nil, fmt.Errorf("drive API %d: %s", resp.StatusCode, string(body))
		}
		return body, nil
	}
	return nil, fmt.Errorf("unauthorized after token refresh")
}

// refreshAccessToken uses the stored refresh_token to obtain a new access_token
// from Google. The new token is used only for the current call (not persisted).
func refreshAccessToken(c *connector.Ctx) (string, error) {
	refreshToken := c.Cfg("refresh_token")
	if refreshToken == "" {
		return "", fmt.Errorf("Google Drive access token expired. Reconnect via Manager → Connectors → Google Drive → Connect Account")
	}
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)
	data.Set("client_id", c.Cfg("client_id"))
	data.Set("client_secret", c.Cfg("client_secret"))

	req, err := http.NewRequestWithContext(c.Context(), http.MethodPost,
		"https://oauth2.googleapis.com/token", strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode refresh response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("refresh error: %s: %s", result.Error, result.ErrorDesc)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("empty access_token in refresh response")
	}
	return result.AccessToken, nil
}

// probeTokenScopes calls Google tokeninfo to get the granted OAuth scopes
// for the current user_token. Used by HealthCheck.
func probeTokenScopes(c *connector.Ctx) (string, error) {
	params := url.Values{}
	params.Set("access_token", c.Cfg("user_token"))
	req, err := http.NewRequestWithContext(c.Context(), http.MethodGet,
		"https://www.googleapis.com/oauth2/v1/tokeninfo?"+params.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("build tokeninfo request: %w", err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("tokeninfo request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Scope string `json:"scope"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode tokeninfo: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("tokeninfo error: %s", result.Error)
	}
	return result.Scope, nil
}

// exportMIME maps Google Workspace MIME types to plain-text export formats.
var exportMIME = map[string]string{
	"application/vnd.google-apps.document":     "text/plain",
	"application/vnd.google-apps.spreadsheet":  "text/csv",
	"application/vnd.google-apps.presentation": "text/plain",
}

// fetchFileContent downloads a file's text content. Google Workspace files
// are exported as plain text / CSV; binary files are read raw (first 100KB).
func fetchFileContent(c *connector.Ctx, fileID string) (any, error) {
	infoBody, err := driveGet(c, "/files/"+fileID, buildDetailParams())
	if err != nil {
		return nil, err
	}
	var f driveFile
	if err := json.Unmarshal(infoBody, &f); err != nil {
		return nil, fmt.Errorf("parse file info: %w", err)
	}

	var content string
	if exportType, ok := exportMIME[f.MimeType]; ok {
		params := url.Values{}
		params.Set("mimeType", exportType)
		body, err := driveGet(c, "/files/"+fileID+"/export", params)
		if err != nil {
			return nil, fmt.Errorf("export file: %w", err)
		}
		content = string(body)
	} else {
		params := url.Values{}
		params.Set("alt", "media")
		body, err := driveGet(c, "/files/"+fileID, params)
		if err != nil {
			return nil, fmt.Errorf("download file: %w", err)
		}
		const maxBytes = 100 * 1024
		if len(body) > maxBytes {
			body = body[:maxBytes]
		}
		content = string(body)
	}

	return FileContent{ID: f.ID, Name: f.Name, MimeType: f.MimeType, Content: content}, nil
}

// createDriveFolder creates a new folder and returns its FileItem.
func createDriveFolder(c *connector.Ctx, name, parentID string) (FileItem, error) {
	meta := map[string]any{
		"name":     name,
		"mimeType": "application/vnd.google-apps.folder",
	}
	if parentID != "" {
		meta["parents"] = []string{parentID}
	}
	body, err := drivePost(c, "/files?fields="+driveFileFields, meta)
	if err != nil {
		return FileItem{}, err
	}
	var f driveFile
	if err := json.Unmarshal(body, &f); err != nil {
		return FileItem{}, fmt.Errorf("parse folder response: %w", err)
	}
	return shapeFileItem(f), nil
}

// trashFile moves a file to the trash by patching trashed=true.
func trashFile(c *connector.Ctx, fileID string) (any, error) {
	body, err := drivePatch(c, "/files/"+fileID, map[string]bool{"trashed": true})
	if err != nil {
		return nil, err
	}
	var result struct {
		ID      string `json:"id"`
		Trashed bool   `json:"trashed"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse trash response: %w", err)
	}
	return result, nil
}

// createPermission grants access to a file for a user by email.
func createPermission(c *connector.Ctx, fileID, email, role string) (any, error) {
	permission := map[string]string{
		"role":         role,
		"type":         "user",
		"emailAddress": email,
	}
	body, err := drivePost(c, "/files/"+fileID+"/permissions?fields=id,role,emailAddress", permission)
	if err != nil {
		return nil, err
	}
	var result struct {
		ID           string `json:"id"`
		Role         string `json:"role"`
		EmailAddress string `json:"emailAddress"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse permission response: %w", err)
	}
	return map[string]string{
		"file_id":       fileID,
		"email":         email,
		"role":          role,
		"permission_id": result.ID,
	}, nil
}

// uploadMultipart uploads file content with metadata using multipart/related.
func uploadMultipart(c *connector.Ctx, name, content, folderID, mimeType string) ([]byte, error) {
	if mimeType == "" {
		mimeType = "text/plain"
	}
	meta := map[string]any{"name": name}
	if folderID != "" {
		meta["parents"] = []string{folderID}
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("marshal upload metadata: %w", err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	metaHeader := textproto.MIMEHeader{}
	metaHeader.Set("Content-Type", "application/json; charset=UTF-8")
	metaPart, err := mw.CreatePart(metaHeader)
	if err != nil {
		return nil, fmt.Errorf("create metadata part: %w", err)
	}
	if _, err := metaPart.Write(metaJSON); err != nil {
		return nil, fmt.Errorf("write metadata part: %w", err)
	}

	contentHeader := textproto.MIMEHeader{}
	contentHeader.Set("Content-Type", mimeType)
	contentPart, err := mw.CreatePart(contentHeader)
	if err != nil {
		return nil, fmt.Errorf("create content part: %w", err)
	}
	if _, err := contentPart.Write([]byte(content)); err != nil {
		return nil, fmt.Errorf("write content part: %w", err)
	}
	mw.Close()

	boundary := mw.Boundary()
	bufBytes := buf.Bytes()

	return doWithRefresh(c, func(token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(c.Context(), http.MethodPost,
			"https://www.googleapis.com/upload/drive/v3/files?uploadType=multipart&fields="+driveFileFields,
			bytes.NewReader(bufBytes))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "multipart/related; boundary="+boundary)
		return req, nil
	})
}
