package wick

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
	"google.golang.org/genai"
)

// loadHistory rebuilds the model message history for a session from its
// conversation.jsonl — the same provider-agnostic store every provider
// writes through the parser pipeline. This is what gives wick
// cross-provider continuity: a session started on claude/codex/gemini
// keeps its conversation when switched to wick, because the turns are
// read from the shared store, not a wick-private file.
//
// v1 replays TEXT turns only (user + assistant). Tool-call trace replay
// (reading thinking/<turn_id>.json) is deferred behind config — richer
// grounding, more tokens. System turns are skipped in v1 except the
// compaction summary, which is folded in as a model note.
//
// maxContextTokens>0 trims oldest-first (keeping the most recent turns)
// so the replay fits the budget. Token count uses the cheap chars/4
// estimate; the engine calibrates against real usage on later turns.
func loadHistory(sessionDir string, maxContextTokens int) []*genai.Content {
	if sessionDir == "" {
		return nil
	}

	// If a compaction sidecar exists, the turns it covers must NOT be
	// replayed verbatim — they're represented by the summary. Replay
	// [summary note] + only the turns AFTER the cutoff. This is what makes
	// compaction actually stick across a respawn: without it, loadHistory
	// would re-read the whole conversation.jsonl (the full, uncompacted
	// history) and the summary would be pointless. rawIdx tracks the on-disk
	// turn position so we can skip everything at/below CoveredThrough.
	cs := readCompactionState(sessionDir)

	path := filepath.Join(sessionDir, "conversation.jsonl")
	var contents []*genai.Content
	if cs != nil {
		contents = append(contents, genai.NewContentFromText(
			compactionMarker+"\n"+cs.Summary, genai.RoleModel))
	}
	rawIdx := 0
	_ = storage.ReadJSONL(path, func(line []byte) bool {
		idx := rawIdx
		rawIdx++
		var t store.ConversationTurn
		if json.Unmarshal(line, &t) != nil {
			return true
		}
		// Skip turns already folded into the summary.
		if cs != nil && idx < cs.CoveredThrough {
			return true
		}
		if c := turnToContent(t); c != nil {
			contents = append(contents, c)
		}
		return true
	})
	if maxContextTokens > 0 {
		contents = trimToBudget(contents, maxContextTokens)
	}
	return contents
}

// lastUserAttachments returns the attachments on the most recent user turn in
// the session's conversation.jsonl. The pool persists the user turn (with
// attachments) BEFORE handing wick the message, so when runTurn builds the
// current user content this reads back the image attachments the pool only
// surfaced to wick as path text. Empty when none / unreadable.
func lastUserAttachments(sessionDir string) []store.Attachment {
	if sessionDir == "" {
		return nil
	}
	path := filepath.Join(sessionDir, "conversation.jsonl")
	var atts []store.Attachment
	_ = storage.ReadJSONL(path, func(line []byte) bool {
		var t store.ConversationTurn
		if json.Unmarshal(line, &t) != nil {
			return true
		}
		if t.Role == "user" && len(t.Attachments) > 0 {
			atts = t.Attachments // keep the latest; append-order means last wins
		}
		return true
	})
	return atts
}

// currentUserContent builds the genai.Content for the message being sent THIS
// turn: the text plus inline image parts from the latest persisted user turn's
// image attachments. Falls back to text-only when there are no images.
func currentUserContent(sessionDir, userText string) *genai.Content {
	imgs := imagePartsFromAttachments(lastUserAttachments(sessionDir))
	return userContentWithImages(userText, imgs)
}

// userContentWithImages builds a user Content: the pool-augmented text (which
// ALREADY lists every attachment's path under "[Attached files]", so the model
// can read_file any of them) plus, as a BONUS, inline image parts for image
// attachments so a vision-capable model can also see them directly. The path
// text is deliberately KEPT — it's the reliable, all-file-types channel; the
// image part is an addition, not a replacement. Text-only when there are no
// images.
func userContentWithImages(text string, imgs []*genai.Part) *genai.Content {
	if len(imgs) == 0 {
		return genai.NewContentFromText(text, genai.RoleUser)
	}
	parts := make([]*genai.Part, 0, len(imgs)+1)
	if strings.TrimSpace(text) != "" {
		parts = append(parts, &genai.Part{Text: text})
	}
	parts = append(parts, imgs...)
	return &genai.Content{Role: genai.RoleUser, Parts: parts}
}


// maxInlineImageBytes caps a single inline image (5 MB) so a stray huge file
// can't blow up the request body / token budget. Larger images are skipped
// (the model just won't see them; the path text still rides along).
const maxInlineImageBytes = 5 << 20

