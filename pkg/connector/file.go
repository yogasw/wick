package connector

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// FileOutputKey is the reserved result key an op uses to hand wick a file
// instead of data for the caller to read (an export, a report). wick takes
// it out of the result after masking, writes it to artifacts/<name> under
// the calling agent session's working directory (creating artifacts/ when
// missing), and puts a FileRef in its place — so the content never reaches
// the agent or the run log, only the path does.
//
// Text (CSV, TSV, JSON, NDJSON, …) goes in Content as a plain JSON string
// on purpose: the host's masking sweep covers JSON strings, so every value
// the op passed to c.Mask is masked inside the file too. Binary files (a
// downloaded image, PDF, video, archive) go in ContentBase64 instead; wick
// decodes it once on the host and writes the raw bytes, so the agent gets
// the path and never the encoded content. An op running inside wick sets
// Data (raw bytes) instead: wick writes it as is, and it is encoded as
// content_base64 only when it has to cross the plugin transport. Masking
// does not reach inside binary content (ContentBase64 or Data), so use them
// for downloaded files, not for values an op built from secrets.
const FileOutputKey = "wick_file"

// FileRefKey is where the written file is described in the result wick
// returns.
const FileRefKey = "file"

// FileOutput is the file an op returns under FileOutputKey.
type FileOutput struct {
	// Name is the file name only (no directories); wick sanitizes it and
	// adds a numeric suffix rather than overwrite an existing file.
	Name string `json:"name"`
	// Format is the extension without the dot. Built in — text: csv, tsv,
	// json, ndjson, txt; binary: png, jpg, jpeg, gif, webp, pdf, mp4, mov,
	// webm, mp3, ogg, wav, zip, docx, xlsx, pptx. The admin may allow more
	// (setting file_io.allowed_formats), never scripts or executables.
	Format string `json:"format"`
	// Exactly one of Content (text, masked), ContentBase64 (bytes as
	// standard or URL-safe base64) and Data (raw bytes) is set. A text
	// format may also come as bytes (a CSV that is not UTF-8); those bytes
	// are written as they are, unmasked.
	Content       string `json:"content,omitempty"`
	ContentBase64 string `json:"content_base64,omitempty"`
	// Data never appears in JSON as such: MarshalJSON turns it into
	// content_base64 for the plugin transport.
	Data []byte `json:"-"`
}

// MarshalJSON encodes Data as content_base64 so a file an op returns from
// a plugin process reaches wick intact.
func (f FileOutput) MarshalJSON() ([]byte, error) {
	type plain FileOutput
	p := plain(f)
	if len(f.Data) > 0 && p.ContentBase64 == "" {
		p.ContentBase64 = base64.StdEncoding.EncodeToString(f.Data)
	}
	return json.Marshal(p)
}

// HasBinary reports whether the file carries bytes (Data or ContentBase64)
// rather than masked text.
func (f FileOutput) HasBinary() bool { return len(f.Data) > 0 || f.ContentBase64 != "" }

// FileRef is what replaces FileOutput in the result the caller sees.
type FileRef struct {
	// Path is relative to the session's working directory, e.g.
	// "artifacts/apps-20261014-1150.csv".
	Path   string `json:"path"`
	Format string `json:"format"`
	Bytes  int    `json:"bytes"`
}

var (
	textFileFormats = map[string]bool{"csv": true, "tsv": true, "json": true, "ndjson": true, "txt": true}
	// binaryFileFormats are media, documents and archives that nothing on
	// the host opens or runs by itself. Active content (html, svg, js) and
	// anything executable (sh, exe, py, …) stay off the list on purpose.
	binaryFileFormats = map[string]bool{
		"png": true, "jpg": true, "jpeg": true, "gif": true, "webp": true,
		"pdf": true, "mp4": true, "mov": true, "webm": true,
		"mp3": true, "ogg": true, "wav": true,
		"zip": true, "docx": true, "xlsx": true, "pptx": true,
	}
	// deniedFileFormats are scripts and executables. They are never
	// written, whatever the admin's allowed-formats setting says.
	deniedFileFormats = map[string]bool{
		"exe": true, "dll": true, "so": true, "dylib": true, "bat": true, "cmd": true,
		"com": true, "msi": true, "sh": true, "bash": true, "zsh": true, "ps1": true,
		"vbs": true, "jar": true, "apk": true, "app": true, "bin": true, "py": true,
		"rb": true, "pl": true, "php": true, "js": true, "mjs": true, "cgi": true,
		"phtml": true, "phar": true, "php5": true, "jsp": true, "asp": true, "aspx": true,
		"hta": true, "scr": true, "pif": true, "lnk": true, "cpl": true, "msp": true,
		"reg": true, "wsf": true, "vbe": true, "jse": true, "psm1": true, "cjs": true,
		"ts": true, "desktop": true, "service": true, "appimage": true, "deb": true, "rpm": true,
		"run": true, "command": true, "csh": true, "ksh": true, "fish": true, "lua": true,
		"pyc": true, "pyw": true, "xhtml": true, "shtml": true, "svgz": true, "xsl": true,
	}
)

