package team

import (
	"encoding/json"
	"errors"
	"strings"
)

// MaxSuggestedPrompts is Slack's cap on agent-view prompt chips.
const MaxSuggestedPrompts = 4

// Lengths a prompt chip is kept under; Slack truncates a long title in
// the chip anyway.
const (
	maxPromptTitle   = 75
	maxPromptMessage = 1000
)

// SuggestedPrompt is one prompt chip: Title is what the chip shows,
// Message what clicking it sends.
type SuggestedPrompt struct {
	Title   string `json:"title"`
	Message string `json:"message"`
}

// ErrTooManyPrompts is NormalizeSuggestedPrompts' answer to more than
// MaxSuggestedPrompts non-empty prompts.
var ErrTooManyPrompts = errors.New("at most 4 suggested prompts")

// NormalizeSuggestedPrompts trims every prompt and drops the ones with
// nothing in both fields. A prompt with only one of the two takes the
// other from it, so a chip is never blank and never sends nothing.
func NormalizeSuggestedPrompts(in []SuggestedPrompt) ([]SuggestedPrompt, error) {
	out := make([]SuggestedPrompt, 0, len(in))
	for _, p := range in {
		p.Title, p.Message = strings.TrimSpace(p.Title), strings.TrimSpace(p.Message)
		if p.Title == "" && p.Message == "" {
			continue
		}
		if p.Title == "" {
			p.Title = p.Message
		}
		if p.Message == "" {
			p.Message = p.Title
		}
		p.Title = clipRunes(p.Title, maxPromptTitle)
		p.Message = clipRunes(p.Message, maxPromptMessage)
		out = append(out, p)
	}
	if len(out) > MaxSuggestedPrompts {
		return nil, ErrTooManyPrompts
	}
	return out, nil
}

// DecodeSuggestedPrompts reads the stored column; anything unreadable is
// no prompts.
func DecodeSuggestedPrompts(raw string) []SuggestedPrompt {
	out := []SuggestedPrompt{}
	if raw == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return []SuggestedPrompt{}
	}
	return out
}

// EncodeSuggestedPrompts is the stored form of a normalized list.
func EncodeSuggestedPrompts(p []SuggestedPrompt) string {
	if len(p) == 0 {
		return "[]"
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
