package wick

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/store"
	"google.golang.org/genai"
)

// TestVisionEndToEnd exercises the FULL wick vision pipe with no mocks of the
// wick code under test: a user turn with an image attachment is persisted
// through the REAL store (AppendUserTurnWithAttachments → conversation.jsonl),
// then read back exactly the way the engine does at runtime (currentUserContent
// + loadHistory read from that same session dir), then fed through each vendor
// adapter's buildRequest — asserting the image actually lands in the outbound
// body. This is the regression guard for "attached image never reached the
// model" (every message.content was a plain string). No vendor network call.
func TestVisionEndToEnd(t *testing.T) {
	base := t.TempDir()
	layout := config.NewLayout(base)
	sessionID := "sess-vision"

	// The upload must physically exist at the attachment's AbsPath — the pipe
	// reads the bytes off disk (a missing/oversize file is silently skipped, so
	// a test that forgot this would false-pass with no image).
	uploads := filepath.Join(layout.SessionDir(sessionID), "uploads")
	if err := os.MkdirAll(uploads, 0o755); err != nil {
		t.Fatal(err)
	}
	imgPath := filepath.Join(uploads, "shot.png")
	if err := os.WriteFile(imgPath, testPNG, 0o644); err != nil {
		t.Fatal(err)
	}

	// Persist the user turn through the real store, mirroring what the pool's
	// send() does before it hands wick the message.
	sto := store.New(store.Options{Layout: layout, SessionID: sessionID, AgentName: "main"})
	att := store.Attachment{
		Name:       "shot.png",
		StoredName: "shot.png",
		AbsPath:    imgPath,
		MIME:       "image/png",
	}
	// Pool appends the file PATH to the text ("[Attached files]") — the pipe
	// must strip that image path once the image rides inline.
	augmented := "ocr this image\n\n[Attached files]\n- " + imgPath
	if err := sto.AppendUserTurnWithAttachments("user", "ui", augmented, []store.Attachment{att}); err != nil {
		t.Fatal(err)
	}

	sessionDir := layout.SessionDir(sessionID)

	// 1) currentUserContent (the CURRENT message the engine builds this turn).
	//    The image rides as an inline part AND the "[Attached files]" path text
	//    is KEPT — the path is the reliable all-file-types channel (read_file),
	//    the image part is a bonus for vision models.
	cur := currentUserContent(sessionDir, augmented)
	assertHasImage(t, "currentUserContent", cur)
	assertHasPath(t, "currentUserContent", cur, imgPath)

	// 2) loadHistory (replay path) must also carry the image on the user turn.
	hist := loadHistory(sessionDir, 0, store.SenderName)
	var userTurn *genai.Content
	for _, c := range hist {
		if c != nil && c.Role == genai.RoleUser {
			userTurn = c
		}
	}
	if userTurn == nil {
		t.Fatal("loadHistory produced no user turn")
	}
	assertHasImage(t, "loadHistory", userTurn)

	contents := []*genai.Content{cur}

	// 3a) OpenAI adapter (covers openai / openrouter / grok / other) — the user
	//     message content must be an array with an image_url data-URL.
	oai := (&openAIModel{modelID: "x"}).buildRequest(&LLMRequest{Contents: contents})
	var sawOAIImage bool
	for _, m := range oai.Messages {
		parts, ok := m.Content.([]oaiContentPart)
		if !ok {
			continue
		}
		for _, p := range parts {
			if p.Type == "image_url" && p.ImageURL != nil &&
				p.ImageURL.URL == "data:image/png;base64,"+base64.StdEncoding.EncodeToString(testPNG) {
				sawOAIImage = true
			}
		}
	}
	if !sawOAIImage {
		t.Errorf("OpenAI body carried no image_url part: %+v", oai.Messages)
	}

	// 3b) Anthropic adapter — an "image" block with a base64 source.
	ant := (&anthropicModel{modelID: "claude-x"}).buildRequest(&LLMRequest{Contents: contents})
	var sawAntImage bool
	for _, m := range ant.Messages {
		for _, b := range m.Content {
			if b.Type == "image" && b.Source != nil && b.Source.Type == "base64" &&
				b.Source.Data == base64.StdEncoding.EncodeToString(testPNG) {
				sawAntImage = true
			}
		}
	}
	if !sawAntImage {
		t.Errorf("Anthropic body carried no image block: %+v", ant.Messages)
	}
}

func assertHasImage(t *testing.T, where string, c *genai.Content) {
	t.Helper()
	if c == nil {
		t.Fatalf("%s: nil content", where)
	}
	for _, p := range c.Parts {
		if p != nil && p.InlineData != nil && len(p.InlineData.Data) > 0 {
			return
		}
	}
	t.Fatalf("%s: no inline image part in %+v", where, c.Parts)
}

// assertHasPath: the "[Attached files]" path text is KEPT so read_file remains
// a reliable channel for every file type (the image part is a bonus on top).
func assertHasPath(t *testing.T, where string, c *genai.Content, imgPath string) {
	t.Helper()
	for _, p := range c.Parts {
		if p != nil && p.Text != "" && strings.Contains(p.Text, imgPath) {
			return
		}
	}
	t.Fatalf("%s: expected the attachment path %q kept in the text, not found in %+v", where, imgPath, c.Parts)
}
