package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"github.com/yogasw/wick/internal/mcpconfig"
)

const (
	checkOK   = "✓"
	checkFail = "✗"
	checkWarn = "!"
)

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check your wick environment and report any issues",
		Long: `doctor runs a series of environment checks and prints a summary.

Each check reports ✓ (ok), ✗ (missing/broken), or ! (warning).
Exit code is 0 when all required checks pass, 1 otherwise.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor()
		},
	}
}

type check struct {
	label   string
	status  string // checkOK | checkFail | checkWarn
	detail  string
	indent  int
	required bool
}

func runDoctor() error {
	checks := collectChecks()

	maxLabel := 0
	for _, c := range checks {
		padded := c.indent*2 + len(c.label)
		if padded > maxLabel {
			maxLabel = padded
		}
	}

	allOK := true
	for _, c := range checks {
		prefix := strings.Repeat("  ", c.indent)
		label := prefix + c.label
		padding := strings.Repeat(" ", maxLabel-len(label)+2)

		line := fmt.Sprintf("  %s  %s%s", c.status, label, padding)
		if c.detail != "" {
			line += c.detail
		}
		fmt.Println(line)

		if c.required && c.status == checkFail {
			allOK = false
		}
	}

	fmt.Println()
	if allOK {
		fmt.Println("  All checks passed.")
	} else {
		fmt.Println("  Some required checks failed. Run the suggested commands to fix them.")
		return fmt.Errorf("doctor: environment issues detected")
	}
	return nil
}

func collectChecks() []check {
	var checks []check
	cwd, _ := os.Getwd()

	// ── wick CLI ──────────────────────────────────────────────────────
	checks = append(checks, check{
		label:    "wick CLI",
		status:   checkOK,
		detail:   AppVersion,
		required: false,
	})

	// ── Go toolchain ──────────────────────────────────────────────────
	if out, err := exec.Command("go", "version").Output(); err == nil {
		version := parseGoVersion(strings.TrimSpace(string(out)))
		checks = append(checks, check{
			label:    "go",
			status:   checkOK,
			detail:   version,
			required: true,
		})
	} else {
		checks = append(checks, check{
			label:    "go",
			status:   checkFail,
			detail:   "not found — install from https://go.dev/dl",
			required: true,
		})
	}

	// ── wick.yml ──────────────────────────────────────────────────────
	cfg, err := loadConfig()
	if err == nil {
		detail := ""
		if cfg.Name != "" {
			detail = fmt.Sprintf("name: %s", cfg.Name)
		}
		if cfg.Version != "" {
			if detail != "" {
				detail += ", "
			}
			detail += "version: " + cfg.Version
		}
		checks = append(checks, check{
			label:    "wick.yml",
			status:   checkOK,
			detail:   detail,
			required: false,
		})
	} else {
		checks = append(checks, check{
			label:    "wick.yml",
			status:   checkWarn,
			detail:   "not found in current directory",
			required: false,
		})
	}

	// ── wick-gate ─────────────────────────────────────────────────────
	checks = append(checks, checkWickGate())

	// ── templ ─────────────────────────────────────────────────────────
	checks = append(checks, checkBinary("templ", "run: wick setup"))

	// ── tailwindcss ───────────────────────────────────────────────────
	checks = append(checks, checkTailwind())

	// ── MCP clients ───────────────────────────────────────────────────
	checks = append(checks, check{
		label:  "MCP clients",
		status: checkOK,
		detail: "",
	})

	mcpName := filepath.Base(cwd)
	for _, client := range mcpconfig.AllClients(cwd) {
		dirOK := dirExists(filepath.Dir(client.Path))
		_, installed := mcpconfig.IsInstalled(client, mcpName)

		var status, detail string
		switch {
		case !dirOK:
			status = checkWarn
			detail = "not installed on this machine"
		case installed:
			status = checkOK
			detail = "wick registered"
		default:
			status = checkFail
			detail = fmt.Sprintf("wick not registered — run: wick mcp install --client %s", client.ID)
		}

		checks = append(checks, check{
			label:    client.Label,
			status:   status,
			detail:   detail,
			indent:   1,
			required: false,
		})
	}

	return checks
}

// checkWickGate checks for wick-gate next to this binary, in ./bin/, or PATH.
func checkWickGate() check {
	// Same resolution order as server.go resolveWickGateBin.
	names := []string{"wick-gate", "wick-gate.exe"}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, name := range names {
			if p := filepath.Join(dir, name); statOK(p) {
				return check{label: "wick-gate", status: checkOK, detail: p}
			}
		}
	}
	for _, name := range names {
		if p := localBinPath(name); statOK(p) {
			return check{label: "wick-gate", status: checkOK, detail: p}
		}
	}
	if p, err := exec.LookPath("wick-gate"); err == nil {
		return check{label: "wick-gate", status: checkOK, detail: p}
	}
	return check{
		label:  "wick-gate",
		status: checkWarn,
		detail: "not found — run: wick setup  (or: go install github.com/yogasw/wick/cmd/wick-gate@latest)",
	}
}

func statOK(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// checkBinary checks whether a binary is available in PATH or ./bin/.
func checkBinary(name, hint string) check {
	// Check ./bin/ first (local project install)
	localBin := localBinPath(name)
	if _, err := os.Stat(localBin); err == nil {
		return check{
			label:    name,
			status:   checkOK,
			detail:   localBin,
			required: false,
		}
	}
	// Fall back to PATH
	if path, err := exec.LookPath(name); err == nil {
		return check{
			label:    name,
			status:   checkOK,
			detail:   path,
			required: false,
		}
	}
	return check{
		label:    name,
		status:   checkFail,
		detail:   hint,
		required: false,
	}
}

// checkTailwind checks for the tailwindcss binary using the arch-aware
// name from wick.yml vars when available, falling back to generic names.
func checkTailwind() check {
	// Try arch-specific names first, then generic
	candidates := tailwindCandidates()
	for _, name := range candidates {
		local := localBinPath(name)
		if _, err := os.Stat(local); err == nil {
			return check{label: "tailwindcss", status: checkOK, detail: local}
		}
		if path, err := exec.LookPath(name); err == nil {
			return check{label: "tailwindcss", status: checkOK, detail: path}
		}
	}
	return check{
		label:  "tailwindcss",
		status: checkFail,
		detail: "run: wick setup",
	}
}

func tailwindCandidates() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{"tailwindcss.exe", "tailwindcss"}
	default:
		return []string{"tailwindcss"}
	}
}

func localBinPath(name string) string {
	return filepath.Join("bin", name)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// parseGoVersion extracts the short version from "go version go1.25.0 linux/amd64".
func parseGoVersion(raw string) string {
	parts := strings.Fields(raw)
	if len(parts) >= 3 {
		return strings.TrimPrefix(parts[2], "go")
	}
	return raw
}
