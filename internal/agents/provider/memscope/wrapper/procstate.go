package wrapper

// procstate.go holds the shape every platform shares. Only READING
// cgroup membership is Linux-only; describing and counting it is not, so
// a caller that formats a report compiles everywhere.

// ProcState is one running agent process and where it lives.
type ProcState struct {
	PID  int
	Name string
	// RSSBytes is what it currently holds.
	RSSBytes uint64
	// Cgroup is the leaf path from /proc/<pid>/cgroup, the evidence for
	// every judgement below.
	Cgroup string
	// Isolated is true when the process sits in the agents slice — a
	// scope of its own, with the slice's ceiling above it.
	Isolated bool
	// FromWick is true when this process descends from this wick.
	// Reported separately from Isolated because the two failure modes
	// need different fixes: wick's own agent outside the slice means the
	// guard is off or the shim is not installed, while a stranger
	// outside the slice means something else on the machine runs the
	// same binary and no shim can reach it.
	FromWick bool
}
