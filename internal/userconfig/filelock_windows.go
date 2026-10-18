//go:build windows

package userconfig

// lockFile is a no-op on Windows: one process owns the data dir there
// (no graceful reload hands it over), and providerFileMu already
// serialises that process's writers.
func lockFile(string) (func(), error) { return func() {}, nil }
