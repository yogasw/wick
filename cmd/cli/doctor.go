package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	agentgate "github.com/yogasw/wick/internal/agents/gate"
	"github.com/yogasw/wick/internal/mcpconfig"
)

const (
	checkOK   = "✓"
	checkFail = "✗"
	checkWarn = "!"
)

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor [binary]",
		Short: "Check your wick environment and report any issues",
		Long: `doctor runs a series of environment checks and prints a summary.

Each check reports ✓ (ok), ✗ (missing/broken), or ! (warning).
Exit code is 0 when all required checks pass, 1 otherwise.

Pass an optional binary path (e.g. wick-lab.exe) to inspect a specific
branded build — doctor will derive its AppName, locate the matching gate
binary, and verify socket/spec paths.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file := ""
			if len(args) > 0 {
				file = args[0]
			}
			return runDoctor(file)
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

func runDoctor(file string) error {
	checks := collectChecks(file)

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

func collectChecks(file string) []check {
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

	// ── gate ──────────────────────────────────────────────────────────
	checks = append(checks, checkGate(file)...)

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

// checkGate returns checks for the branded gate binary, AppName
// consistency, socket file, and socket liveness.
// appNameFromFile derives AppName from a binary path the same way
// gate.AppName() does from os.Executable — strip .exe, strip -gate suffix.
// Returns "" when file is empty or the stem is blank.
func appNameFromFile(file string) string {
	if file == "" {
		return ""
	}
	stem := strings.TrimSuffix(filepath.Base(file), ".exe")
	stem = strings.TrimSuffix(stem, "-gate")
	return stem
}

func checkGate(file string) []check {
	var out []check

	// Derive AppName from --file binary stem, fallback to agentgate.AppName().
	appName := appNameFromFile(file)
	if appName == "" {
		appName = agentgate.AppName()
	}
	gateName := appName + "-gate"
	if runtime.GOOS == "windows" {
		gateName += ".exe"
	}

	// ── app_name derived from this binary ──────────────────────────────
	out = append(out, check{
		label:  "gate app_name",
		status: checkOK,
		detail: appName,
	})

	// ── gate binary (sibling > ./bin/ > PATH) ─────────────────────────
	// Sibling dir: prefer --file's dir, fallback to this exe's dir.
	gatePath := ""
	gateSource := ""
	siblingDir := ""
	if file != "" {
		siblingDir = filepath.Dir(file)
	} else if exe, err := os.Executable(); err == nil {
		siblingDir = filepath.Dir(exe)
	}
	if siblingDir != "" {
		if p := filepath.Join(siblingDir, gateName); statOK(p) {
			gatePath, gateSource = p, "sibling"
		}
	}
	if gatePath == "" {
		if p := localBinPath(gateName); statOK(p) {
			gatePath, gateSource = p, "bin/"
		}
	}
	if gatePath == "" {
		if p, err := exec.LookPath(strings.TrimSuffix(gateName, ".exe")); err == nil {
			gatePath, gateSource = p, "PATH"
		}
	}
	if gatePath != "" {
		out = append(out, check{
			label:  "gate binary",
			status: checkOK,
			detail: fmt.Sprintf("%s  (%s)", gatePath, gateSource),
			indent: 1,
		})
	} else {
		out = append(out, check{
			label:    "gate binary",
			status:   checkFail,
			detail:   fmt.Sprintf("%s not found — run: wick build", gateName),
			indent:   1,
			required: true,
		})
	}

	// ── AppName match: gate binary stem == server AppName ─────────────
	// Verify gate binary derives the same AppName so socket paths align.
	if gatePath != "" {
		stem := strings.TrimSuffix(filepath.Base(gatePath), ".exe")
		stem = strings.TrimSuffix(stem, "-gate")
		if stem == appName {
			out = append(out, check{
				label:  "gate name match",
				status: checkOK,
				detail: fmt.Sprintf("gate stem %q == app_name %q", stem, appName),
				indent: 1,
			})
		} else {
			out = append(out, check{
				label:  "gate name match",
				status: checkFail,
				detail: fmt.Sprintf("gate stem %q != app_name %q — socket paths will diverge", stem, appName),
				indent: 1,
			})
		}
	}

	// ── socket path ───────────────────────────────────────────────────
	socketPath := agentgate.SharedSocketPath(appName)
	out = append(out, check{
		label:  "gate socket",
		status: checkOK,
		detail: socketPath,
		indent: 1,
	})

	// ── gate round-trip ───────────────────────────────────────────────
	// Send a probe ApprovalRequest and expect any ApprovalResponse back
	// (server will block+timeout it, but the JSON decode proves the
	// full encode→decode path works). We use a 3s deadline so doctor
	// doesn't hang waiting for a human to click Approve.
	out = append(out, checkGateRoundTrip(socketPath))

	// ── spec file ─────────────────────────────────────────────────────
	specPath := agentgate.SharedSpecPath(appName)
	if st, err := os.Stat(specPath); err == nil {
		out = append(out, check{
			label:  "gate spec",
			status: checkOK,
			detail: fmt.Sprintf("%s  (%d bytes)", specPath, st.Size()),
			indent: 1,
		})
	} else {
		out = append(out, check{
			label:  "gate spec",
			status: checkWarn,
			detail: fmt.Sprintf("%s missing — created on first server start", specPath),
			indent: 1,
		})
	}

	return out
}

// checkGateRoundTrip dials the socket, sends a probe ApprovalRequest,
// and waits up to 3s for any response. A timeout reply from the server
// still counts as success — it proves encode→decode works end-to-end.
// "connection refused" or EOF means the server is not ready.
func checkGateRoundTrip(socketPath string) check {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return check{
			label:  "gate round-trip",
			status: checkWarn,
			detail: "not listening — start the server first",
			indent: 1,
		}
	}
	defer conn.Close()

	req := agentgate.ApprovalRequest{
		ID:        "doctor-probe",
		SessionID: "doctor",
		Tool:      "Bash",
		Cmd:       "echo doctor-probe",
		WorkDir:   "/",
		MatchKey:  "doctor",
		Timestamp: time.Now().UnixMilli(),
		Probe:     true,
	}
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return check{
			label:  "gate round-trip",
			status: checkFail,
			detail: fmt.Sprintf("send failed: %v", err),
			indent: 1,
		}
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var resp agentgate.ApprovalResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return check{
			label:  "gate round-trip",
			status: checkFail,
			detail: fmt.Sprintf("read response failed: %v", err),
			indent: 1,
		}
	}
	return check{
		label:  "gate round-trip",
		status: checkOK,
		detail: fmt.Sprintf("ok — server replied decision=%q", resp.Decision),
		indent: 1,
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
