package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	provider "github.com/yogasw/wick/internal/agents/provider"

	"github.com/yogasw/wick/internal/pkg/envscrub"
)

// An opencode session lives in the SQLite DB of the data folder that ran
// it, and another instance (or account folder) has its own DB: resuming
// the id there finds nothing. So before a turn resumes a session on a
// folder other than the one that last wrote it, the session is copied
// over: `opencode export <id>` with the writer's env, `opencode import`
// with this folder's. The id is kept (import upserts), so a switch back
// later copies the newer turns the same way. The DB is never shared or
// linked: its -wal/-shm files follow the link path.

// ownersFile records, per wick session, the data folder that last ran
// each opencode session id ("" key: the folder of the last spawn, for a
// session whose id was not known yet when it started).
const ownersFile = "owners.json"

var ownersMu sync.Mutex

// syncRunner runs one opencode subcommand with env added to the scrubbed
// OS env and returns its stdout. Swapped in tests. It runs inside the
// memory guard like an agent spawn ("opencode-export" / "opencode-import"
// scope), with the limit of the instance whose data folder env names.
var syncRunner = func(ctx context.Context, bin, dir string, env []string, args ...string) ([]byte, error) {
	label := provider.HelperLabel(provider.TypeOpencode, "sync")
	if len(args) > 0 {
		label = provider.HelperLabel(provider.TypeOpencode, args[0])
	}
	cmd, release := provider.HelperCommand(ctx, provider.InstanceForAccountEnv(env), label, bin, args...)
	defer release()
	cmd.Dir = dir
	cmd.Env = append(envscrub.ScrubOSEnv(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	hideConsole(cmd)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, bytes.TrimSpace(errb.Bytes()))
	}
	return out.Bytes(), nil
}

// syncTimeout bounds one export or import.
const syncTimeout = 2 * time.Minute

// syncSource is the folder the session must be copied from before a turn
// resumes resumeID in target ("" = nothing to copy: no resume, the writer
// is target, or the writer is unknown or gone).
func syncSource(owners map[string]string, resumeID, target string) string {
	if resumeID == "" {
		return ""
	}
	src := owners[resumeID]
	if src == "" {
		src = owners[""]
	}
	if src == "" || filepath.Clean(src) == filepath.Clean(target) {
		return ""
	}
	if _, err := os.Stat(filepath.Join(src, "opencode", "opencode.db")); err != nil {
		return ""
	}
	return src
}

// copySession exports id from src and imports it into dst, both folders
// pinned by opencodeStoreEnv exactly as a spawn there would be. The
// export lands in tmpDir and is removed afterwards.
func copySession(ctx context.Context, bin, cwd, tmpDir, src, dst, id string) error {
	// The id names the export file: one that could leave tmpDir is no
	// opencode session id (as the omp side refuses it too).
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return fmt.Errorf("invalid session id %q", id)
	}
	ctx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	out, err := syncRunner(ctx, bin, cwd, storeEnv(src), "export", id)
	if err != nil {
		return fmt.Errorf("export from %s: %w", src, err)
	}
	if i := bytes.IndexByte(out, '{'); i > 0 {
		out = out[i:] // a status line before the JSON
	}
	if !json.Valid(out) {
		return errors.New("export from " + src + ": output is not JSON")
	}
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return err
	}
	f := filepath.Join(tmpDir, "export-"+id+".json")
	if err := os.WriteFile(f, out, 0o600); err != nil {
		return err
	}
	defer os.Remove(f)
	if _, err := syncRunner(ctx, bin, cwd, storeEnv(dst), "import", f); err != nil {
		return fmt.Errorf("import into %s: %w", dst, err)
	}
	return nil
}

// storeEnv pins opencode to dir the way a spawn there does, so export
// and import read and write the DBs the turns use.
func storeEnv(dir string) []string {
	env, _ := provider.OpencodeEnv(provider.Instance{Type: provider.TypeOpencode, OpencodeConfig: &provider.OpencodeConfig{DataDir: dir}})
	return env
}

func init() {
	provider.RegisterHistoryCarrier(provider.TypeOpencode, carrySwitch)
	// An idle `opencode serve` yields to any other spawn (pool admission →
	// provider.YieldIdleServers); one running or queued turns never does.
	provider.RegisterIdleYielder("opencode", func(keep func(instance, group string) bool) int {
		return servers.RetireIdle(keep)
	})
}

// carrySwitch is opencode's provider.HistoryCarrier: the session is copied
// from the source instance's data folder at the next spawn (carryHistory),
// which needs that folder's DB.
func carrySwitch(from, _ provider.Instance, _ string) provider.HistoryCarry {
	src, err := provider.OpencodeDataDir(from)
	if err != nil {
		return provider.HistoryCarry{Reason: "the data folder of " + from.Name + " is unknown"}
	}
	if _, err := os.Stat(filepath.Join(src, "opencode", "opencode.db")); err != nil {
		return provider.HistoryCarry{Reason: "the opencode data of " + from.Name + " is gone, so its history cannot be copied"}
	}
	return provider.HistoryCarry{Copied: true}
}

// carryHistory makes resumeID resumable in target before the turn: it
// copies the session from the folder that last wrote it, then records
// target as the writer. A failed copy keeps the id: the resume then fails
// on its own and the pool tells the user it starts fresh (never a silent
// new conversation). stateDir is the wick session's .opencode-wick dir.
func carryHistory(ctx context.Context, bin, cwd, stateDir, resumeID, target string) {
	owners := readOwners(stateDir)
	if src := syncSource(owners, resumeID, target); src != "" {
		start := time.Now()
		if err := copySession(ctx, bin, cwd, stateDir, src, target, resumeID); err != nil {
			log.Warn().Err(err).Str("resume", resumeID).Str("from", src).Str("to", target).
				Msg("agents.spawn: opencode history not carried to this instance")
			return
		}
		log.Info().Str("resume", resumeID).Str("from", src).Str("to", target).
			Dur("took", time.Since(start)).Msg("agents.spawn: opencode history carried to this instance")
	}
	if stateDir == "" {
		return
	}
	owners[""] = target
	if resumeID != "" {
		owners[resumeID] = target
	}
	writeOwners(stateDir, owners)
}

func readOwners(dir string) map[string]string {
	m := map[string]string{}
	if dir == "" {
		return m
	}
	ownersMu.Lock()
	defer ownersMu.Unlock()
	if b, err := os.ReadFile(filepath.Join(dir, ownersFile)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func writeOwners(dir string, m map[string]string) {
	ownersMu.Lock()
	defer ownersMu.Unlock()
	// Same modes as the export beside it (copySession).
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, ownersFile), b, 0o600)
}
