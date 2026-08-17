//go:build !linux && !android

package memscope

// cgroupfs_other.go stubs the raw-cgroup fallback off Linux, where there
// is no cgroup filesystem to drive by hand either. DetectBackend never
// reaches these — cgroupFSProbe only exists so backend_linux.go's mirror
// package shape compiles — but EnsureCgroupSlice and WrapArgvCgroupFS are
// called from the platform-agnostic memguard.go, so they need a body
// here too.

func cgroupFSProbe() bool { return false }

func EnsureCgroupSlice(limits SliceLimits) error { return ErrUnsupported }

func WrapArgvCgroupFS(selfPath, bin string, args []string, o Opts) (string, []string) {
	return bin, args
}

// RemoveCgroupScope has nothing to remove off Linux: no scope was ever
// created. Nil rather than ErrUnsupported because the caller runs this on
// every agent exit and an error there would be noise, not information.
func RemoveCgroupScope(unit string) error { return nil }
