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
type Host struct {
	OS   string `json:"os"`   // "linux" | "darwin" | "windows"
	Arch string `json:"arch"` // "x64" | "arm64" | raw GOARCH otherwise
	Musl bool   `json:"musl"`
	AVX2 bool   `json:"avx2"`
}

// Label is the one-line host description shown in the UI:
// "linux-x64 · glibc · AVX2".
func (h Host) Label() string {
	parts := []string{h.OS + "-" + h.Arch}
	if h.OS == "linux" {
		if h.Musl {
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
		h.Musl = detectMusl()
		if h.Arch == "x64" {
			if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
				h.AVX2 = cpuinfoHasAVX2(b)
			}
		}
	}
	return h
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
