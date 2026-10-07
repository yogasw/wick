package event

// display.go — how a tool call or tool result should be RENDERED.
//
// Every provider hands wick a tool payload in its own shape: claude's
// tool_result content is a JSON string literal or an array of content
// blocks, codex wraps shell output in {"output":…,"metadata":…}, omp and
// MCP servers send image blocks next to text. Left alone, the UI shows the
// escaped JSON ("# P1 …\n…") instead of the markdown it carries.
//
// Display is the one answer to "what is this payload": a kind, the
// unwrapped body, and the few fields a card needs (command, path, pattern,
// connector/op, mime, size). Parsers extract the raw text from their own
// format, fix their own quirks, and call ClassifyCall / ClassifyResult —
// via toolCalls.decorate — on the FULL payload, before the store truncates
// anything. The raw text stays on the event untouched; Display sits beside
// it, so the UI can always offer "Raw".
//
// Adding a kind: write a Detector and RegisterDetector it (init in your
// own file is fine). Detectors run highest priority first; the first one
// that returns ok=true wins. Nothing in the parsers or the store changes —
// they only carry the Display through. The FE dispatches on Kind and falls
// back to plain text for a kind it does not know.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Display kinds. Binary kinds (image/pdf/audio/video/binary) never carry
// a Body: the bytes live in Blob (in memory) and, once stored, in a blob
// file named by BlobRef.
const (
	KindText     = "text"
	KindMarkdown = "markdown"
	KindJSON     = "json"
	KindCode     = "code"
	KindDiff     = "diff"
	KindError    = "error"
	KindTerminal = "terminal" // shell output
	KindCommand  = "command"  // shell call
	KindFile     = "file"     // read/write call on one path
	KindSearch   = "search"   // grep/glob/web search call or file-list result
	KindMCP      = "mcp"      // connector/MCP call
	KindImage    = "image"
	KindPDF      = "pdf"
	KindAudio    = "audio"
	KindVideo    = "video"
	KindBinary   = "binary" // any other binary payload
)

// Tool families — provider tool names normalized so one detector serves
// claude's "Bash", codex's "exec_command" and opencode's "bash" alike.
const (
	ToolBash      = "bash"
	ToolRead      = "read"
	ToolWrite     = "write"
	ToolEdit      = "edit"
	ToolPatch     = "patch"
	ToolGrep      = "grep"
	ToolGlob      = "glob"
	ToolMCP       = "mcp"
	ToolWebSearch = "web_search"
	ToolFetch     = "fetch"
)

