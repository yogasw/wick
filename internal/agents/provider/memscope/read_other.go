//go:build !linux && !android

package memscope

// ReadStats reports nothing off Linux — there are no cgroup files to read.
// The parsing itself still lives in read.go and stays under test here via
// ReadStatsAt against a temp tree.
func ReadStats(unit string) Stats { return Stats{} }
