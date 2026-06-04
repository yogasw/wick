package slack

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// doUploadFile implements the Slack v2 three-step upload flow:
//  1. files.getUploadURLExternal  — obtain upload_url + file_id
//  2. HTTP PUT to upload_url      — transfer raw file bytes
//  3. files.completeUploadExternal — share the file into channelID
func doUploadFile(c *connector.Ctx, channelID, content, filename, encoding, title, initialComment string) (any, error) {
	var fileBytes []byte
	switch strings.ToLower(encoding) {
	case "base64":
		var err error
		fileBytes, err = base64.StdEncoding.DecodeString(strings.TrimSpace(content))
		if err != nil {
			return nil, fmt.Errorf("base64 decode: %w", err)
		}
	case "text":
		fileBytes = []byte(content)
	default:
		return nil, fmt.Errorf("encoding must be base64 or text, got %q", encoding)
	}

	// Step 1: request an upload URL
	step1 := map[string]any{
		"filename": filename,
		"length":   len(fileBytes),
	}
	raw1, err := slackPost(c, "files.getUploadURLExternal", step1)
	if err != nil {
		return nil, fmt.Errorf("get upload URL: %w", err)
	}
	m1, ok := raw1.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("get upload URL: unexpected response")
	}
	uploadURL, _ := m1["upload_url"].(string)
	fileID, _ := m1["file_id"].(string)
	if uploadURL == "" || fileID == "" {
		return nil, fmt.Errorf("get upload URL: missing upload_url or file_id")
	}

	// Step 2: PUT raw bytes to the upload URL
	if err := slackPutBinary(c, uploadURL, fileBytes); err != nil {
		return nil, fmt.Errorf("upload file content: %w", err)
	}

	// Step 3: complete upload and post to channel
	fileEntry := map[string]any{"id": fileID}
	if title != "" {
		fileEntry["title"] = title
	}
	step3 := map[string]any{
		"files":      []map[string]any{fileEntry},
		"channel_id": channelID,
	}
	if initialComment != "" {
		step3["initial_comment"] = initialComment
	}
	raw3, err := slackPost(c, "files.completeUploadExternal", step3)
	if err != nil {
		return nil, fmt.Errorf("complete upload: %w", err)
	}

	result := map[string]any{
		"file_id":  fileID,
		"filename": filename,
		"channel":  channelID,
	}
	if m3, ok := raw3.(map[string]any); ok {
		if files, ok := m3["files"].([]any); ok && len(files) > 0 {
			if f, ok := files[0].(map[string]any); ok {
				if pl, ok := f["permalink"].(string); ok && pl != "" {
					result["permalink"] = pl
				}
			}
		}
	}
	return result, nil
}
