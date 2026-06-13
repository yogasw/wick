// Package googleworkspace — service.go: Pure-Go types, validators, URL builders, and response shapers.
//
// Purpose: Provides the stable output types for all 8 operations, scope validation
// logic for HealthCheck, and URL parameter builders for the repo layer.
//
// Caller:   connector.go handlers (validation), repo.go (param builders),
//           connector.go HealthCheck (scope eval)
// Dependencies: standard library only (encoding/json, fmt, net/url, strings)
// Side Effects: none (pure functions + type definitions)
package googleworkspace

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// FileItem is the standard response shape for file listing operations.
type FileItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	MimeType     string `json:"mimeType"`
	ModifiedTime string `json:"modifiedTime"`
	WebViewLink  string `json:"webViewLink"`
	Size         string `json:"size"`
}

// FileDetail is the extended response shape for get_file_info.
type FileDetail struct {
	FileItem
	OwnerEmail string   `json:"ownerEmail"`
	Shared     bool     `json:"shared"`
	Parents    []string `json:"parents"`
}

// FileContent is the response shape for get_file_content.
type FileContent struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Content  string `json:"content"`
}

// driveFile is the raw structure returned by the Drive API files resource.
type driveFile struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	MimeType     string   `json:"mimeType"`
	ModifiedTime string   `json:"modifiedTime"`
	WebViewLink  string   `json:"webViewLink"`
	Size         string   `json:"size"`
	Shared       bool     `json:"shared"`
	Parents      []string `json:"parents"`
	Owners       []struct {
		EmailAddress string `json:"emailAddress"`
	} `json:"owners"`
}

const driveFileFields = "id,name,mimeType,modifiedTime,webViewLink,size"
const driveDetailFields = "id,name,mimeType,modifiedTime,webViewLink,size,shared,parents,owners"

// opScopes maps each operation key to alternative scope groups (OR of ANDs).
var opScopes = map[string][][]string{
	"list_files": {
		{"https://www.googleapis.com/auth/drive.readonly"},
		{"https://www.googleapis.com/auth/drive"},
	},
	"search_files": {
		{"https://www.googleapis.com/auth/drive.readonly"},
		{"https://www.googleapis.com/auth/drive"},
	},
	"get_file_info": {
		{"https://www.googleapis.com/auth/drive.readonly"},
		{"https://www.googleapis.com/auth/drive"},
	},
	"get_file_content": {
		{"https://www.googleapis.com/auth/drive.readonly"},
		{"https://www.googleapis.com/auth/drive"},
	},
	"upload_file":   {{"https://www.googleapis.com/auth/drive"}},
	"create_folder": {{"https://www.googleapis.com/auth/drive"}},
	"delete_file":   {{"https://www.googleapis.com/auth/drive"}},
	"share_file":    {{"https://www.googleapis.com/auth/drive"}},
}

// normalizeGranted parses a space-separated scope string and applies the
// drive → drive.readonly implication (drive is a superset of drive.readonly).
func normalizeGranted(scopeStr string) map[string]bool {
	m := make(map[string]bool)
	for _, s := range strings.Fields(scopeStr) {
		m[s] = true
	}
	if m["https://www.googleapis.com/auth/drive"] {
		m["https://www.googleapis.com/auth/drive.readonly"] = true
	}
	return m
}

// evalScopes checks whether granted satisfies any scope group in required.
func evalScopes(required [][]string, granted map[string]bool) (ok bool, missing []string) {
	for _, group := range required {
		var miss []string
		for _, s := range group {
			if !granted[s] {
				miss = append(miss, s)
			}
		}
		if len(miss) == 0 {
			return true, nil
		}
		if missing == nil || len(miss) < len(missing) {
			missing = miss
		}
	}
	return false, missing
}

// shapeFileItem maps a driveFile to the stable FileItem output type.
func shapeFileItem(f driveFile) FileItem {
	return FileItem{
		ID:           f.ID,
		Name:         f.Name,
		MimeType:     f.MimeType,
		ModifiedTime: f.ModifiedTime,
		WebViewLink:  f.WebViewLink,
		Size:         f.Size,
	}
}

// shapeFileDetail maps a driveFile to the extended FileDetail output type.
func shapeFileDetail(f driveFile) FileDetail {
	ownerEmail := ""
	if len(f.Owners) > 0 {
		ownerEmail = f.Owners[0].EmailAddress
	}
	return FileDetail{
		FileItem:   shapeFileItem(f),
		OwnerEmail: ownerEmail,
		Shared:     f.Shared,
		Parents:    f.Parents,
	}
}

// parseFileList decodes a Drive API files list response into []FileItem.
func parseFileList(body []byte) ([]FileItem, error) {
	var resp struct {
		Files []driveFile `json:"files"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse file list: %w", err)
	}
	items := make([]FileItem, len(resp.Files))
	for i, f := range resp.Files {
		items[i] = shapeFileItem(f)
	}
	return items, nil
}

// buildListParams builds query params for the files.list endpoint.
func buildListParams(folderID string, pageSize int, orderBy string) url.Values {
	params := url.Values{}
	q := "trashed=false"
	if folderID != "" {
		q = fmt.Sprintf("'%s' in parents and trashed=false", folderID)
	}
	params.Set("q", q)
	params.Set("pageSize", fmt.Sprintf("%d", pageSize))
	params.Set("orderBy", orderBy)
	params.Set("fields", "files("+driveFileFields+")")
	return params
}

// buildSearchParams builds query params for a Drive full-text search.
func buildSearchParams(query string, pageSize int) url.Values {
	params := url.Values{}
	params.Set("q", query+" and trashed=false")
	params.Set("pageSize", fmt.Sprintf("%d", pageSize))
	params.Set("fields", "files("+driveFileFields+")")
	return params
}

// buildDetailParams builds query params for a single file metadata fetch.
func buildDetailParams() url.Values {
	params := url.Values{}
	params.Set("fields", driveDetailFields)
	return params
}

// validateString trims a string and returns an error if empty.
func validateString(val, name string) (string, error) {
	v := strings.TrimSpace(val)
	if v == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return v, nil
}
