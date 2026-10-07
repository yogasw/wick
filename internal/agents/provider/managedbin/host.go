package managedbin

import (
	"bytes"
	"context"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/pkg/envscrub"
	"github.com/yogasw/wick/pkg/safeexec"
)

// Host is what asset selection keys on. Mirrors the official installers
// (omp.sh/install, opencode.ai/install): x64/arm64, musl when
// /etc/alpine-release exists or `ldd --version` mentions musl, AVX2 from
// /proc/cpuinfo on linux-x64.
//
// Termux is linux on bionic: neither /lib/ld-linux-* nor /lib/ld-musl-*
// exists, so no published build runs as downloaded. It keeps the glibc
// pick and the install points the interpreter at Termux's glibc-runner
// loader, in place (see prepareBinary).
type Host struct {
	OS     string `json:"os"`   // "linux" | "darwin" | "windows"
	Arch   string `json:"arch"` // "x64" | "arm64" | raw GOARCH otherwise
	Musl   bool   `json:"musl"`
	AVX2   bool   `json:"avx2"`
	Termux bool   `json:"termux,omitempty"`
}

// Label is the one-line host description shown in the UI:
// "linux-x64 · glibc · AVX2".
func (h Host) Label() string {
	parts := []string{h.OS + "-" + h.Arch}
	if h.OS == "linux" {
		if h.Termux {
			parts = append(parts, "termux")
		} else if h.Musl {
			parts = append(parts, "musl")
		} else {
			parts = append(parts, "glibc")
		}
	}
	if h.Arch == "x64" {
		if h.AVX2 {
			parts = append(parts, "AVX2")
		} else {
			parts = append(parts, "no AVX2")
		}
	}
	return strings.Join(parts, " · ")
}

// DetectHost probes the running machine.
func DetectHost() Host {
	h := Host{OS: runtime.GOOS, Arch: normArch(runtime.GOARCH)}
	if h.OS == "linux" {
		// Termux has no musl loader either; glibc is the one glibc-runner
		// can provide, so musl detection is skipped there.
		h.Termux = isTermux(os.Getenv, pathExists)
		if !h.Termux {
			h.Musl = detectMusl()
		}
		if h.Arch == "x64" {
			if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
				h.AVX2 = cpuinfoHasAVX2(b)
			}
		}
	}
	return h
}

// isTermux mirrors env.IsTermux, plus $PREFIX for a relocated install.
func isTermux(getenv func(string) string, exists func(string) bool) bool {
	if getenv("TERMUX_VERSION") != "" || strings.Contains(getenv("PREFIX"), "/com.termux/") {
		return true
	}
	return exists("/data/data/com.termux/files/usr")
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func normArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	}
	return goarch
}

func detectMusl() bool {
	if _, err := os.Stat("/etc/alpine-release"); err == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Resolved first: a bare name makes exec.Cmd call LookPath (faccessat2,
	// SIGSYS on old Android kernels). No ldd = not musl.
	ldd, err := safeexec.ResolveBin("ldd")
	if err != nil {
		return false
	}
	c := safeexec.CommandContext(ctx, ldd, "--version")
	c.Env = envscrub.ScrubOSEnv()
	out, _ := c.CombinedOutput()
	return bytes.Contains(bytes.ToLower(out), []byte("musl"))
}

// cpuinfoHasAVX2 is `grep -qwi avx2 /proc/cpuinfo`.
func cpuinfoHasAVX2(b []byte) bool {
	for _, f := range strings.Fields(strings.ToLower(string(b))) {
		if f == "avx2" {
			return true
		}
	}
	return false
}
