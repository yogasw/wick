package wick

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"
)

// compaction_store.go persists a session's compaction summary to a sidecar
// file so it survives a respawn — otherwise compaction is in-memory only
// and every fresh process either re-summarizes from scratch or (worse)
// drops the oldest turns raw via trimToBudget.
//
// The sidecar is deliberately SEPARATE from conversation.jsonl: the store
// owns that file and writes it turn-by-turn, so appending from the engine
// would race it and would also surface the summary as a system turn in the
// UI history. The sidecar keeps conversation.jsonl untouched (full history
// stays intact for the UI / export) while giving the model prompt a
// compacted view.
//
// The critical field is CoveredThrough: the number of conversation.jsonl
// turns the summary already accounts for. On load we replay ONLY
// [summary note] + turns AFTER that cutoff — the turns before it are
// represented by the summary and must NOT be replayed again (that was the
// user's exact worry: "don't keep carrying the conversation that's already
// been compacted"). conversation.jsonl is append-only and ordered, so a
// turn count is a stable cutoff across reloads.

// compactionSidecarName is the per-session sidecar file, next to
// conversation.jsonl under the session dir.
const compactionSidecarName = "compaction.json"

// compactionState is the on-disk sidecar shape. Overwritten (not appended)
// each time compaction runs, so it always holds the latest summary + the
// furthest cutoff.
type compactionState struct {
	Summary string `json:"summary"`
	// CoveredThrough is how many conversation.jsonl turns the summary
	// subsumes. Load replays [summary] + conversation[CoveredThrough:].
	CoveredThrough int `json:"covered_through"`
}

// compactionSidecarPath returns the sidecar path for a session dir. Empty
// dir → "" so callers can no-op (tests / unwired).
func compactionSidecarPath(sessionDir string) string {
	if sessionDir == "" {
		return ""
	}
	return filepath.Join(sessionDir, compactionSidecarName)
}

// readCompactionState loads the sidecar, or (nil, nil) when absent. A
// malformed sidecar is treated as absent (log + ignore) rather than
// failing the whole session load.
func readCompactionState(sessionDir string) *compactionState {
	path := compactionSidecarPath(sessionDir)
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil // absent is the common case
	}
	var st compactionState
	if err := json.Unmarshal(b, &st); err != nil {
		log.Warn().Err(err).Str("path", path).Msg("wick.compaction: malformed sidecar, ignoring")
		return nil
	}
	if st.CoveredThrough < 0 || st.Summary == "" {
		return nil
	}
	return &st
}

// writeCompactionState overwrites the sidecar atomically-ish (write temp,
// rename) so a crash mid-write can't leave a half-file that reads as
// corrupt. Best-effort: a failure is logged, not fatal (compaction still
// worked in memory for this spawn).
func writeCompactionState(sessionDir string, st compactionState) {
	path := compactionSidecarPath(sessionDir)
	if path == "" {
		return
	}
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		log.Warn().Err(err).Str("path", tmp).Msg("wick.compaction: write sidecar failed")
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Warn().Err(err).Str("path", path).Msg("wick.compaction: rename sidecar failed")
		_ = os.Remove(tmp)
	}
}

// countConversationTurns counts the lines (turns) currently in
// conversation.jsonl. Used to stamp CoveredThrough at compact time: every
// settled turn on disk is subsumed by the summary; turns that arrive later
// (this turn and beyond) sit past the cutoff and are replayed normally on
// the next load. Missing file → 0.
func countConversationTurns(sessionDir string) int {
	if sessionDir == "" {
		return 0
	}
	f, err := os.Open(filepath.Join(sessionDir, "conversation.jsonl"))
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		if len(sc.Bytes()) > 0 {
			n++
		}
	}
	return n
}
