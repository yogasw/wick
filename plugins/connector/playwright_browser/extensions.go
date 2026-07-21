package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// Chrome extensions for live sessions.
//
// Extensions are stored UNPACKED, one folder per extension, under
// <sessionDir>/extensions/<id>/. On session_open we pass every folder via
// --load-extension (+ --disable-extensions-except to keep the set exact).
//
// Chrome's --load-extension only works in a HEADED browser (classic headless
// ignores extensions), so a session is forced headed whenever any extension is
// installed — see openSession.
//
// Install accepts a .zip or a .crx (uploaded as base64, or a .crx fetched from
// the Web Store by core). A .crx is just a small header followed by a zip, so
// both paths converge on the same unzip.

// extID matches our own extension folder ids: a slug we derive from the upload
// name / store id. Restricted so it can never traverse out of the extensions dir.
var extID = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func validExtID(id string) bool { return id != "" && id != "." && id != ".." && extID.MatchString(id) }

// extensionsDir is <sessionDir>/extensions.
func extensionsDir(c *connector.Ctx) string {
	return filepath.Join(sessionDir(c), "extensions")
}

// installedExtensions returns the absolute paths of every unpacked extension
// folder (those containing a manifest.json). Used to build --load-extension.
func installedExtensions(c *connector.Ctx) []string {
	root := extensionsDir(c)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err == nil {
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	return dirs
}

// extensionList is the extension_list op: one entry per installed extension.
func extensionList(c *connector.Ctx) (any, error) {
	root := extensionsDir(c)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{"extensions": []any{}, "count": 0}, nil
		}
		return nil, err
	}
	list := make([]map[string]any, 0)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		manifest := filepath.Join(dir, "manifest.json")
		info, err := os.Stat(manifest)
		if err != nil {
			continue // not an unpacked extension
		}
		name, version := readManifestMeta(manifest)
		list = append(list, map[string]any{
			"id":      e.Name(),
			"name":    name,
			"version": version,
			"size":    dirSize(dir),
			"loaded":  info != nil,
		})
	}
	sort.Slice(list, func(i, j int) bool { return list[i]["id"].(string) < list[j]["id"].(string) })
	return map[string]any{"extensions": list, "count": len(list)}, nil
}

// extensionRemove is the extension_remove op: delete one unpacked extension.
func extensionRemove(c *connector.Ctx, id string) (any, error) {
	if !validExtID(id) {
		return nil, fmt.Errorf("invalid extension id %q", id)
	}
	dir := filepath.Join(extensionsDir(c), id)
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("no extension %q", id)
	}
	if err := os.RemoveAll(dir); err != nil {
		return nil, fmt.Errorf("remove extension: %w", err)
	}
	return map[string]any{"id": id, "removed": true, "note": "Applies to new sessions; already-running sessions are unaffected."}, nil
}

// extensionInstall is the extension_install op. It unpacks a base64 .zip or .crx
// into <sessionDir>/extensions/<id>/. id is the caller-provided slug (from the
// upload filename or the store id); data_b64 is the archive bytes.
func extensionInstall(c *connector.Ctx, id, dataB64 string) (any, error) {
	if !validExtID(id) {
		return nil, fmt.Errorf("invalid extension id %q", id)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(dataB64))
	if err != nil {
		return nil, fmt.Errorf("decode upload: %w", err)
	}
	zipBytes, err := toZipBytes(raw)
	if err != nil {
		return nil, err
	}

	dest := filepath.Join(extensionsDir(c), id)
	// Fresh unpack: clear any previous version of the same id.
	_ = os.RemoveAll(dest)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, fmt.Errorf("create extension dir: %w", err)
	}
	if err := unzipInto(zipBytes, dest); err != nil {
		_ = os.RemoveAll(dest)
		return nil, err
	}
	// A valid unpacked extension must have a manifest. Some zips wrap everything
	// in a single top-level folder — if so, lift that folder's contents up.
	if _, err := os.Stat(filepath.Join(dest, "manifest.json")); err != nil {
		if err := liftSingleTopDir(dest); err != nil {
			_ = os.RemoveAll(dest)
			return nil, err
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "manifest.json")); err != nil {
		_ = os.RemoveAll(dest)
		return nil, fmt.Errorf("archive has no manifest.json — not a Chrome extension")
	}

	name, version := readManifestMeta(filepath.Join(dest, "manifest.json"))
	return map[string]any{
		"id":      id,
		"name":    name,
		"version": version,
		"note":    "Applies to new sessions; open a new live session to load it.",
	}, nil
}

// toZipBytes returns the zip payload from either a raw .zip or a .crx. A .crx3
// file is "Cr24" + uint32 version + uint32 headerLen + header + zip; .crx2 is
// "Cr24" + version + uint32 pubkeyLen + uint32 sigLen + pubkey + sig + zip.
func toZipBytes(raw []byte) ([]byte, error) {
	if len(raw) >= 2 && raw[0] == 'P' && raw[1] == 'K' {
		return raw, nil // already a zip
	}
	if len(raw) < 16 || string(raw[0:4]) != "Cr24" {
		return nil, errors.New("unrecognized archive: expected a .zip or a .crx (Cr24) file")
	}
	version := binary.LittleEndian.Uint32(raw[4:8])
	switch version {
	case 3:
		headerLen := binary.LittleEndian.Uint32(raw[8:12])
		off := 12 + int(headerLen)
		if off > len(raw) {
			return nil, errors.New("crx3: header length exceeds file")
		}
		return raw[off:], nil
	case 2:
		pubLen := binary.LittleEndian.Uint32(raw[8:12])
		sigLen := binary.LittleEndian.Uint32(raw[12:16])
		off := 16 + int(pubLen) + int(sigLen)
		if off > len(raw) {
			return nil, errors.New("crx2: header length exceeds file")
		}
		return raw[off:], nil
	default:
		return nil, fmt.Errorf("unsupported crx version %d", version)
	}
}

// unzipInto extracts a zip archive into dest, rejecting zip-slip paths.
func unzipInto(zipBytes []byte, dest string) error {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return fmt.Errorf("read archive as zip: %w", err)
	}
	destAbs, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		// Reject absolute paths and any entry that escapes dest (zip-slip).
		target := filepath.Join(dest, f.Name)
		targetAbs, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		if targetAbs != destAbs && !strings.HasPrefix(targetAbs, destAbs+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe path in archive: %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := writeZipFile(f, target); err != nil {
			return err
		}
	}
	return nil
}

func writeZipFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	// Cap per-file size to guard against a decompression bomb (256 MiB).
	_, err = io.Copy(out, io.LimitReader(rc, 256<<20))
	return err
}

// liftSingleTopDir handles archives that wrap the extension in one top-level
// folder (dest/<only-dir>/manifest.json). It moves that folder's contents up
// into dest so --load-extension finds the manifest at the root.
func liftSingleTopDir(dest string) error {
	entries, err := os.ReadDir(dest)
	if err != nil {
		return err
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return nil // not the single-wrapper case
	}
	inner := filepath.Join(dest, entries[0].Name())
	innerEntries, err := os.ReadDir(inner)
	if err != nil {
		return err
	}
	for _, e := range innerEntries {
		from := filepath.Join(inner, e.Name())
		to := filepath.Join(dest, e.Name())
		if err := os.Rename(from, to); err != nil {
			return err
		}
	}
	return os.RemoveAll(inner)
}

// readManifestMeta pulls name + version out of a manifest.json (best-effort).
func readManifestMeta(path string) (name, version string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	var m struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	_ = json.Unmarshal(b, &m)
	return m.Name, m.Version
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}
