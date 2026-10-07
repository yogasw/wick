package omp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// omp keeps a transcript per profile, in
// <home>/<PI_CONFIG_DIR|.omp>/profiles/<p>/agent/sessions/<cwd-slug>/<ts>_<id>.jsonl,
// and `--resume <id>` only looks in the running profile. A wick session
// moved to another omp instance (another profile) would lose its
// conversation, so the resume is by PATH when the id lives elsewhere:
// omp appends to that one file whichever profile runs the turn, the way
// every claude or codex instance shares one store.

// resumePathsFile caches the paths found, per wick session, so a turn
// does not rescan every profile.
const resumePathsFile = "resume-paths.json"

var resumePathsMu sync.Mutex

// resumeArg is what --resume gets for opt's resume id on profile: the id
// when profile has it, else the path another profile holds it under.
// The path is remembered in cacheDir (the wick session's .omp dir) and
// reused while the file is still there.
func resumeArg(root, profile, id, cacheDir string) string {
	if id == "" {
		return ""
	}
	cache := readResumePaths(cacheDir)
	if p := cache[id]; p != "" && cachedSessionPathOK(root, id, p) {
		if _, err := os.Stat(p); err == nil && !strings.HasPrefix(p, filepath.Join(root, "profiles", profile)+string(filepath.Separator)) {
			return p
		}
	}
	p := findSessionPath(root, profile, id)
	if p == "" {
		return id
	}
	if cacheDir != "" {
		cache[id] = p
		writeResumePaths(cacheDir, cache)
	}
	return p
}

// cachedSessionPathOK holds a remembered path to what findSessionPath
// would have found: a transcript for id in an omp store under root. The
// cache sits in the session workspace, so it is checked, not trusted.
func cachedSessionPathOK(root, id, p string) bool {
	if id == "" || strings.ContainsAny(id, `/\`) || !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return false
	}
	name := "*_" + globEscape(id) + "*.jsonl"
	for _, pat := range []string{
		filepath.Join(globEscape(root), "profiles", "*", "agent", "sessions", "*", name),
		filepath.Join(globEscape(root), "agent", "sessions", "*", name),
	} {
		if ok, _ := filepath.Match(pat, p); ok {
			return true
		}
	}
	return false
}

func readResumePaths(dir string) map[string]string {
	m := map[string]string{}
	if dir == "" {
		return m
	}
	resumePathsMu.Lock()
	defer resumePathsMu.Unlock()
	if b, err := os.ReadFile(filepath.Join(dir, resumePathsFile)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func writeResumePaths(dir string, m map[string]string) {
	resumePathsMu.Lock()
	defer resumePathsMu.Unlock()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, resumePathsFile), b, 0o600)
}

// writersFile records, per wick session, the omp profile that last ran
// each omp session id.
const writersFile = "writers.json"

// A resumed transcript brings back the model it last ran on (omp replays
// its model_change entries). When another profile wrote that turn, the
// model may be one this account cannot use (model_not_found), so the
// resume names this instance's own model instead — both ways: on A→B→A,
// A gets A's model back, not the one B recorded last.

// foreignWriter reports whether the transcript for id was last written by
// a profile other than profile: the recorded writer, else (no record yet)
// whether resume is a path into another profile's store.
func foreignWriter(cacheDir, profile, id, resume string) bool {
	if id == "" {
		return false
	}
	if w := readJSONMap(cacheDir, writersFile)[id]; w != "" {
		return w != profile
	}
	return resume != id
}

// noteWriter records profile as the last writer of id.
func noteWriter(cacheDir, profile, id string) {
	if cacheDir == "" || id == "" {
		return
	}
	m := readJSONMap(cacheDir, writersFile)
	if m[id] == profile {
		return
	}
	m[id] = profile
	writeJSONMap(cacheDir, writersFile, m)
}

// Seams over the shared model rules (provider/modelpick.go); swapped in
// tests because both may exec `omp models`.
var (
	explicitDefaultFn = provider.ExplicitLiveDefault
	ownModelFn        = provider.InstanceOwnModel
)

// defaultModelArgs is the --model wick adds to a spawn it pins nothing
// for (no session pin, no AI router, no --model in the args):
//   - live mode with a Default model chosen: that model, always — the
//     same rule opencode's instanceDefault follows;
//   - resuming a transcript another profile wrote last: this instance's
//     own model, so the transcript's recorded one (maybe not on this
//     account) does not come back — and on A→B→A, A's own again;
//   - otherwise nothing here: buildArgs still sends ProvenDefaultModel,
//     else omp runs its own default (deliberate, see modelwatch.go).
func defaultModelArgs(ctx context.Context, ins provider.Instance, opt provider.SpawnOptions, profile, resume, cacheDir string, extra []string) []string {
	if ins.UseAIRouter || len(provider.ModelArgs(opt, nil)) > 0 || hasModelArg(extra) || hasModelArg(opt.ExtraArgs) {
		return nil
	}
	lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if m := explicitDefaultFn(lctx, ins); m != "" {
		return []string{"--model", m}
	}
	if opt.ResumeID != "" && foreignWriter(cacheDir, profile, opt.ResumeID, resume) {
		if m := ownModelFn(lctx, ins); m != "" {
			return []string{"--model", m}
		}
	}
	return nil
}

func hasModelArg(args []string) bool {
	for _, a := range args {
		if a == "--model" || a == "-m" || strings.HasPrefix(a, "--model=") {
			return true
		}
	}
	return false
}

func readJSONMap(dir, name string) map[string]string {
	m := map[string]string{}
	if dir == "" {
		return m
	}
	resumePathsMu.Lock()
	defer resumePathsMu.Unlock()
	if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func writeJSONMap(dir, name string, m map[string]string) {
	resumePathsMu.Lock()
	defer resumePathsMu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, name), b, 0o644)
}
