package plugin

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// InstallFromDir verifies the {binary, plugin.json} in srcDir and copies them
// into destRoot/<key>/. The manifest's sha256 + signature are checked against
// the binary BEFORE anything is written, so an unverified plugin never lands in
// the plugins dir.
func InstallFromDir(srcDir, destRoot string) error {
	raw, err := os.ReadFile(filepath.Join(srcDir, "plugin.json"))
	if err != nil {
		return fmt.Errorf("read plugin.json: %w", err)
	}
	var env wickplugin.Manifest
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("parse plugin.json: %w", err)
	}
	key := env.Module.Meta.Key
	// Enforce the slug rule here too — a hand-written or third-party manifest
	// could carry a key the build never checked, and key becomes the install
	// dir name. Rejects empty, too-long, and path-traversal/illegal-char keys.
	if err := wickplugin.ValidateKey(key); err != nil {
		return err
	}
	entry := env.Entry
	if entry == "" {
		entry = key
	}
	binSrc := filepath.Join(srcDir, entry)
	if err := wickplugin.VerifyManifest(env, binSrc); err != nil {
		return fmt.Errorf("verify %q: %w", key, err)
	}
	destDir := filepath.Join(destRoot, key)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create plugin dir: %w", err)
	}
	if err := copyFile(binSrc, filepath.Join(destDir, entry), 0o755); err != nil {
		return fmt.Errorf("copy binary: %w", err)
	}
	if err := copyFile(filepath.Join(srcDir, "plugin.json"), filepath.Join(destDir, "plugin.json"), 0o644); err != nil {
		return fmt.Errorf("copy manifest: %w", err)
	}
	return nil
}

// Phase names an install step, streamed to the caller so the UI can show a
// staged progress indicator instead of one opaque spinner.
type Phase string

const (
	PhaseDownloading Phase = "downloading"
	PhaseVerifying   Phase = "verifying"
	PhaseReplacing   Phase = "replacing"
	PhaseDone        Phase = "done"
)

// Progress is one install-progress update. Pct is the download percentage
// (0–100) and is only meaningful while Phase==PhaseDownloading and the server
// sent a Content-Length; it is -1 when the total size is unknown.
type Progress struct {
	Phase Phase `json:"phase"`
	Pct   int   `json:"pct"`
}

// ProgressFunc receives install-progress updates. nil is allowed (no-op).
type ProgressFunc func(Progress)

func (f ProgressFunc) emit(p Progress) {
	if f != nil {
		f(p)
	}
}

// InstallFromURL downloads an archive (zip or tar.gz) from url, extracts it,
// verifies the manifest, and installs into destRoot. Used by the marketplace
// install action and `<app> plugin install <url|name>`.
func InstallFromURL(ctx context.Context, url, destRoot string) error {
	return InstallFromURLProgress(ctx, url, destRoot, nil)
}

// InstallFromURLProgress is InstallFromURL with staged progress reporting.
// The archive lands in a temp dir first; the on-disk plugin is only touched in
// the Replacing phase, so a failed download/verify never disturbs the existing
// install (download-succeeds-then-replace).
func InstallFromURLProgress(ctx context.Context, url, destRoot string, onProgress ProgressFunc) error {
	pf := ProgressFunc(onProgress)
	tmp, err := os.MkdirTemp("", "wick-plugin-dl-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	pf.emit(Progress{Phase: PhaseDownloading, Pct: 0})
	archive := filepath.Join(tmp, "download")
	if err := downloadTo(ctx, url, archive, pf); err != nil {
		return err
	}

	pf.emit(Progress{Phase: PhaseVerifying, Pct: 100})
	dir, err := ExtractArchive(archive, tmp)
	if err != nil {
		return err
	}

	pf.emit(Progress{Phase: PhaseReplacing, Pct: 100})
	if err := InstallFromDir(dir, destRoot); err != nil {
		return err
	}

	pf.emit(Progress{Phase: PhaseDone, Pct: 100})
	return nil
}

// ResolveSource turns a path / url / archive into a directory containing
// {binary, plugin.json}, returning the dir and a cleanup func. A bare existing
// directory is returned as-is.
func ResolveSource(ctx context.Context, src string) (string, func(), error) {
	noop := func() {}
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		tmp, err := os.MkdirTemp("", "wick-plugin-dl-*")
		if err != nil {
			return "", noop, err
		}
		archive := filepath.Join(tmp, "download")
		if err := downloadTo(ctx, src, archive, nil); err != nil {
			os.RemoveAll(tmp)
			return "", noop, err
		}
		dir, err := ExtractArchive(archive, tmp)
		if err != nil {
			os.RemoveAll(tmp)
			return "", noop, err
		}
		return dir, func() { os.RemoveAll(tmp) }, nil
	}
	info, err := os.Stat(src)
	if err != nil {
		return "", noop, err
	}
	if info.IsDir() {
		return src, noop, nil
	}
	tmp, err := os.MkdirTemp("", "wick-plugin-x-*")
	if err != nil {
		return "", noop, err
	}
	dir, err := ExtractArchive(src, tmp)
	if err != nil {
		os.RemoveAll(tmp)
		return "", noop, err
	}
	return dir, func() { os.RemoveAll(tmp) }, nil
}

