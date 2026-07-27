package wick

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/store"
)

// Regression for the real bug: the store persists RAW user text (paths live in
// t.Attachments), so a history replay MUST re-append the "[Attached files]"
// block — otherwise the model is told nothing about the file and hunts for a
// path. Covers ALL file types, not just images.
func TestHistoryReplayReAppendsAttachmentPath(t *testing.T) {
	base := t.TempDir()
	layout := config.NewLayout(base)
	sid := "s-histpath"
	up := filepath.Join(layout.SessionDir(sid), "uploads")
	os.MkdirAll(up, 0o755)
	doc := filepath.Join(up, "report.pdf")
	os.WriteFile(doc, []byte("%PDF-1.4 stub"), 0o644)

	sto := store.New(store.Options{Layout: layout, SessionID: sid, AgentName: "main"})
	// Persist RAW text (what the store actually receives), path only in atts.
	sto.AppendUserTurnWithAttachments("user", "ui", "summarize this doc",
		[]store.Attachment{{Name: "report.pdf", AbsPath: doc, MIME: "application/pdf"}})

	hist := loadHistory(layout.SessionDir(sid), 0)
	var got string
	for _, c := range hist {
		for _, p := range c.Parts {
			if p != nil && p.Text != "" {
				got = p.Text
			}
		}
	}
	if !strings.Contains(got, "[Attached files]") || !strings.Contains(got, doc) {
		t.Fatalf("replayed turn must re-append the attachment path block, got: %q", got)
	}
}
