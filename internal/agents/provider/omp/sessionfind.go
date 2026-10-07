package omp

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// sessionfind.go finds an omp session's transcript whichever profile holds
// it, for the spawner (resume by path, sessionpath.go) and for Switch
// (whether a conversation can carry over to another omp instance, via the
// carrier registered below).

func init() {
	provider.RegisterHistoryCarrier(provider.TypeOMP, carryHistory)
	// Idle warm RPC processes and auth brokers yield to any other spawn
	// (pool admission → provider.YieldIdleServers); busy or queued ones
	// never do.
	provider.RegisterIdleYielder("omp", func(keep func(instance, group string) bool) int {
		return rpcServers.RetireIdle(keep) + brokers.RetireIdle(keep)
	})
}

// carryHistory is omp's provider.HistoryCarrier: another profile's
// transcript is resumed by path, so the conversation carries whenever the
// file is still on disk in any profile.
func carryHistory(_, to provider.Instance, resumeID string) provider.HistoryCarry {
	home, _ := homeDir()
	cfg := envValue(to.Env, "PI_CONFIG_DIR")
	if cfg == "" {
		cfg = os.Getenv("PI_CONFIG_DIR")
	}
	if home == "" || !sessionReachable(ompRoot(home, cfg), provider.OMPProfile(to), resumeID) {
		return provider.HistoryCarry{Reason: "omp transcript " + resumeID + " was not found in any omp profile"}
	}
	return provider.HistoryCarry{}
}

// ompRoot is omp's config root under home (PI_CONFIG_DIR, else .omp).
func ompRoot(home, cfgDir string) string {
	if cfgDir == "" {
		cfgDir = ".omp"
	}
	if filepath.IsAbs(cfgDir) {
		return cfgDir
	}
	return filepath.Join(home, cfgDir)
}

// sessionFiles lists the transcripts under agentDir whose id is id or
// starts with it (omp resumes by id prefix too), newest first.
func sessionFiles(agentDir, id string) []string {
	matches, _ := filepath.Glob(filepath.Join(agentDir, "sessions", "*", "*_"+globEscape(id)+"*.jsonl"))
	sort.Slice(matches, func(i, j int) bool { return modTime(matches[i]) > modTime(matches[j]) })
	return matches
}

func modTime(p string) int64 {
	if st, err := os.Stat(p); err == nil {
		return st.ModTime().UnixNano()
	}
	return 0
}

// globEscape keeps glob metacharacters in an id literal.
func globEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "*", `\*`, "?", `\?`, "[", `\[`)
	return r.Replace(s)
}

// findSessionPath returns the absolute path of the transcript for id in
// any omp profile under root other than own ("" when own already has it,
// or no profile does — omp then reports the miss itself).
func findSessionPath(root, own, id string) string {
	if id == "" || strings.ContainsAny(id, `/\`) {
		return ""
	}
	if len(sessionFiles(filepath.Join(root, "profiles", own, "agent"), id)) > 0 {
		return ""
	}
	dirs, _ := filepath.Glob(filepath.Join(root, "profiles", "*", "agent"))
	// omp's own profile-less store is a place a transcript can live too.
	dirs = append(dirs, filepath.Join(root, "agent"))
	var found []string
	for _, d := range dirs {
		if filepath.Base(filepath.Dir(d)) == own && filepath.Base(filepath.Dir(filepath.Dir(d))) == "profiles" {
			continue
		}
		found = append(found, sessionFiles(d, id)...)
	}
	if len(found) == 0 {
		return ""
	}
	sort.Slice(found, func(i, j int) bool { return modTime(found[i]) > modTime(found[j]) })
	return found[0]
}

// sessionReachable reports whether profile can resume id: its own
// store has it, or another profile's file can be resumed by path.
func sessionReachable(root, profile, id string) bool {
	if id == "" {
		return false
	}
	if len(sessionFiles(filepath.Join(root, "profiles", profile, "agent"), id)) > 0 {
		return true
	}
	return findSessionPath(root, profile, id) != ""
}
