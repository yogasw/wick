//go:build linux || android

package plugin

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

func envInt(key string) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil && n > 0 {
		return n
	}
	return 0
}

// setRlimit applies a cap, soft-failing on error. Some Android/Termux kernels
// restrict RLIMIT_AS; a failure must not abort the plugin.
func setRlimit(resource int, max uint64) {
	lim := syscall.Rlimit{Cur: max, Max: max}
	if err := syscall.Setrlimit(resource, &lim); err != nil {
		fmt.Fprintf(os.Stderr, "wick-plugin: rlimit %d set failed: %v\n", resource, err)
	}
}

// applyRlimits caps the plugin's own resources from inherited env (the host
// passes os.Environ to the subprocess). 0/unset leaves a limit untouched.
func applyRlimits() {
	if mb := envInt("WICK_PLUGIN_RLIMIT_AS_MB"); mb > 0 {
		setRlimit(syscall.RLIMIT_AS, uint64(mb)*1024*1024)
	}
	if sec := envInt("WICK_PLUGIN_RLIMIT_CPU_SEC"); sec > 0 {
		setRlimit(syscall.RLIMIT_CPU, uint64(sec))
	}
	if n := envInt("WICK_PLUGIN_RLIMIT_NOFILE"); n > 0 {
		setRlimit(syscall.RLIMIT_NOFILE, uint64(n))
	}
}
