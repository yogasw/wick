package wick

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"
)

// toolresult.go caps how much of a tool result enters the model's context.
//
// The store already spills large events to thinking/<turn>/<event>.json
// for display, but the COPY that goes into e.history (the actual model
// prompt) was the raw, uncapped string — so a single oversized result
// (a 100MB log dump, `cat` of a big file, a connector returning
// hundreds of thousands of tokens) blew straight past the context window.
// Compaction can't save this: it summarizes the OLDEST turns, but the
// giant result is the newest one, in the tail.
//
// The fix is an ingestion cap: results above the threshold are written to
// a spill file and the context gets a head+tail preview plus a pointer,
// with explicit guidance to SEARCH the file with a script (grep/head/awk)
// rather than read it whole — reading a huge file back into context is the
// exact expense we're avoiding (a real 100MB-log session ended with the
// agent writing a script to grep it, not paging it in).

// toolResultMaxChars is the ceiling for a single tool result's context
// copy. Aligned with the shell tool's own 30k cap so behaviour is uniform
// across every tool (connectors, wick_execute, playwright, …), not just
// shell. Results at or below this pass through untouched.
const toolResultMaxChars = 30_000

// toolResultHeadChars / toolResultTailChars split the preview kept inline
// when a result is truncated: the head usually carries the schema/first
// rows, the tail the summary/exit status. Sum stays well under the cap.
const (
	toolResultHeadChars = 12_000
	toolResultTailChars = 4_000
)

// capToolResult returns the string to store in the model's history for one
// tool result. Small results pass through. Large ones are spilled to a
// file (when a spill dir is set) and replaced with a head+tail preview and
// a pointer that tells the agent to grep/head the file instead of reading
// it whole. callID scopes the spill filename to this specific tool call.
func (e *engine) capToolResult(callID, toolName, result string) string {
	if len(result) <= toolResultMaxChars {
		return result
	}

	head := result[:toolResultHeadChars]
	tail := result[len(result)-toolResultTailChars:]

	path, spillErr := e.spillToolResult(callID, result)

	var pointer string
	if spillErr == nil && path != "" {
		pointer = fmt.Sprintf(
			"Full output (%d chars) saved to %s. Do NOT read the whole file back — that just re-fills the context. "+
				"Search it with the shell instead: grep/head/tail/awk on that path to pull only the lines you need.",
			len(result), path)
	} else {
		// No spill dir (or write failed): the rest is genuinely gone, so
		// say so — the agent should re-run the tool more narrowly (add a
		// filter/limit) rather than assume a file exists.
		pointer = fmt.Sprintf(
			"Full output was %d chars and has been truncated (not saved). Re-run the command more narrowly "+
				"(grep/head/limit at the source) to get just the part you need.",
			len(result))
	}

	return fmt.Sprintf(
		"%s\n\n[tool result truncated — %d chars total from %q. %s]\n\n…(showing last %d chars)…\n%s",
		head, len(result), toolName, pointer, toolResultTailChars, tail)
}

// spillToolResult writes the full result to <toolSpillDir>/tool-out/<callID>.txt
// and returns the path. Returns ("", err) when no spill dir is configured
// or the write fails — the caller degrades to an inline-only truncation.
func (e *engine) spillToolResult(callID, result string) (string, error) {
	if e.toolSpillDir == "" {
		return "", fmt.Errorf("no spill dir")
	}
	dir := filepath.Join(e.toolSpillDir, "tool-out")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Warn().Err(err).Str("dir", dir).Msg("wick.toolresult: mkdir spill dir failed")
		return "", err
	}
	path := filepath.Join(dir, callID+".txt")
	if err := os.WriteFile(path, []byte(result), 0o644); err != nil {
		log.Warn().Err(err).Str("path", path).Msg("wick.toolresult: write spill file failed")
		return "", err
	}
	return path, nil
}
