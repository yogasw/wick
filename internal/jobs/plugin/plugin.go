// Package plugin is the host side of job plugins: a kind=job binary under
// plugins/jobs/<key>/ is registered like a built-in job (Jobs page, cron,
// Run now, run history). Each Run spawns the binary, calls the gRPC Job.Run
// once, and kills the process when the stream ends — nothing stays resident
// between runs.
package plugin

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/internal/jobs"
	"github.com/yogasw/wick/pkg/job"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DefaultTimeout bounds one plugin run; WICK_JOB_PLUGIN_TIMEOUT (a Go
// duration) overrides it. The process is killed when it elapses.
const DefaultTimeout = 30 * time.Minute

// TriggerScheduled is the trigger sent to the plugin. The scheduler does not
// tell a cron run from Run now yet, so every run is reported the same way.
const TriggerScheduled = "cron"

// spawnFn starts one job plugin process; swapped in tests.
type spawnFn func(binary string) (kill func(), conn wickplugin.JobConn, err error)

func spawnProcess(binary string) (func(), wickplugin.JobConn, error) {
	client, raw, err := connplugin.Spawn(connplugin.SpawnSpec{
		Binary:   binary,
		Plugins:  wickplugin.JobVersionedPlugins,
		Dispense: wickplugin.JobPluginName,
	})
	if err != nil {
		return nil, nil, err
	}
	conn, ok := raw.(wickplugin.JobConn)
	if !ok {
		client.Kill()
		return nil, nil, fmt.Errorf("plugin %s is not a job plugin", binary)
	}
	return client.Kill, conn, nil
}

// live holds the kill of every job process still running, so KillAll can
// reap them on shutdown / before a reload hands over.
var live = struct {
	sync.Mutex
	next  int
	kills map[int]func()
}{kills: map[int]func(){}}

// track registers kill as live; the returned func kills once and unregisters.
func track(kill func()) func() {
	live.Lock()
	live.next++
	id := live.next
	var once sync.Once
	k := func() { once.Do(kill) }
	live.kills[id] = k
	live.Unlock()
	return func() {
		live.Lock()
		delete(live.kills, id)
		live.Unlock()
		k()
	}
}

// KillAll kills every job plugin process still running (wick shutdown or
// the end of a reload drain). The interrupted runs fail like a timeout.
func KillAll() {
	live.Lock()
	kills := make([]func(), 0, len(live.kills))
	for id, k := range live.kills {
		kills = append(kills, k)
		delete(live.kills, id)
	}
	live.Unlock()
	for _, k := range kills {
		k()
	}
}

func timeout() time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(os.Getenv("WICK_JOB_PLUGIN_TIMEOUT"))); err == nil && d > 0 {
		return d
	}
	return DefaultTimeout
}

// BuildModule turns one discovered kind=job plugin into a job.Module whose
// Run spawns the binary for exactly one call.
func BuildModule(f connplugin.Found) job.Module {
	return buildModule(f, spawnProcess, timeout())
}

func buildModule(f connplugin.Found, spawn spawnFn, limit time.Duration) job.Module {
	jm := f.Manifest.Job
	if jm == nil {
		jm = &wickplugin.JobModule{}
	}
	meta := jm.Meta
	meta.Key = f.Key // the folder/manifest key is the one identity
	if meta.Name == "" {
		meta.Name = f.Manifest.Module.Meta.Name
	}
	if meta.Description == "" {
		meta.Description = f.Manifest.Module.Meta.Description
	}
	if meta.Icon == "" {
		meta.Icon = "🧩"
	}
	keys := make([]string, 0, len(jm.Configs))
	for _, c := range jm.Configs {
		keys = append(keys, c.Key)
	}
	bin, version := f.BinaryPath, f.Manifest.Version
	run := func(ctx context.Context) (string, error) {
		cfg := make(map[string]string, len(keys))
		jc := job.FromContext(ctx)
		for _, k := range keys {
			cfg[k] = jc.Cfg(k)
		}
		ctx, cancel := context.WithTimeout(ctx, limit)
		defer cancel()
		kill, conn, err := spawn(bin)
		if err != nil {
			return "", fmt.Errorf("start job plugin %s: %w", meta.Key, err)
		}
		// The process lives for this one call only.
		kill = track(kill)
		defer kill()
		var lines []string
		result, err := conn.Run(ctx, TriggerScheduled, cfg, func(line string) {
			lines = append(lines, line)
		})
		// gRPC enforces the same deadline with its own timer, so the call
		// can fail with DeadlineExceeded a moment before ctx.Err() is set.
		if ctx.Err() == context.DeadlineExceeded || status.Code(err) == codes.DeadlineExceeded {
			err = fmt.Errorf("job plugin %s timed out after %s", meta.Key, limit)
		}
		return withLog(result, version, lines), err
	}
	return job.Module{Meta: meta, Configs: jm.Configs, Run: run}
}

// withLog appends the streamed progress lines under the result so they land in
// run history next to it.
func withLog(result, version string, lines []string) string {
	if len(lines) == 0 {
		return result
	}
	var b strings.Builder
	b.WriteString(result)
	if result != "" {
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "**Plugin log** (v%s)\n\n```\n%s\n```", version, strings.Join(lines, "\n"))
	return b.String()
}

// Load registers every enabled, verified kind=job plugin under dir with the
// jobs registry and returns how many were registered. Call it before
// job.ValidateJobs / configs bootstrap so the plugin's config rows are seeded
// like a built-in job's. record (optional) is told the kind + version of each
// registered plugin (PluginState bookkeeping).
func Load(dir string, enabled func(string) bool, record func(key, kind, version string) error) int {
	return load(dir, enabled, record, jobs.Register, BuildModule)
}

func load(dir string, enabled func(string) bool, record func(key, kind, version string) error,
	register func(job.Module), build func(connplugin.Found) job.Module) int {
	found, err := connplugin.ScanKind(dir, wickplugin.KindJob)
	if err != nil {
		log.Warn().Err(err).Msg("job plugins: scan failed")
		return 0
	}
	n := 0
	for _, f := range found {
		if err := wickplugin.ValidateKey(f.Key); err != nil {
			log.Warn().Str("plugin", f.Key).Err(err).Msg("job plugin: skipped (invalid key)")
			continue
		}
		if enabled != nil && !enabled(f.Key) {
			continue
		}
		if err := wickplugin.VerifyManifest(f.Manifest, f.BinaryPath); err != nil {
			log.Warn().Str("job", f.Key).Err(err).Msg("job plugin: skipped (verification failed)")
			continue
		}
		register(build(f))
		if record != nil {
			if err := record(f.Key, wickplugin.KindJob, f.Manifest.Version); err != nil {
				log.Warn().Str("job", f.Key).Err(err).Msg("job plugin: state record failed")
			}
		}
		n++
	}
	return n
}