// imagePartsFromAttachments turns a user turn's IMAGE attachments into inline
// genai image parts (read from disk, base64 handled by the SDK/adapters via
// InlineData). Non-image attachments and unreadable/oversize files are skipped.
// This is the wick vision pipe: the pool only appended file PATHS as text, so
// without this the model never actually receives the picture.
func imagePartsFromAttachments(atts []store.Attachment) []*genai.Part {
	var out []*genai.Part
	for _, a := range atts {
		mime := strings.ToLower(strings.TrimSpace(a.MIME))
		if !strings.HasPrefix(mime, "image/") {
			continue
		}
		path := a.AbsPath
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 || info.Size() > maxInlineImageBytes {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		out = append(out, &genai.Part{InlineData: &genai.Blob{MIMEType: mime, Data: data}})
	}
	return out
}

// appendAttachmentPaths re-appends the "[Attached files]" path block for a
// replayed user turn, mirroring the pool's augmentWithAttachments (which runs
// only on the LIVE send, not on what's persisted). Keeping the exact same
// format means a replayed turn reads identically to when it was first sent, so
// read_file still resolves the paths. Returns text unchanged when there are no
// attachments.
func appendAttachmentPaths(text string, atts []store.Attachment) string {
	if len(atts) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString(text)
	if text != "" {
		b.WriteString("\n\n")
	}
	b.WriteString("[Attached files]")
	for _, a := range atts {
		path := a.AbsPath
		if path == "" {
			path = a.StoredName
		}
		b.WriteString("\n- ")
		if a.Name != "" && a.Name != filepath.Base(path) {
			b.WriteString(a.Name)
			b.WriteString(": ")
		}
		b.WriteString(path)
	}
	return b.String()
}

// turnToContent maps one stored turn to a genai.Content, or nil to skip.
func turnToContent(t store.ConversationTurn) *genai.Content {
	text := strings.TrimSpace(t.Text)
	switch t.Role {
	case "user":
		imgs := imagePartsFromAttachments(t.Attachments)
		if text == "" && len(t.Attachments) == 0 {
			return nil
		}
		// The store persists the RAW user text (path list lives structurally in
		// t.Attachments, not in the text). The pool's live send re-appends the
		// "[Attached files]" block via augmentWithAttachments, but a HISTORY
		// replay reads t.Text directly — so we must re-append the path block
		// here too, or replayed turns lose every attachment path and the model
		// is told nothing about the files (it then hunts/hallucinates paths).
		// Images ALSO ride as inline parts (bonus for vision models).
		text = appendAttachmentPaths(text, t.Attachments)
		return userContentWithImages(text, imgs)
	case "assistant":
		if text == "" {
			return nil
		}
		return genai.NewContentFromText(text, genai.RoleModel)
	case "system":
		// Only the compaction summary carries context worth replaying;
		// other system turns (provider_switch, errors) are display-only.
		// (wick's own compaction now uses the compaction.json sidecar, not a
		// kind:"compaction" turn — this branch stays for cross-provider
		// sessions that may carry one, kept on the same marker for
		// consistency with the pinning in trimToBudget.)
		if t.Kind == "compaction" && text != "" {
			return genai.NewContentFromText(compactionMarker+"\n"+text, genai.RoleModel)
		}
		return nil
	}
	return nil
}

// trimToBudget drops oldest contents until the estimated token count is
// within budget, always keeping the most recent turns. A single
// oversized most-recent turn is kept as-is (better a big last turn than
// an empty history).
//
// A leading compaction summary note is PINNED: it represents every turn
// already folded away, so dropping it would silently lose all of that
// context. When present we trim the turns after it instead, and keep the
// note at the head.
func trimToBudget(contents []*genai.Content, maxTokens int) []*genai.Content {
	if len(contents) > 0 && isCompactionNote(contents[0]) {
		note := contents[0]
		rest := contents[1:]
		for len(rest) > 1 && estimateTokens(append([]*genai.Content{note}, rest...)) > maxTokens {
			rest = rest[1:]
		}
		return append([]*genai.Content{note}, rest...)
	}
	for len(contents) > 1 && estimateTokens(contents) > maxTokens {
		contents = contents[1:]
	}
	return contents
}

// isCompactionNote reports whether c is a compaction summary note (the
// pinned head produced by compaction / loaded from the sidecar).
func isCompactionNote(c *genai.Content) bool {
	if c == nil || len(c.Parts) == 0 || c.Parts[0] == nil {
		return false
	}
	return strings.HasPrefix(c.Parts[0].Text, compactionMarker)
}

// estimateTokens is the cheap chars/4 heuristic over ALL part payloads —
// not just text. A wick turn's biggest contributors are tool traffic:
// FunctionCall.Args (the params JSON) and especially FunctionResponse
// (a shell/connector result can be tens of KB). Counting only p.Text
// (the old bug) undercounted history by the entire tool-result mass, so
// the context budget was compared against a fraction of the real prompt
// and overflowed the vendor's window (e.g. 557k tokens sent under a 500k
// cap). partChars folds every payload in.
func estimateTokens(contents []*genai.Content) int {
	chars := 0
	for _, c := range contents {
		if c == nil {
			continue
		}
		for _, p := range c.Parts {
			chars += partChars(p)
		}
	}
	return chars / 4
}

// partChars approximates the serialized size of one part: text, plus tool
// call/result payloads which carry no p.Text but dominate real token
// usage. Marshal errors fall back to 0 for that field (can't happen for
// these shapes, but never panic in a size estimator).
func partChars(p *genai.Part) int {
	if p == nil {
		return 0
	}
	n := len(p.Text)
	if fc := p.FunctionCall; fc != nil {
		n += len(fc.Name)
		if b, err := json.Marshal(fc.Args); err == nil {
			n += len(b)
		}
	}
	if fr := p.FunctionResponse; fr != nil {
		n += len(fr.Name)
		if b, err := json.Marshal(fr.Response); err == nil {
			n += len(b)
		}
	}
	// Inline image: a vision model bills images by area, not bytes, but the
	// engine's budget only needs a rough, monotonic contribution so a big
	// image still pushes toward compaction. Approximate at ~1 token per 3
	// image bytes (in chars/4 terms that's ~0.75 tokens/byte) — deliberately
	// on the high side so images err toward triggering compaction early.
	if id := p.InlineData; id != nil {
		n += len(id.Data) * 3
	}
	return n
}