// Display is the render hint for one tool call or tool result. Only Kind
// is always set; the rest depends on the kind. Optional everywhere, so a
// trace written before Display existed still decodes.
type Display struct {
	Kind string `json:"kind"`
	// Body is the unwrapped payload to render (markdown source, pretty
	// enough JSON, terminal output, diff). Empty for binary kinds.
	Body string `json:"body,omitempty"`
	// Lang is the syntax-highlight language for code/command/file bodies.
	Lang string `json:"lang,omitempty"`
	// Mime is set for binary kinds (image/png, application/pdf, …).
	Mime string `json:"mime,omitempty"`
	// OriginalBytes is the size of the payload before the trace cut it:
	// the raw text length, or the decoded byte count for binary kinds.
	OriginalBytes int `json:"original_bytes,omitempty"`
	// Truncated is set by the store when Body was cut to the event cap.
	// The agent itself saw the full payload; only the trace copy is short.
	Truncated bool `json:"truncated,omitempty"`
	// BlobRef names the stored binary (e.g. "e3") — fetch it from the
	// trace blob endpoint. Empty until stored, or when TooLarge.
	BlobRef string `json:"blob_ref,omitempty"`
	// TooLarge marks a binary over trace_blob_max_mb: info only, no blob.
	TooLarge bool `json:"too_large,omitempty"`

	// Card fields — the one-line header and the few facts a call card shows.
	Tool      string `json:"tool,omitempty"`  // normalized family (ToolBash, …)
	Title     string `json:"title,omitempty"` // tool name as the provider gave it
	Summary   string `json:"summary,omitempty"`
	Command   string `json:"command,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	Timeout   int    `json:"timeout,omitempty"` // ms, when the call set one
	Path      string `json:"path,omitempty"`
	Pattern   string `json:"pattern,omitempty"`
	Connector string `json:"connector,omitempty"`
	Op        string `json:"op,omitempty"`
	ExitCode  *int   `json:"exit_code,omitempty"`
	Name      string `json:"name,omitempty"` // file name for a binary chip

	// Parts holds the extra blocks of a mixed result (text + image): the
	// top-level Display renders the text, each part one binary chip.
	Parts []Display `json:"parts,omitempty"`

	// Blob is the decoded binary, in memory only. The store writes it to
	// the blob file and never serializes it.
	Blob []byte `json:"-"`
}

// IsBinary reports whether kind is a binary kind (no Body, blob-backed).
func IsBinary(kind string) bool {
	switch kind {
	case KindImage, KindPDF, KindAudio, KindVideo, KindBinary:
		return true
	}
	return false
}

// HasBinary reports whether d or any of its parts is binary.
func (d *Display) HasBinary() bool {
	if d == nil {
		return false
	}
	if IsBinary(d.Kind) {
		return true
	}
	for i := range d.Parts {
		if IsBinary(d.Parts[i].Kind) {
			return true
		}
	}
	return false
}

// DetectInput is what a detector sees.
type DetectInput struct {
	ToolName string   // as the provider named it
	Tool     string   // normalized family, "" when unknown
	Text     string   // unwrapped payload
	Call     bool     // true for a tool_use input, false for a result
	IsError  bool     // result only
	CallInfo *Display // result only: the matching call's Display, when known
}

// Detector inspects one payload and returns its Display when it applies.
type Detector func(in DetectInput) (Display, bool)

type detectorEntry struct {
	kind     string
	priority int
	seq      int
	fn       Detector
}

var (
	detectorsMu sync.RWMutex
	detectors   []detectorEntry
)

// RegisterDetector adds a detector. Higher priority runs first; equal
// priorities keep registration order. kind is informational (what the
// detector produces) — a detector may still return another kind.
func RegisterDetector(kind string, priority int, fn Detector) {
	detectorsMu.Lock()
	defer detectorsMu.Unlock()
	detectors = append(detectors, detectorEntry{kind: kind, priority: priority, seq: len(detectors), fn: fn})
	sort.SliceStable(detectors, func(i, j int) bool {
		if detectors[i].priority != detectors[j].priority {
			return detectors[i].priority > detectors[j].priority
		}
		return detectors[i].seq < detectors[j].seq
	})
}

func runDetectors(in DetectInput) Display {
	detectorsMu.RLock()
	ds := detectors
	detectorsMu.RUnlock()
	for _, d := range ds {
		if out, ok := d.fn(in); ok {
			return out
		}
	}
	return Display{Kind: KindText, Body: in.Text}
}

// Classify classifies a successful tool result.
func Classify(toolName, raw string) Display {
	return ClassifyResult(toolName, raw, false, nil)
}

// ClassifyResult classifies a tool result. call is the matching call's
// Display when the parser knows it (gives detectors the path/family).
func ClassifyResult(toolName, raw string, isError bool, call *Display) Display {
	text, bins := unwrapPayload(raw)
	in := DetectInput{ToolName: toolName, Tool: ToolFamily(toolName), Text: text, IsError: isError, CallInfo: call}
	var d Display
	if len(bins) > 0 && strings.TrimSpace(text) == "" {
		d = bins[0]
		d.Parts = bins[1:]
		if len(d.Parts) == 0 {
			d.Parts = nil
		}
	} else {
		d = runDetectors(in)
		d.Parts = append(d.Parts, bins...)
	}
	finish(&d, in, raw)
	if IsBinary(d.Kind) && call != nil && call.Path != "" {
		// The read call names the file better than a mime-derived default.
		d.Name = path.Base(call.Path)
	}
	return d
}

// ClassifyCall classifies a tool_use input.
func ClassifyCall(toolName, input string) Display {
	in := DetectInput{ToolName: toolName, Tool: ToolFamily(toolName), Text: input, Call: true}
	d := runDetectors(in)
	finish(&d, in, input)
	return d
}

func finish(d *Display, in DetectInput, raw string) {
	if d.Tool == "" {
		d.Tool = in.Tool
	}
	if d.Title == "" {
		d.Title = in.ToolName
	}
	if d.OriginalBytes == 0 {
		if IsBinary(d.Kind) {
			d.OriginalBytes = len(d.Blob)
		} else {
			d.OriginalBytes = len(raw)
		}
	}
}

// ToolFamily normalizes a provider tool name to a family, "" when the tool
// has no dedicated rendering.
func ToolFamily(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimPrefix(n, "functions.")
	if strings.HasPrefix(n, "mcp__") {
		return ToolMCP
	}
	switch n {
	case "bash", "shell", "exec_command", "local_shell", "run_command", "run_shell_command",
		"execute_command", "terminal", "exec", "container.exec", "write_stdin":
		return ToolBash
	case "read", "read_file", "view", "cat", "readfile":
		return ToolRead
	case "write", "write_file", "create_file", "writefile":
		return ToolWrite
	case "edit", "multiedit", "multi_edit", "str_replace", "str_replace_editor", "edit_file", "replace", "notebookedit":
		return ToolEdit
	case "apply_patch", "patch":
		return ToolPatch
	case "grep", "rg", "ripgrep", "search", "grep_search", "search_files", "codesearch":
		return ToolGrep
	case "glob", "find", "find_files", "list_files", "ls", "list", "list_dir":
		return ToolGlob
	case "websearch", "web_search", "web_search_preview":
		return ToolWebSearch
	case "webfetch", "web_fetch", "fetch":
		return ToolFetch
	case "wick_execute":
		return ToolMCP
	}
	// codex names MCP tools "<server>.<tool>".
	if strings.Contains(n, ".") {
		return ToolMCP
	}
	return ""
}

// ── unwrap ──────────────────────────────────────────────────────────────

// unwrapPayload peels the provider envelope off a payload: a JSON string
// literal becomes its value, a content-block array (or one block) becomes
// its joined text plus any binary blocks. Anything else passes through.
func unwrapPayload(raw string) (string, []Display) {
	t := strings.TrimSpace(raw)
	if len(t) >= 2 && t[0] == '"' && t[len(t)-1] == '"' {
		var s string
		if json.Unmarshal([]byte(t), &s) == nil {
			return unwrapPayload(s)
		}
	}
	if len(t) > 1 && (t[0] == '[' || t[0] == '{') && strings.Contains(t, `"type"`) {
		var blocks []contentBlock
		if t[0] == '{' {
			var b contentBlock
			if json.Unmarshal([]byte(t), &b) == nil && b.isBlock() {
				blocks = []contentBlock{b}
			}
		} else if json.Unmarshal([]byte(t), &blocks) != nil {
			blocks = nil
		}
		if len(blocks) > 0 && allBlocks(blocks) {
			var texts []string
			var bins []Display
			for _, b := range blocks {
				if b.Type == "text" {
					texts = append(texts, b.Text)
					continue
				}
				if d, ok := b.binary(); ok {
					bins = append(bins, d)
				}
			}
			return strings.Join(texts, "\n"), bins
		}
	}
	return raw, nil
}

// contentBlock covers the claude/anthropic and MCP content-block shapes:
//
//	{"type":"text","text":"…"}
//	{"type":"image","source":{"type":"base64","media_type":"image/png","data":"…"}}  (claude)
//	{"type":"image","data":"…","mimeType":"image/png"}                               (MCP)
//	{"type":"document","source":{…application/pdf…}} / {"type":"audio",…}
type contentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Source   *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"source,omitempty"`
}

