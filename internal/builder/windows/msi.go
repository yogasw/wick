package windows

import (
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// ErrSkippedMSI is returned when MSI creation is skipped because the
// host has no usable MSI builder on PATH (`wixl` from msitools).
// Callers may treat this as a non-fatal warning — the .exe alone is
// still a valid distributable.
var ErrSkippedMSI = errors.New("msi: wixl not found on PATH")

// PackageMSI wraps a freshly compiled .exe into a Windows Installer
// .msi package via `wixl` (msitools).
//
// The MSI is always per-user: installs to
// %LocalAppData%\Programs\<AppName>\<AppName>.exe, registers a Start
// Menu shortcut and an Add/Remove Programs entry, and never adds the
// app to autostart (that stays an in-app toggle). Per-user matters
// because it skips the UAC prompt and — critically — leaves the .exe
// in a location the running app can rewrite, so the in-app
// self-updater keeps working on installed builds the same way it
// works on portable .exe builds.
//
// Returns the .msi path on success; returns ErrSkippedMSI when wixl
// is not on PATH (mirrors the darwin .dmg skip pattern so cross-builds
// from hosts without wixl still produce a usable .exe).
func PackageMSI(exePath, appName, appVersion, goarch string) (string, error) {
	if _, err := exec.LookPath("wixl"); err != nil {
		return "", ErrSkippedMSI
	}

	wixArch, win64 := mapGoArchToWix(goarch)
	verSlug := strings.TrimPrefix(strings.TrimSpace(appVersion), "v")
	msiPath := filepath.Join(filepath.Dir(exePath), fmt.Sprintf("%s-%s-windows-%s.msi", appName, verSlug, goarch))

	maj, min, pat := parseSemver(appVersion)
	wxs := buildWXS(wxsParams{
		AppName:      appName,
		ExeName:      appName + ".exe",
		ExeSource:    exePath,
		Version:      fmt.Sprintf("%d.%d.%d.0", maj, min, pat),
		Win64:        win64,
		UpgradeCode:  stableGUID("wick.upgrade." + appName),
		MainCompGUID: stableGUID("wick.main." + appName),
		MenuCompGUID: stableGUID("wick.menu." + appName),
		DeskCompGUID: stableGUID("wick.desktop." + appName),
		Manufacturer: appName,
	})

	wxsFile, err := os.CreateTemp("", "wick-msi-*.wxs")
	if err != nil {
		return "", fmt.Errorf("wxs temp: %w", err)
	}
	if _, err := wxsFile.WriteString(wxs); err != nil {
		wxsFile.Close()
		os.Remove(wxsFile.Name())
		return "", fmt.Errorf("write wxs: %w", err)
	}
	wxsFile.Close()
	defer os.Remove(wxsFile.Name())

	_ = os.Remove(msiPath)
	cmd := exec.Command("wixl", "--arch", wixArch, "-o", msiPath, wxsFile.Name())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("wixl: %w", err)
	}
	return msiPath, nil
}

type wxsParams struct {
	AppName      string
	ExeName      string
	ExeSource    string
	Version      string
	Win64        string
	UpgradeCode  string
	MainCompGUID string
	MenuCompGUID string
	DeskCompGUID string
	Manufacturer string
}

func buildWXS(p wxsParams) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Wix xmlns="http://schemas.microsoft.com/wix/2006/wi">
  <Product Id="*" Name="%[1]s" Version="%[2]s" Manufacturer="%[3]s" UpgradeCode="%[4]s" Language="1033">
    <Package InstallerVersion="200" Compressed="yes" InstallScope="perUser"/>
    <MajorUpgrade AllowSameVersionUpgrades="yes" DowngradeErrorMessage="A newer version of %[1]s is already installed."/>
    <Media Id="1" Cabinet="app.cab" EmbedCab="yes"/>
    <Directory Id="TARGETDIR" Name="SourceDir">
      <Directory Id="LocalAppDataFolder">
        <Directory Id="ProgramsFolder" Name="Programs">
          <Directory Id="INSTALLDIR" Name="%[1]s">
            <Component Id="MainExecutable" Guid="%[5]s"%[6]s>
              <File Id="MainExe" Name="%[7]s" Source="%[8]s" KeyPath="yes"/>
            </Component>
          </Directory>
        </Directory>
      </Directory>
      <Directory Id="ProgramMenuFolder">
        <Directory Id="AppMenuFolder" Name="%[1]s"/>
      </Directory>
      <Directory Id="DesktopFolder" Name="Desktop"/>
    </Directory>
    <DirectoryRef Id="AppMenuFolder">
      <Component Id="AppShortcut" Guid="%[9]s"%[6]s>
        <Shortcut Id="AppStartMenuShortcut" Name="%[1]s"
                  Target="[INSTALLDIR]%[7]s" WorkingDirectory="INSTALLDIR"/>
        <RemoveFolder Id="AppMenuFolder" On="uninstall"/>
        <RegistryValue Root="HKCU" Key="Software\%[1]s" Name="installed" Type="integer" Value="1" KeyPath="yes"/>
      </Component>
    </DirectoryRef>
    <DirectoryRef Id="DesktopFolder">
      <Component Id="DesktopShortcut" Guid="%[10]s"%[6]s>
        <Shortcut Id="AppDesktopShortcut" Name="%[1]s"
                  Target="[INSTALLDIR]%[7]s" WorkingDirectory="INSTALLDIR"/>
        <RegistryValue Root="HKCU" Key="Software\%[1]s" Name="desktopShortcut" Type="integer" Value="1" KeyPath="yes"/>
      </Component>
    </DirectoryRef>
    <Feature Id="MainFeature" Title="%[1]s" Level="1">
      <ComponentRef Id="MainExecutable"/>
      <ComponentRef Id="AppShortcut"/>
      <ComponentRef Id="DesktopShortcut"/>
    </Feature>
    <Property Id="LaunchAppCmd" Value="cmd.exe"/>
    <CustomAction Id="LaunchApp"
                  Property="LaunchAppCmd"
                  ExeCommand='/c start "" "[INSTALLDIR]%[7]s"'
                  Execute="immediate"
                  Impersonate="yes"
                  Return="asyncNoWait"/>
    <InstallExecuteSequence>
      <Custom Action="LaunchApp" After="InstallFinalize">NOT Installed AND NOT REMOVE</Custom>
    </InstallExecuteSequence>
  </Product>
</Wix>
`,
		html.EscapeString(p.AppName),      // 1
		p.Version,                         // 2
		html.EscapeString(p.Manufacturer), // 3
		p.UpgradeCode,                     // 4
		p.MainCompGUID,                    // 5
		p.Win64,                           // 6
		html.EscapeString(p.ExeName),      // 7
		html.EscapeString(p.ExeSource),    // 8
		p.MenuCompGUID,                    // 9
		p.DeskCompGUID,                    // 10
	)
}

func mapGoArchToWix(goarch string) (wixArch, win64Attr string) {
	switch goarch {
	case "386":
		return "x86", ""
	case "arm64":
		return "arm64", ` Win64="yes"`
	default: // amd64 and any unknown — assume 64-bit
		return "x64", ` Win64="yes"`
	}
}

// stableGUID derives a deterministic GUID from a seed string so the
// MSI's UpgradeCode and Component IDs stay constant across builds —
// required for in-place upgrades and Add/Remove Programs continuity.
func stableGUID(seed string) string {
	id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(seed))
	return strings.ToUpper(id.String())
}
