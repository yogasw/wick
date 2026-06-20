// Package googleworkspace — repo.go: Outbound HTTP calls to the Google Drive REST API v3.
//
// Purpose: All network I/O for the Google Drive connector. Implements lazy token
// refresh (retry on 401 with refresh_token) via doWithRefresh.
//
// Caller:   connector.go handler functions
// Dependencies: connector.Ctx (HTTP client + config reads), service.go types
// Side Effects: outbound HTTPS calls to googleapis.com
package googleworkspace

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
			return nil, fmt.Errorf("googleapis %d: %s", resp.StatusCode, string(body))
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

// buildJSONRequest builds an authorized HTTP request with a JSON body. Shared by
// repo_sheets, repo_docs, repo_slides, repo_calendar, repo_meet. The token comes
// from the doWithRefresh closure so the lazy-refresh retry re-signs the request.
func buildJSONRequest(c *connector.Ctx, method, fullURL, token string, body any) (*http.Request, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}
	req, err := http.NewRequestWithContext(c.Context(), method, fullURL, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// createWorkspaceFile creates a Google Workspace file (Doc, Sheet, or Slides) using the Drive API.
// If content is non-empty, it is imported via multipart upload with the given contentMimeType.
// Returns WorkspaceFileResult.
func createWorkspaceFile(c *connector.Ctx, name, workspaceMime, folderID, content, contentMime string) (WorkspaceFileResult, error) {
	var body []byte
	var err error
	if content != "" {
		body, err = uploadMultipartWorkspace(c, name, workspaceMime, folderID, content, contentMime)
	} else {
		meta := map[string]any{"name": name, "mimeType": workspaceMime}
		if folderID != "" {
			meta["parents"] = []string{folderID}
		}
		body, err = drivePost(c, "/files?fields=id,name,webViewLink", meta)
	}
	if err != nil {
		return WorkspaceFileResult{}, err
	}
	var f driveFile
	if err := json.Unmarshal(body, &f); err != nil {
		return WorkspaceFileResult{}, fmt.Errorf("parse workspace file response: %w", err)
	}
	return WorkspaceFileResult{ID: f.ID, Name: f.Name, WebViewLink: f.WebViewLink}, nil
}

// uploadMultipartWorkspace uploads content with a target Google Workspace MIME type,
// causing Drive to import it as a native workspace file (Doc, Sheet, etc).
func uploadMultipartWorkspace(c *connector.Ctx, name, workspaceMime, folderID, content, contentMime string) ([]byte, error) {
	meta := map[string]any{"name": name, "mimeType": workspaceMime}
	if folderID != "" {
		meta["parents"] = []string{folderID}
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("marshal workspace metadata: %w", err)
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
	contentHeader.Set("Content-Type", contentMime)
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
			"https://www.googleapis.com/upload/drive/v3/files?uploadType=multipart&fields=id,name,webViewLink",
			bytes.NewReader(bufBytes))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "multipart/related; boundary="+boundary)
		return req, nil
	})
}

// createWorkspaceFileAndSetTitle creates a blank Google Slides presentation and
// optionally sets the first slide's title via the Slides API batchUpdate.
// The title update is best-effort: if it fails, the created file is still returned.
func createWorkspaceFileAndSetTitle(c *connector.Ctx, name, folderID, firstSlideText string) (WorkspaceFileResult, error) {
	result, err := createWorkspaceFile(c, name, "application/vnd.google-apps.presentation", folderID, "", "")
	if err != nil {
		return WorkspaceFileResult{}, err
	}
	if firstSlideText == "" {
		return result, nil
	}
	presBody, err := slidesGet(c, "/"+result.ID+"?fields=slides")
	if err != nil {
		return result, nil
	}
	var pres struct {
		Slides []struct {
			PageElements []struct {
				ObjectID string `json:"objectId"`
				Shape    *struct {
					Placeholder *struct{ Type string `json:"type"` } `json:"placeholder"`
				} `json:"shape"`
			} `json:"pageElements"`
		} `json:"slides"`
	}
	if err := json.Unmarshal(presBody, &pres); err != nil || len(pres.Slides) == 0 {
		return result, nil
	}
	var titleObjectID string
	for _, el := range pres.Slides[0].PageElements {
		if el.Shape != nil && el.Shape.Placeholder != nil && el.Shape.Placeholder.Type == "TITLE" {
			titleObjectID = el.ObjectID
			break
		}
	}
	if titleObjectID == "" {
		return result, nil
	}
	updateReq := map[string]any{
		"requests": []any{
			map[string]any{
				"insertText": map[string]any{
					"objectId":       titleObjectID,
					"insertionIndex": 0,
					"text":           firstSlideText,
				},
			},
		},
	}
	_, _ = slidesPost(c, "/"+result.ID+":batchUpdate", updateReq)
	return result, nil
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
