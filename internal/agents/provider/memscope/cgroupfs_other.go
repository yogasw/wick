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
