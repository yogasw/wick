// Package metrics defines the Recorder interface that connectors.Service
// uses to emit telemetry. The default implementation (SimpleRecorder)
// uses only stdlib — no external dependency. Projects that want
// Prometheus can implement Recorder against client_golang and wire it
// in via connectors.Service.SetMetrics before the server starts.
package metrics

// Recorder captures connector execution telemetry.
type Recorder interface {
	// RecordRun records one completed connector run.
	RecordRun(connectorKey, operationKey, status string, latencyMs int)
	// IncActive increments the in-flight run gauge.
	IncActive()
	// DecActive decrements the in-flight run gauge.
	DecActive()
}

// Noop silently discards all metrics. Used as the zero value when no
// backend is wired so connector service code stays unconditional.
type Noop struct{}

func (Noop) RecordRun(_, _, _ string, _ int) {}
func (Noop) IncActive()                       {}
func (Noop) DecActive()                       {}