// DefaultFileFormats is the built-in allowed list, used while the admin's
// allowed-formats setting is empty or unreadable.
const DefaultFileFormats = "csv,tsv,json,ndjson,txt,png,jpg,jpeg,gif,webp,pdf,mp4,mov,webm,mp3,ogg,wav,zip,docx,xlsx,pptx"

// Binary file size limit: DefaultMaxBinaryMB while the admin setting is
// empty or invalid, never more than MaxBinaryMBCeiling whatever it says.
const (
	DefaultMaxBinaryMB = 50
	MaxBinaryMBCeiling = 200
)

var (
	maxBinaryMu sync.RWMutex
	maxBinaryFn func() int
)

// SetMaxBinaryBytesFunc lets the host publish its current binary file
// limit (the admin setting) to connectors running in its process.
func SetMaxBinaryBytesFunc(fn func() int) {
	maxBinaryMu.Lock()
	maxBinaryFn = fn
	maxBinaryMu.Unlock()
}

// MaxBinaryBytes is the largest file the host will write, so a connector
// can refuse a download before it starts. In a plugin process, where the
// host setting cannot be read, it is the default (DefaultMaxBinaryMB).
func MaxBinaryBytes() int {
	maxBinaryMu.RLock()
	fn := maxBinaryFn
	maxBinaryMu.RUnlock()
	if fn == nil {
		return DefaultMaxBinaryMB << 20
	}
	return fn()
}

var fileFormatRe = regexp.MustCompile(`^[a-z0-9]{1,10}$`)

// ParseFileFormats turns a comma-separated format list into a set: lower
// case, leading dots dropped. Malformed entries and denied (script or
// executable) formats are left out of the set and returned separately.
func ParseFileFormats(v string) (set map[string]bool, denied, invalid []string) {
	set = map[string]bool{}
	for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		f := strings.TrimLeft(strings.ToLower(part), ".")
		switch {
		case f == "":
		case !fileFormatRe.MatchString(f):
			invalid = append(invalid, part)
		case deniedFileFormats[f]:
			denied = append(denied, f)
		default:
			set[f] = true
		}
	}
	return set, denied, invalid
}

// ParseMaxBinaryMB reads a size setting in MB: DefaultMaxBinaryMB when
// empty or not a positive number, MaxBinaryMBCeiling at most.
func ParseMaxBinaryMB(v string) int {
	mb, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || mb <= 0 {
		return DefaultMaxBinaryMB
	}
	return min(mb, MaxBinaryMBCeiling)
}

// ValidateFileFormats rejects a format list that names something the
// writer would ignore, so the admin sees it instead of a silent drop.
func ValidateFileFormats(v string) error {
	set, denied, invalid := ParseFileFormats(v)
	for _, w := range []string{"all", "any", "none"} {
		if set[w] {
			return fmt.Errorf("%q is not a file extension: list each format (e.g. png,pdf), or leave empty for the built-in list", w)
		}
	}
	if len(denied) > 0 {
		return fmt.Errorf("%s: scripts and executables can never be allowed", strings.Join(denied, ", "))
	}
	if len(invalid) > 0 {
		return fmt.Errorf("not a file extension: %s (letters and digits only, e.g. png)", strings.Join(invalid, ", "))
	}
	return nil
}

// ValidateMaxBinaryMB accepts empty (the default) or 1..MaxBinaryMBCeiling.
func ValidateMaxBinaryMB(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if mb, err := strconv.Atoi(v); err != nil || mb <= 0 || mb > MaxBinaryMBCeiling {
		return fmt.Errorf("must be a whole number of MB from 1 to %d", MaxBinaryMBCeiling)
	}
	return nil
}

// FileFormat reports whether format (an extension without the dot, any
// case) is on the built-in list, and whether it is binary — carried in
// ContentBase64 — rather than text carried in Content. The host may allow
// more formats through the admin setting.
func FileFormat(format string) (builtin, binary bool) {
	format = strings.ToLower(strings.TrimSpace(format))
	if binaryFileFormats[format] {
		return true, true
	}
	return textFileFormats[format], false
}

// FileFormatDenied reports whether format is a script or executable type
// that is never written, even when an admin adds it to the allowed list.
func FileFormatDenied(format string) bool {
	return deniedFileFormats[strings.ToLower(strings.TrimSpace(format))]
}

// WithFile attaches f to result under FileOutputKey and returns result.
func WithFile(result map[string]any, f FileOutput) map[string]any {
	if result == nil {
		result = map[string]any{}
	}
	result[FileOutputKey] = f
	return result
}
