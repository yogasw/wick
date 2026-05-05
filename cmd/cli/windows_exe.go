package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/josephspurrier/goversioninfo"

	"github.com/yogasw/wick/internal/systemtray"
)

// embedWindowsResource generates a COFF .syso resource alongside main.go
// so the next `go build` for GOOS=windows picks up the brand W icon plus
// version metadata (FileDescription, FileVersion, ProductName, etc.).
//
// Returns a cleanup func the caller defers — once `go build` finishes,
// the temporary .ico and the rsrc_windows_<arch>.syso file are removed.
//
// outputPath is used only as the OriginalFilename hint inside the version
// resource; nothing is written there.
func embedWindowsResource(outputPath, appName, appVersion string) (func(), error) {
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
