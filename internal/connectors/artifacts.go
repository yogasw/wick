package connectors

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/yogasw/wick/pkg/connector"
)

// Files an op returns under connector.FileOutputKey land in
// <session cwd>/artifacts/. The session → cwd mapping lives in the agents
// package, which registers it at boot.

var (
	artifactDirMu       sync.RWMutex
	artifactDirResolver func(sessionID string) (string, error)
)

// SetArtifactDirResolver registers how to find an agent session's working
// directory. nil disables file outputs.
func SetArtifactDirResolver(fn func(sessionID string) (string, error)) {
	artifactDirMu.Lock()
	artifactDirResolver = fn
	artifactDirMu.Unlock()
}

const maxFileOutputBytes = 60 << 20

var (
	fileFormats  = map[string]bool{"csv": true, "tsv": true, "json": true, "ndjson": true, "txt": true}
	unsafeNameRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)

// extractFileOutput writes the file an op attached under FileOutputKey (in
// the already-masked response) to the session's artifacts/ folder and
// returns the response with a FileRef in its place. A response without a
// file is returned unchanged.
func extractFileOutput(respJSON, sessionID string) (string, error) {
	if !strings.Contains(respJSON, `"`+connector.FileOutputKey+`"`) {
		return respJSON, nil
	}
	dec := json.NewDecoder(strings.NewReader(respJSON))
	dec.UseNumber()
	var top map[string]any
	if err := dec.Decode(&top); err != nil {
		return respJSON, nil // not an object: nothing to extract
	}
	raw, ok := top[connector.FileOutputKey]
	if !ok {
		return respJSON, nil
	}
	b, _ := json.Marshal(raw)
	var f connector.FileOutput
	if err := json.Unmarshal(b, &f); err != nil {
		return "", fmt.Errorf("file output is malformed: %w", err)
	}
	ref, err := writeArtifact(sessionID, f)
	if err != nil {
		return "", err
	}
	delete(top, connector.FileOutputKey)
	top[connector.FileRefKey] = ref
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(top); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

func writeArtifact(sessionID string, f connector.FileOutput) (connector.FileRef, error) {
	format := strings.ToLower(strings.TrimSpace(f.Format))
	if !fileFormats[format] {
		return connector.FileRef{}, fmt.Errorf("file format %q is not supported (csv, tsv, json, ndjson, txt)", f.Format)
	}
	if len(f.Content) > maxFileOutputBytes {
		return connector.FileRef{}, fmt.Errorf("file is %d MB, over the %d MB limit; narrow the query", len(f.Content)>>20, maxFileOutputBytes>>20)
	}
	if strings.TrimSpace(sessionID) == "" {
		return connector.FileRef{}, errors.New("this op writes a file into the agent session's artifacts/ folder; call it from an agent session")
	}
	artifactDirMu.RLock()
	resolve := artifactDirResolver
	artifactDirMu.RUnlock()
	if resolve == nil {
		return connector.FileRef{}, errors.New("file outputs are not available on this wick")
	}
	cwd, err := resolve(sessionID)
	if err != nil || cwd == "" {
		return connector.FileRef{}, fmt.Errorf("cannot find the working directory of session %s: %v", sessionID, err)
	}
	dir := filepath.Join(cwd, "artifacts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return connector.FileRef{}, fmt.Errorf("create artifacts folder: %w", err)
	}

	base := filepath.Base(strings.TrimSpace(f.Name))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.Trim(unsafeNameRe.ReplaceAllString(base, "-"), "-.")
	if base == "" {
		base = "export"
	}
	if len(base) > 80 {
		base = base[:80]
	}
	name := base + "." + format
	for i := 2; ; i++ {
		fh, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			if i > 999 {
				return connector.FileRef{}, errors.New("too many files with the same name in artifacts/")
			}
			name = base + "-" + strconv.Itoa(i) + "." + format
			continue
		}
		if err != nil {
			return connector.FileRef{}, fmt.Errorf("create %s: %w", name, err)
		}
		_, werr := fh.WriteString(f.Content)
		cerr := fh.Close()
		if werr != nil || cerr != nil {
			_ = os.Remove(filepath.Join(dir, name))
			return connector.FileRef{}, fmt.Errorf("write %s: %v", name, errors.Join(werr, cerr))
		}
		break
	}
	return connector.FileRef{Path: "artifacts/" + name, Format: format, Bytes: len(f.Content)}, nil
}
