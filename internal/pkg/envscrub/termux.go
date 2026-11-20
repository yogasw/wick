package envscrub

import (
	"os"
	"strings"
)

// Termux glibc loader overrides.
//
// glibc-runner on Termux is often set up with LD_PRELOAD or LD_LIBRARY_PATH
// pointing into $PREFIX/glibc/lib. libc.so in that directory is a GNU ld
// linker script, not an ELF object, so every child that inherits it dies
// with "libc.so: invalid ELF header" (or exit 127) before it runs: the
// provider login terminal, `--version` probes, usage and account checks,
// and the managed binaries themselves. Filtering per spawner left paths
// behind, so it happens here, where every spawner already passes, and once
// on the daemon's own environment so a detached start, a graceful-upgrade
// successor and the deploy script it runs inherit a clean one too.
//
// Only entries under Termux's glibc tree are removed. Termux's own
// libtermux-exec.so preload is kept: bionic tools (sh, git) need it.
var loaderKeys = []string{"LD_PRELOAD", "LD_LIBRARY_PATH"}

// isTermuxGlibcPath reports a path inside Termux's glibc-runner tree.
func isTermuxGlibcPath(p string) bool {
	return strings.Contains(p, "/com.termux/") && strings.Contains(p, "/glibc/")
}

// cleanLoaderValue drops Termux glibc entries from a loader list. Entries
// are separated by ':' (LD_PRELOAD also accepts spaces). changed is false
// when nothing was removed, so the original value is kept byte for byte.
func cleanLoaderValue(v string) (out string, changed bool) {
	fields := strings.FieldsFunc(v, func(r rune) bool { return r == ':' || r == ' ' })
	keep := fields[:0:0]
	for _, f := range fields {
		if isTermuxGlibcPath(f) {
			changed = true
			continue
		}
		keep = append(keep, f)
	}
	if !changed {
		return v, false
	}
	return strings.Join(keep, ":"), true
}

// dropTermuxGlibcLoader returns env without Termux glibc loader entries.
// A variable left empty is removed instead of being passed as "KEY=".
// The input slice is returned as is when there is nothing to drop.
func dropTermuxGlibcLoader(env []string) []string {
	var out []string
	for i, kv := range env {
		key, val, ok := strings.Cut(kv, "=")
		clean, changed := "", false
		if ok && isLoaderKey(key) {
			clean, changed = cleanLoaderValue(val)
		}
		if changed && out == nil {
			out = append(make([]string, 0, len(env)), env[:i]...)
		}
		switch {
		case out == nil:
		case !changed:
			out = append(out, kv)
		case clean != "":
			out = append(out, key+"="+clean)
		}
	}
	if out == nil {
		return env
	}
	return out
}

func isLoaderKey(key string) bool {
	for _, k := range loaderKeys {
		if key == k {
			return true
		}
	}
	return false
}

// ScrubProcessLoaderEnv removes Termux glibc loader entries from this
// process's own environment. Call it once at startup, before anything is
// spawned; a no-op everywhere else.
func ScrubProcessLoaderEnv() {
	for _, k := range loaderKeys {
		v, ok := os.LookupEnv(k)
		if !ok {
			continue
		}
		clean, changed := cleanLoaderValue(v)
		if !changed {
			continue
		}
		if clean == "" {
			_ = os.Unsetenv(k)
		} else {
			_ = os.Setenv(k, clean)
		}
	}
}