func (b contentBlock) isBlock() bool {
	switch b.Type {
	case "text", "image", "document", "audio", "video", "resource", "resource_link", "file":
		return true
	}
	return false
}

func allBlocks(bs []contentBlock) bool {
	for _, b := range bs {
		if !b.isBlock() {
			return false
		}
	}
	return true
}

func (b contentBlock) binary() (Display, bool) {
	data, mime := b.Data, b.MimeType
	if b.Source != nil && b.Source.Type == "base64" {
		data, mime = b.Source.Data, b.Source.MediaType
	}
	if data == "" {
		return Display{}, false
	}
	return decodeBinary(data, mime)
}

// ── binary ──────────────────────────────────────────────────────────────

var dataURLRe = regexp.MustCompile(`^data:([a-zA-Z0-9.+/-]+)?(;[^,]*)?;base64,`)

// decodeBinary decodes a base64 payload and returns its binary Display.
// mime is trusted when given, otherwise sniffed from the magic bytes.
func decodeBinary(b64, mime string) (Display, bool) {
	clean := stripSpace(b64)
	blob, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		if blob, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(clean, "=")); err != nil {
			return Display{}, false
		}
	}
	if mime == "" {
		mime = SniffMime(blob)
	}
	kind := binaryKind(mime)
	return Display{
		Kind:          kind,
		Mime:          mime,
		Blob:          blob,
		OriginalBytes: len(blob),
		Name:          defaultName(kind, mime),
		Summary:       fmt.Sprintf("%s · %s", mimeLabel(mime), HumanBytes(len(blob))),
	}, true
}

