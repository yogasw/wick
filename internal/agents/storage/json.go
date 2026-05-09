// Package storage holds the filesystem primitives used by every other
// agents subpackage: atomic JSON write, JSONL append/read/tail/truncate,
// directory scan, and identifier validation. No agents-domain logic
// lives here — only generic on-disk helpers.
package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// WriteJSON writes v as indented JSON to path atomically: write to a
// tmp file in the same directory, fsync, then rename over path.
//
// On POSIX `os.Rename` is atomic; on Windows it is also atomic when the
// target exists (and replaces it). Parent directories are created on
// demand.
func WriteJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// ReadJSON loads a JSON file into v. Forwards os.ErrNotExist verbatim
// so callers can distinguish "missing" from "corrupt".
func ReadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
