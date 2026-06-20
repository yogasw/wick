package googleworkspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/yogasw/wick/pkg/connector"
)

const docsBaseURL = "https://docs.googleapis.com/v1/documents"

func docsGet(c *connector.Ctx, path string) ([]byte, error) {
	return doWithRefresh(c, func(token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, docsBaseURL+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return req, nil
	})
}

func docsPost(c *connector.Ctx, path string, body any) ([]byte, error) {
	return doWithRefresh(c, func(token string) (*http.Request, error) {
		return buildJSONRequest(c, http.MethodPost, docsBaseURL+path, token, body)
	})
}

// appendDocText appends plain text to the end of a Google Document.
func appendDocText(c *connector.Ctx, fileID, text string) (DocResult, error) {
	params := url.Values{}
	params.Set("fields", "body.content,revisionId")
	docBody, err := docsGet(c, "/"+fileID+"?"+params.Encode())
	if err != nil {
		return DocResult{}, fmt.Errorf("docs get for append: %w", err)
	}
	var doc struct {
		RevisionID string `json:"revisionId"`
		Body       struct {
			Content []struct {
				EndIndex int `json:"endIndex"`
			} `json:"content"`
		} `json:"body"`
	}
	if err := json.Unmarshal(docBody, &doc); err != nil {
		return DocResult{}, fmt.Errorf("parse doc: %w", err)
	}
	endIndex := 1
	if len(doc.Body.Content) > 0 {
		last := doc.Body.Content[len(doc.Body.Content)-1]
		if last.EndIndex > 1 {
			endIndex = last.EndIndex - 1
		}
	}
	req := map[string]any{
		"requests": []any{
			map[string]any{
				"insertText": map[string]any{
					"location": map[string]any{"index": endIndex},
					"text":     text,
				},
			},
		},
	}
	respBody, err := docsPost(c, "/"+fileID+":batchUpdate", req)
	if err != nil {
		return DocResult{}, fmt.Errorf("docs append text: %w", err)
	}
	var resp struct {
		RevisionID string `json:"documentRevisionId"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return DocResult{}, fmt.Errorf("parse append response: %w", err)
	}
	return DocResult{RevisionID: resp.RevisionID}, nil
}

// replaceDocText performs find-and-replace throughout a Google Document.
func replaceDocText(c *connector.Ctx, fileID, find, replace string, matchCase bool) (DocResult, error) {
	req := map[string]any{
		"requests": []any{
			map[string]any{
				"replaceAllText": map[string]any{
					"replaceText": replace,
					"containsText": map[string]any{
						"text":      find,
						"matchCase": matchCase,
					},
				},
			},
		},
	}
	respBody, err := docsPost(c, "/"+fileID+":batchUpdate", req)
	if err != nil {
		return DocResult{}, fmt.Errorf("docs replace text: %w", err)
	}
	var resp struct {
		Replies []struct {
			ReplaceAllText *struct {
				OccurrencesChanged int `json:"occurrencesChanged"`
			} `json:"replaceAllText"`
		} `json:"replies"`
		RevisionID string `json:"documentRevisionId"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return DocResult{}, fmt.Errorf("parse replace response: %w", err)
	}
	changed := 0
	if len(resp.Replies) > 0 && resp.Replies[0].ReplaceAllText != nil {
		changed = resp.Replies[0].ReplaceAllText.OccurrencesChanged
	}
	return DocResult{RevisionID: resp.RevisionID, OccurrencesChanged: changed}, nil
}
