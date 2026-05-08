package storage

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// jsonlMetaLine is the header line written first to every jsonl file.
// Readers must skip lines whose first JSON key is "_meta".
type jsonlMetaLine struct {
	Meta jsonlMeta `json:"_meta"`
}

type jsonlMeta struct {
	Version int    `json:"version"`
	Format  string `json:"format"`
	Session string `json:"session,omitempty"`
}

// AppendJSONL appends one JSON-encoded line to path. Creates the file
// (with header `_meta` line) if missing. Each call opens, writes,
// fsyncs, closes — safe for concurrent appenders within one process
// given each write is one syscall < PIPE_BUF on POSIX. Cross-process
// safety is not promised; agents owns its files.
func AppendJSONL(path string, format, sessionID string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	created := false
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		created = true
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if created {
		header, err := json.Marshal(jsonlMetaLine{Meta: jsonlMeta{Version: 1, Format: format, Session: sessionID}})
		if err != nil {
			return err
		}
		if _, err := f.Write(append(header, '\n')); err != nil {
			return err
		}
	}
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// ReadJSONL streams every non-meta line in path to emit. emit returns
// false to stop early. A missing file is treated as "no entries yet"
// (returns nil) — callers that care about the difference should stat
// the file themselves.
func ReadJSONL(path string, emit func(line []byte) bool) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if isMetaLine(line) {
			continue
		}
		buf := make([]byte, len(line))
		copy(buf, line)
		if !emit(buf) {
			return nil
		}
	}
	return scanner.Err()
}

// TailJSONL returns the last n non-meta lines via emit, in file order.
// Naive (full scan + ring buffer) but adequate for the modest line
// counts we expect per session (<10k).
func TailJSONL(path string, n int, emit func(line []byte)) error {
	if n <= 0 {
		return nil
	}
	all := make([][]byte, 0, n)
	if err := ReadJSONL(path, func(line []byte) bool {
		buf := make([]byte, len(line))
		copy(buf, line)
		all = append(all, buf)
		if len(all) > n {
			all = all[1:]
		}
		return true
	}); err != nil {
		return err
	}
	for _, line := range all {
		emit(line)
	}
	return nil
}

// CountJSONLEntries returns how many non-meta lines exist in path.
func CountJSONLEntries(path string) (int, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	count := 0
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && !isMetaLine(line) {
			count++
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, err
		}
	}
	return count, nil
}

// TruncateJSONL clears the body of path while keeping a fresh `_meta`
// header. Used by session reset.
func TruncateJSONL(path, format, sessionID string) error {
	header, err := json.Marshal(jsonlMetaLine{Meta: jsonlMeta{Version: 1, Format: format, Session: sessionID}})
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(header, '\n'), 0o644)
}

// isMetaLine reports whether a jsonl line is the header `_meta` entry.
// Cheap prefix check beats a full Unmarshal for every read.
func isMetaLine(line []byte) bool {
	trim := line
	for len(trim) > 0 && (trim[0] == ' ' || trim[0] == '\t') {
		trim = trim[1:]
	}
	return len(trim) >= 8 && string(trim[:8]) == `{"_meta"`
}
