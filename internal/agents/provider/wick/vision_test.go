package wick

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/genai"

	"github.com/yogasw/wick/internal/agents/store"
)

// A tiny 1x1 PNG is enough — imagePartsFromAttachments only reads bytes.
var testPNG = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x01, 0x02, 0x03}

func writeTempImage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(p, testPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// imagePartsFromAttachments: image MIME → InlineData part; non-image + missing
// path skipped.
func TestImagePartsFromAttachments(t *testing.T) {
	p := writeTempImage(t)
	atts := []store.Attachment{
		{Name: "shot.png", AbsPath: p, MIME: "image/png"},
		{Name: "notes.txt", AbsPath: p, MIME: "text/plain"}, // non-image → skip
		{Name: "gone.png", AbsPath: filepath.Join(t.TempDir(), "nope.png"), MIME: "image/png"}, // missing → skip
	}
	parts := imagePartsFromAttachments(atts)
	if len(parts) != 1 {
		t.Fatalf("want 1 image part, got %d", len(parts))
	}
	if parts[0].InlineData == nil || parts[0].InlineData.MIMEType != "image/png" {
		t.Fatalf("bad inline data: %+v", parts[0].InlineData)
	}
	if string(parts[0].InlineData.Data) != string(testPNG) {
		t.Fatalf("image bytes not read verbatim")
	}
}

// turnToContent: a user turn with an image attachment becomes a multi-part
// content (text + image).
func TestTurnToContent_Image(t *testing.T) {
	p := writeTempImage(t)
	turn := store.ConversationTurn{
		Role:        "user",
		Text:        "what is this?",
		Attachments: []store.Attachment{{AbsPath: p, MIME: "image/png"}},
	}
	c := turnToContent(turn)
	if c == nil || len(c.Parts) != 2 {
		t.Fatalf("want text+image (2 parts), got %+v", c)
	}
	// Text part keeps the original message AND the re-appended "[Attached
	// files]" path block (so read_file still resolves on replay); image rides
	// as the inline part.
	if !strings.HasPrefix(c.Parts[0].Text, "what is this?") || !strings.Contains(c.Parts[0].Text, "[Attached files]") {
		t.Fatalf("text part should keep message + attachment paths, got %q", c.Parts[0].Text)
	}
	if !strings.Contains(c.Parts[0].Text, p) {
		t.Fatalf("attachment path %q not in text: %q", p, c.Parts[0].Text)
	}
	if c.Parts[1].InlineData == nil {
		t.Fatalf("second part should be the inline image, got %+v", c.Parts[1])
	}
}

// Anthropic adapter: an InlineData part → an image block with base64 source.
func TestAnthropicImageBlock(t *testing.T) {
	m := &anthropicModel{modelID: "claude-x"}
	content := &genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{
		{Text: "look"},
		{InlineData: &genai.Blob{MIMEType: "image/png", Data: testPNG}},
	}}
	out := m.buildRequest(&LLMRequest{Contents: []*genai.Content{content}})
	if len(out.Messages) != 1 || len(out.Messages[0].Content) != 2 {
		t.Fatalf("want 1 msg with 2 blocks, got %+v", out.Messages)
	}
	img := out.Messages[0].Content[1]
	if img.Type != "image" || img.Source == nil || img.Source.Type != "base64" || img.Source.MediaType != "image/png" {
		t.Fatalf("bad image block: %+v", img)
	}
	if img.Source.Data != base64.StdEncoding.EncodeToString(testPNG) {
		t.Fatalf("image not base64-encoded correctly")
	}
}

// OpenAI adapter: an InlineData part → content array with an image_url data-URL;
// a text-only message stays a scalar string (byte-identical to before).
func TestOpenAIImageContent(t *testing.T) {
	m := &openAIModel{modelID: "gpt-x"}

	// Text-only → scalar string content.
	textOnly := m.buildRequest(&LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("hi", genai.RoleUser)}})
	if s, ok := textOnly.Messages[0].Content.(string); !ok || s != "hi" {
		t.Fatalf("text-only content should be scalar string, got %T %v", textOnly.Messages[0].Content, textOnly.Messages[0].Content)
	}

	// With image → array content.
	content := &genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{
		{Text: "look"},
		{InlineData: &genai.Blob{MIMEType: "image/png", Data: testPNG}},
	}}
	out := m.buildRequest(&LLMRequest{Contents: []*genai.Content{content}})
	parts, ok := out.Messages[0].Content.([]oaiContentPart)
	if !ok || len(parts) != 2 {
		t.Fatalf("want 2-part array content, got %T %v", out.Messages[0].Content, out.Messages[0].Content)
	}
	if parts[0].Type != "text" || parts[0].Text != "look" {
		t.Fatalf("first part should be text, got %+v", parts[0])
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(testPNG)
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil || parts[1].ImageURL.URL != want {
		t.Fatalf("bad image_url part: %+v", parts[1])
	}
}