// SniffMime names a binary by its magic bytes; "application/octet-stream"
// when unknown.
func SniffMime(b []byte) string {
	switch {
	case len(b) >= 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg"
	case len(b) >= 6 && (string(b[:6]) == "GIF87a" || string(b[:6]) == "GIF89a"):
		return "image/gif"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WAVE":
		return "audio/wav"
	case len(b) >= 5 && string(b[:5]) == "%PDF-":
		return "application/pdf"
	case len(b) >= 4 && string(b[:4]) == "OggS":
		return "audio/ogg"
	case len(b) >= 3 && string(b[:3]) == "ID3":
		return "audio/mpeg"
	case len(b) >= 12 && string(b[4:8]) == "ftyp":
		return "video/mp4"
	case len(b) >= 4 && b[0] == 0x1A && b[1] == 0x45 && b[2] == 0xDF && b[3] == 0xA3:
		return "video/webm"
	}
	m := http.DetectContentType(b)
	if strings.HasPrefix(m, "text/") {
		return "application/octet-stream"
	}
	return strings.SplitN(m, ";", 2)[0]
}

func binaryKind(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return KindImage
	case mime == "application/pdf":
		return KindPDF
	case strings.HasPrefix(mime, "audio/"):
		return KindAudio
	case strings.HasPrefix(mime, "video/"):
		return KindVideo
	}
	return KindBinary
}

func mimeLabel(mime string) string {
	sub := mime
	if i := strings.IndexByte(sub, '/'); i >= 0 {
		sub = sub[i+1:]
	}
	sub = strings.TrimPrefix(sub, "x-")
	if i := strings.IndexByte(sub, '+'); i >= 0 {
		sub = sub[:i]
	}
	if sub == "octet-stream" || sub == "" {
		return "BINARY"
	}
	return strings.ToUpper(sub)
}

func defaultName(kind, mime string) string {
	ext := strings.ToLower(mimeLabel(mime))
	if ext == "jpeg" {
		ext = "jpg"
	}
	if ext == "binary" {
		ext = "bin"
	}
	base := kind
	if kind == KindPDF || kind == KindBinary {
		base = "file"
	}
	return base + "." + ext
}

// HumanBytes renders a byte count the way the chip shows it.
func HumanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", (n+512)>>10)
	}
	return fmt.Sprintf("%d B", n)
}

func stripSpace(s string) string {
	if !strings.ContainsAny(s, " \n\r\t") {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, s)
}

// looksBase64 is a cheap pre-check before decoding a whole payload.
func looksBase64(s string) bool {
	if len(s) < 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
			c == '+' || c == '/' || c == '=' || c == '\n' || c == '\r') {
			return false
		}
	}
	return true
}

// binaryMagic reports whether a base64 string decodes to a known binary
// signature — checked on the first 32 chars only, before a full decode.
func binaryMagic(s string) bool {
	head := stripSpace(s)
	if len(head) > 32 {
		head = head[:32]
	}
	b, err := base64.StdEncoding.DecodeString(head)
	if err != nil {
		return false
	}
	switch SniffMime(b) {
	case "application/octet-stream", "application/zip":
		return false
	}
	return true
}

// ── shared helpers for detectors ────────────────────────────────────────

func jsonObject(s string) map[string]any {
	t := strings.TrimSpace(s)
	if len(t) < 2 || t[0] != '{' {
		return nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(t), &m) != nil {
		return nil
	}
	return m
}

func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	if r := []rune(s); len(r) > max {
		s = string(r[:max]) + "…"
	}
	return s
}

var extLang = map[string]string{
	".go": "go", ".ts": "typescript", ".tsx": "tsx", ".js": "javascript", ".jsx": "jsx",
	".mjs": "javascript", ".py": "python", ".rb": "ruby", ".rs": "rust", ".java": "java",
	".kt": "kotlin", ".swift": "swift", ".c": "c", ".h": "c", ".cpp": "cpp", ".cc": "cpp",
	".cs": "csharp", ".php": "php", ".ex": "elixir", ".exs": "elixir", ".sh": "shell",
	".bash": "shell", ".zsh": "shell", ".json": "json", ".yaml": "yaml", ".yml": "yaml",
	".toml": "toml", ".md": "markdown", ".sql": "sql", ".html": "html", ".css": "css",
	".scss": "scss", ".svelte": "svelte", ".vue": "vue", ".templ": "templ", ".xml": "xml",
	".svg": "xml", ".proto": "protobuf", ".lua": "lua", ".tf": "hcl", ".ini": "ini",
}

// LangForPath returns the highlight language for a file path, "" if unknown.
func LangForPath(p string) string {
	base := strings.ToLower(path.Base(strings.ReplaceAll(p, "\\", "/")))
	switch base {
	case "dockerfile":
		return "dockerfile"
	case "makefile":
		return "makefile"
	}
	return extLang[path.Ext(base)]
}
