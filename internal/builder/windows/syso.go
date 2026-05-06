// Package windows handles Windows-specific build steps — currently
// the .syso resource that embeds the brand icon plus version metadata
// into the .exe. Compiles on every host (no Windows-only deps), so
// cross-builds from mac/linux still produce a metadata-rich .exe.
package windows

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/josephspurrier/goversioninfo"

	"github.com/yogasw/wick/internal/systemtray"
)

// EmbedResource generates a COFF .syso resource alongside main.go so
// the next `go build` for GOOS=windows picks up the brand W icon plus
// version metadata (FileDescription, FileVersion, ProductName, etc.).
//
// Returns a cleanup func the caller defers — once `go build` finishes,
// the temporary .ico and the rsrc_windows_<arch>.syso file are removed.
//
// outputPath is used only as the OriginalFilename hint inside the
// version resource; nothing is written there.
func EmbedResource(outputPath, appName, appVersion string) (func(), error) {
	goarch := os.Getenv("GOARCH")
	if goarch == "" {
		goarch = runtime.GOARCH
	}

	tmp, err := os.CreateTemp("", "wick-icon-*.ico")
	if err != nil {
		return nil, fmt.Errorf("icon temp: %w", err)
	}
	if _, err := tmp.Write(systemtray.BrandIcon(true)); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, fmt.Errorf("write icon: %w", err)
	}
	tmp.Close()
	icoPath := tmp.Name()

	sysoPath := fmt.Sprintf("rsrc_windows_%s.syso", goarch)
	maj, min, pat := parseSemver(appVersion)
	vi := &goversioninfo.VersionInfo{
		IconPath: icoPath,
		FixedFileInfo: goversioninfo.FixedFileInfo{
			FileVersion:    goversioninfo.FileVersion{Major: maj, Minor: min, Patch: pat},
			ProductVersion: goversioninfo.FileVersion{Major: maj, Minor: min, Patch: pat},
			FileFlagsMask:  "3f",
			FileFlags:      "00",
			FileOS:         "040004",
			FileType:       "01",
			FileSubType:    "00",
		},
		StringFileInfo: goversioninfo.StringFileInfo{
			FileDescription:  appName,
			FileVersion:      appVersion,
			InternalName:     appName,
			OriginalFilename: filepath.Base(outputPath),
			ProductName:      appName,
			ProductVersion:   appVersion,
			LegalCopyright:   fmt.Sprintf("Copyright © %d %s", time.Now().Year(), appName),
		},
		VarFileInfo: goversioninfo.VarFileInfo{
			Translation: goversioninfo.Translation{LangID: 0x0409, CharsetID: 0x04B0},
		},
	}
	vi.Build()
	vi.Walk()
	if err := vi.WriteSyso(sysoPath, goarch); err != nil {
		os.Remove(icoPath)
		return nil, fmt.Errorf("write syso: %w", err)
	}

	return func() {
		os.Remove(icoPath)
		os.Remove(sysoPath)
	}, nil
}

// parseSemver pulls the leading major.minor.patch out of a version
// string. Tolerates a leading "v" and any -suffix / +metadata; missing
// segments default to 0.
func parseSemver(v string) (major, minor, patch int) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	get := func(i int) int {
		if i >= len(parts) {
			return 0
		}
		n, _ := strconv.Atoi(parts[i])
		return n
	}
	return get(0), get(1), get(2)
}
