//go:build !linux && !android

package memscope

// execagent_other.go stubs the cgroupfs re-exec off Linux. The hidden
// `wick __agent-exec` subcommand (app/agentexec_cmd.go) is registered on
// every platform's binary but WrapArgvCgroupFS never produces argv that
// invokes it here (cgroupFSProbe is always false), so this only exists to
// let that command's handler compile.

type ExecOpts struct {
	Root    string
	Slice   string
	Unit    string
	LimitMB int
	Bin     string
	Args    []string
}

func RunAgentExec(o ExecOpts) error { return ErrUnsupported }