// copyFile writes src to dst atomically: it copies into a sibling temp file
// and renames it over dst. The rename matters for the binary — overwriting a
// running executable in place fails with ETXTBSY ("text file busy") on
// Linux/Termux, but rename() only swaps the directory entry, leaving the
// running process holding the old inode. So an update-while-running succeeds
// and the new binary takes effect on the next spawn.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	// Temp file in the destination dir so the rename stays on one filesystem
	// (cross-device rename would EXDEV). Unique-ish name via the dst base.
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// On any failure past this point, don't leave the temp behind.
	defer os.Remove(tmpName)

	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	// Atomic replace — succeeds even if dst is a running executable.
	return os.Rename(tmpName, dst)
}

func downloadTo(ctx context.Context, url, dst string, pf ProgressFunc) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()

	// Wrap the body so we can report download percentage. When the server
	// omits Content-Length (total <= 0), pct is reported as -1 (indeterminate)
	// and the UI falls back to a striped/indeterminate bar.
	src := io.Reader(resp.Body)
	if pf != nil {
		src = &progressReader{r: resp.Body, total: resp.ContentLength, pf: pf}
	}
	_, err = io.Copy(f, src)
	return err
}

// progressReader wraps a download body and emits a PhaseDownloading Progress as
// bytes flow. It throttles to whole-percent changes so a large download doesn't
// flood the SSE stream. total <= 0 means unknown size → pct -1.
type progressReader struct {
	r       io.Reader
	total   int64
	read    int64
	lastPct int
	pf      ProgressFunc
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.read += int64(n)
		pct := -1
		if p.total > 0 {
			pct = min(int(p.read*100/p.total), 100)
		}
		if pct != p.lastPct {
			p.lastPct = pct
			p.pf.emit(Progress{Phase: PhaseDownloading, Pct: pct})
		}
	}
	return n, err
}

// ExtractArchive extracts a .tar.gz or .zip into destBase and returns the
// directory that holds plugin.json (the archive root or its single subdir).
func ExtractArchive(archive, destBase string) (string, error) {
	out := filepath.Join(destBase, "x")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}
	switch {
	case strings.HasSuffix(archive, ".zip"):
		if err := extractZip(archive, out); err != nil {
			return "", err
		}
	case strings.HasSuffix(archive, ".tar.gz"), strings.HasSuffix(archive, ".tgz"), strings.HasSuffix(archive, "download"):
		// Sniff: a downloaded asset has no extension. Try zip first (our build
		// output), then tar.gz.
		if err := extractZip(archive, out); err != nil {
			if terr := extractTarGz(archive, out); terr != nil {
				return "", fmt.Errorf("archive is neither zip nor tar.gz: %v / %v", err, terr)
			}
		}
	default:
		return "", fmt.Errorf("unsupported archive (use .tar.gz or .zip): %s", archive)
	}
	return findManifestDir(out)
}

func findManifestDir(root string) (string, error) {
	if _, err := os.Stat(filepath.Join(root, "plugin.json")); err == nil {
		return root, nil
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(root, e.Name(), "plugin.json")); err == nil {
				return filepath.Join(root, e.Name()), nil
			}
		}
	}
	return "", fmt.Errorf("plugin.json not found in archive")
}

func extractTarGz(archive, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.Clean("/"+h.Name)) // zip-slip guard
		if h.FileInfo().IsDir() {
			os.MkdirAll(target, 0o755)
			continue
		}
		os.MkdirAll(filepath.Dir(target), 0o755)
		w, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode))
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, tr); err != nil { //nolint:gosec // bounded by archive
			w.Close()
			return err
		}
		w.Close()
	}
}

func extractZip(archive, dst string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		target := filepath.Join(dst, filepath.Clean("/"+zf.Name)) // zip-slip guard
		if zf.FileInfo().IsDir() {
			os.MkdirAll(target, 0o755)
			continue
		}
		os.MkdirAll(filepath.Dir(target), 0o755)
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		w, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, zf.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(w, rc) //nolint:gosec // bounded by archive
		w.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
