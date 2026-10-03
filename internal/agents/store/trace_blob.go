package store

// trace_blob.go — binary tool payloads in the trace.
//
// A tool that returns an image (a screenshot, a Read of a .png) hands the
// model hundreds of KB of base64. Cutting that to trace_event_max_kb leaves
// a broken string nobody can render, so binaries take a different road:
// the decoded bytes are written whole to thinking/<turn>/<event_id>.bin
// (up to trace_blob_max_mb) and the event's text becomes a short reference.
// Display.BlobRef tells the UI which file to fetch; a binary over the cap
// keeps only its type and size (Display.TooLarge).

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/yogasw/wick/internal/agents/event"
)

// rawPreviewChars is how much of a binary's original text the reference
// keeps — enough to recognise the envelope, never a decodable payload.
const rawPreviewChars = 120

func (s *Store) blobMax() int {
	if s.traceBlobMaxBytes > 0 {
		return s.traceBlobMaxBytes
	}
	return DefaultTraceBlobMaxBytes
}

// storeBlobs writes the binary parts of ev to blob files and returns ev as
// it should be recorded: Display with BlobRef/TooLarge set and no bytes,
// Text replaced by a reference. Events without a binary pass through.
func (s *Store) storeBlobs(turnID, eventID string, ev TurnEvent) TurnEvent {
	if !ev.Display.HasBinary() {
		return ev
	}
	d := *ev.Display
	d.Parts = append([]event.Display(nil), d.Parts...)
	if missingBlob(&d) {
		// Recovered from inflight.jsonl: Blob is never serialized, so
		// re-derive the bytes from the raw text the entry kept.
		re := event.ClassifyResult(ev.ToolName, ev.Text, ev.IsError, nil)
		if re.HasBinary() && !missingBlob(&re) {
			re.Tool, re.Title, re.ExitCode = d.Tool, d.Title, d.ExitCode
			if d.Name != "" && event.IsBinary(re.Kind) {
				re.Name = d.Name
			}
			d = re
		}
	}
	put := func(p *event.Display, ref string) {
		if !event.IsBinary(p.Kind) || len(p.Blob) == 0 {
			p.Blob = nil
			return
		}
		if len(p.Blob) > s.blobMax() {
			p.TooLarge = true
		} else if err := os.WriteFile(s.layout.SessionThinkingBlob(s.sessionID, turnID, ref), p.Blob, 0o644); err == nil {
			p.BlobRef = ref
		}
		p.Blob = nil
	}
	put(&d, eventID)
	for i := range d.Parts {
		put(&d.Parts[i], fmt.Sprintf("%s-p%d", eventID, i))
	}
	ev.Text = blobRefText(&d, ev.Text)
	ev.Display = &d
	return ev
}

func missingBlob(d *event.Display) bool {
	if event.IsBinary(d.Kind) && len(d.Blob) == 0 {
		return true
	}
	for i := range d.Parts {
		if event.IsBinary(d.Parts[i].Kind) && len(d.Parts[i].Blob) == 0 {
			return true
		}
	}
	return false
}

// blobRefText is the text recorded in place of a binary payload: one line
// per binary (mime, size, where it went), any text the result carried,
// and the first rawPreviewChars of the original for the Raw view.
func blobRefText(d *event.Display, raw string) string {
	var b strings.Builder
	line := func(p *event.Display) {
		where := "blob " + p.BlobRef
		if p.TooLarge {
			where = "too large to keep in trace"
		} else if p.BlobRef == "" {
			where = "not stored"
		}
		fmt.Fprintf(&b, "[%s · %s · %s]\n", p.Mime, event.HumanBytes(p.OriginalBytes), where)
	}
	if event.IsBinary(d.Kind) {
		line(d)
	} else if strings.TrimSpace(d.Body) != "" {
		b.WriteString(d.Body)
		b.WriteString("\n")
	}
	for i := range d.Parts {
		if event.IsBinary(d.Parts[i].Kind) {
			line(&d.Parts[i])
		}
	}
	fmt.Fprintf(&b, "raw (%s): %s…", event.HumanBytes(len(raw)), truncateUTF8(raw, rawPreviewChars))
	return b.String()
}

// truncateUTF8 cuts s to at most n bytes without splitting a rune.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
