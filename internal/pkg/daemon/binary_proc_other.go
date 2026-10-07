//go:build !linux

package daemon

// Outside Linux there is no /proc to read argv[0] from; RunningTarget falls
// back to the process image path reported by processctl.
func procCmdline0(int) string { return "" }
func procCwd(int) string      { return "" }
