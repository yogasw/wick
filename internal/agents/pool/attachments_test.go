package pool

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/store"
)

func TestAugmentWithAttachmentsNoFiles(t *testing.T) {
	got := augmentWithAttachments("hello", nil)
	if got != "hello" {
		t.Fatalf("expected unchanged text, got %q", got)
	}
	got = augmentWithAttachments("", nil)
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestAugmentWithAttachmentsAppendsPaths(t *testing.T) {
	atts := []store.Attachment{
		{Name: "screenshot.png", AbsPath: "/tmp/uploads/1-aa-screenshot.png"},
		{Name: "log.txt", AbsPath: "/tmp/uploads/2-bb-log.txt"},
	}
	got := augmentWithAttachments("analyze these", atts)
	if !strings.HasPrefix(got, "analyze these\n\n[Attached files]") {
		t.Fatalf("expected text + header prefix, got %q", got)
	}
	if !strings.Contains(got, "/tmp/uploads/1-aa-screenshot.png") {
		t.Fatalf("missing first path: %q", got)
	}
	if !strings.Contains(got, "/tmp/uploads/2-bb-log.txt") {
		t.Fatalf("missing second path: %q", got)
	}
}

func TestAugmentWithAttachmentsEmptyTextStillEmitsList(t *testing.T) {
	atts := []store.Attachment{{Name: "x.png", AbsPath: "/tmp/x.png"}}
	got := augmentWithAttachments("", atts)
	if !strings.HasPrefix(got, "[Attached files]") {
		t.Fatalf("expected header at start, got %q", got)
	}
	if !strings.Contains(got, "/tmp/x.png") {
		t.Fatalf("missing path: %q", got)
	}
}

func TestAugmentWithAttachmentsFallsBackToStoredName(t *testing.T) {
	atts := []store.Attachment{{Name: "img.png", StoredName: "1-aa-img.png"}}
	got := augmentWithAttachments("hi", atts)
	if !strings.Contains(got, "1-aa-img.png") {
		t.Fatalf("expected stored name fallback, got %q", got)
	}
}
