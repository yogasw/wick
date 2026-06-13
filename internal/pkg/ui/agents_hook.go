package ui

var agentsAvailFn func() bool

// SetAgentsAvailable registers a function that reports whether the agents
// tool is running in this process. Called during server boot by the agents
// tool package. When not set, agents is treated as unavailable.
func SetAgentsAvailable(fn func() bool) {
	agentsAvailFn = fn
}

func agentsAvailable() bool {
	if agentsAvailFn == nil {
		return false
	}
	return agentsAvailFn()
}
