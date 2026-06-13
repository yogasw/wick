package googleworkspace

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/yogasw/wick/pkg/connector"
)

const slidesBaseURL = "https://slides.googleapis.com/v1/presentations"

func slidesGet(c *connector.Ctx, path string) ([]byte, error) {
	return doWithRefresh(c, func(token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, slidesBaseURL+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return req, nil
	})
}

func slidesPost(c *connector.Ctx, path string, body any) ([]byte, error) {
	return doWithRefresh(c, func(token string) (*http.Request, error) {
		return buildJSONRequest(c, http.MethodPost, slidesBaseURL+path, body)
	})
}

// getPresentationContent returns the text content of all slides.
func getPresentationContent(c *connector.Ctx, fileID string) (PresentationContent, error) {
	body, err := slidesGet(c, "/"+fileID)
	if err != nil {
		return PresentationContent{}, fmt.Errorf("slides get: %w", err)
	}
	var pres struct {
		Title  string `json:"title"`
		Slides []struct {
			ObjectID     string `json:"objectId"`
			PageElements []struct {
				ObjectID string `json:"objectId"`
				Shape    *struct {
					Placeholder *struct{ Type string `json:"type"` } `json:"placeholder"`
					Text        *struct {
						TextElements []struct {
							TextRun *struct{ Content string `json:"content"` } `json:"textRun"`
						} `json:"textElements"`
					} `json:"text"`
				} `json:"shape"`
			} `json:"pageElements"`
		} `json:"slides"`
	}
	if err := json.Unmarshal(body, &pres); err != nil {
		return PresentationContent{}, fmt.Errorf("parse presentation: %w", err)
	}
	slides := make([]SlideInfo, len(pres.Slides))
	for i, s := range pres.Slides {
		info := SlideInfo{Index: i, SlideID: s.ObjectID}
		for _, el := range s.PageElements {
			if el.Shape == nil || el.Shape.Text == nil {
				continue
			}
			var text string
			for _, te := range el.Shape.Text.TextElements {
				if te.TextRun != nil {
					text += te.TextRun.Content
				}
			}
			if el.Shape.Placeholder != nil && el.Shape.Placeholder.Type == "TITLE" {
				info.Title = text
			} else {
				if info.BodyText != "" {
					info.BodyText += "\n"
				}
				info.BodyText += text
			}
		}
		slides[i] = info
	}
	return PresentationContent{
		Title:      pres.Title,
		SlideCount: len(slides),
		Slides:     slides,
	}, nil
}

// addSlide adds a new slide at the given index (or appends if insertAtIndex < 0).
// Title and body text are set via a second batchUpdate using the actual
// API-assigned placeholder object IDs (not hardcoded IDs).
func addSlide(c *connector.Ctx, fileID, title, bodyText, layout string, insertAtIndex int) (SlideAddResult, error) {
	slideID := fmt.Sprintf("new_slide_%d", insertAtIndex)
	createSlideReq := map[string]any{
		"objectId":             slideID,
		"slideLayoutReference": map[string]any{"predefinedLayout": layout},
	}
	if insertAtIndex >= 0 {
		createSlideReq["insertionIndex"] = insertAtIndex
	}
	respBody, err := slidesPost(c, "/"+fileID+":batchUpdate", map[string]any{
		"requests": []any{map[string]any{"createSlide": createSlideReq}},
	})
	if err != nil {
		return SlideAddResult{}, fmt.Errorf("slides add slide: %w", err)
	}
	var resp struct {
		Replies []struct {
			CreateSlide *struct {
				ObjectID string `json:"objectId"`
			} `json:"createSlide"`
		} `json:"replies"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return SlideAddResult{}, fmt.Errorf("parse add slide response: %w", err)
	}
	resultID := slideID
	if len(resp.Replies) > 0 && resp.Replies[0].CreateSlide != nil {
		resultID = resp.Replies[0].CreateSlide.ObjectID
	}

	// Best-effort: set title/body using actual API-assigned placeholder IDs.
	if (title != "" || bodyText != "") && resultID != "" {
		presBody, err := slidesGet(c, "/"+fileID+"?fields=slides.objectId,slides.pageElements")
		if err == nil {
			var pres struct {
				Slides []struct {
					ObjectID     string `json:"objectId"`
					PageElements []struct {
						ObjectID string `json:"objectId"`
						Shape    *struct {
							Placeholder *struct{ Type string `json:"type"` } `json:"placeholder"`
						} `json:"shape"`
					} `json:"pageElements"`
				} `json:"slides"`
			}
			if json.Unmarshal(presBody, &pres) == nil {
				var textReqs []any
				for _, sl := range pres.Slides {
					if sl.ObjectID != resultID {
						continue
					}
					for _, el := range sl.PageElements {
						if el.Shape == nil || el.Shape.Placeholder == nil {
							continue
						}
						var text string
						switch el.Shape.Placeholder.Type {
						case "TITLE", "CENTERED_TITLE":
							text = title
						case "BODY", "SUBTITLE":
							text = bodyText
						}
						if text != "" {
							textReqs = append(textReqs, map[string]any{
								"insertText": map[string]any{
									"objectId":       el.ObjectID,
									"insertionIndex": 0,
									"text":           text,
								},
							})
						}
					}
				}
				if len(textReqs) > 0 {
					_, _ = slidesPost(c, "/"+fileID+":batchUpdate", map[string]any{"requests": textReqs})
				}
			}
		}
	}
	return SlideAddResult{SlideID: resultID, SlideIndex: insertAtIndex}, nil
}

// duplicateSlide duplicates the slide at slideIndex (0-based).
func duplicateSlide(c *connector.Ctx, fileID string, slideIndex int) (SlideAddResult, error) {
	body, err := slidesGet(c, "/"+fileID+"?fields=slides.objectId")
	if err != nil {
		return SlideAddResult{}, fmt.Errorf("slides get for duplicate: %w", err)
	}
	var pres struct {
		Slides []struct{ ObjectID string `json:"objectId"` } `json:"slides"`
	}
	if err := json.Unmarshal(body, &pres); err != nil {
		return SlideAddResult{}, fmt.Errorf("parse presentation slides: %w", err)
	}
	if slideIndex < 0 || slideIndex >= len(pres.Slides) {
		return SlideAddResult{}, fmt.Errorf("slide_index %d out of range (0-%d)", slideIndex, len(pres.Slides)-1)
	}
	srcID := pres.Slides[slideIndex].ObjectID
	req := map[string]any{
		"requests": []any{
			map[string]any{
				"duplicateObject": map[string]any{"objectId": srcID},
			},
		},
	}
	respBody, err := slidesPost(c, "/"+fileID+":batchUpdate", req)
	if err != nil {
		return SlideAddResult{}, fmt.Errorf("slides duplicate: %w", err)
	}
	var resp struct {
		Replies []struct {
			DuplicateObject *struct {
				ObjectID string `json:"objectId"`
			} `json:"duplicateObject"`
		} `json:"replies"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return SlideAddResult{}, fmt.Errorf("parse duplicate response: %w", err)
	}
	newID := ""
	if len(resp.Replies) > 0 && resp.Replies[0].DuplicateObject != nil {
		newID = resp.Replies[0].DuplicateObject.ObjectID
	}
	return SlideAddResult{SlideID: newID, SlideIndex: slideIndex + 1}, nil
}
