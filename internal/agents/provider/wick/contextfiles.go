package wick

import (
	"os"
	"path/filepath"
	"strings"
)

// contextfiles.go loads the ambient context files an agent conventionally
// reads from its working directory — AGENT.md and friends — so a wick
// agent behaves like Claude/codex in a project (parity, Phase 8).
//
// Scope: project-first, else session pwd. In practice the spawn's
// Workspace IS the resolved dir for both cases — a project session runs
// in the project dir, an ad-hoc session in its own pwd — so reading from
// Workspace covers the rule. The loaded text is appended to the system
// prompt (after the immutable rules + preset) via the engine.

// contextFileNames are the files wick looks for, in priority order. The
// first AGENT-style file found wins for that slot; memory/skills are
// additive. Names cover the common conventions across AI CLIs.
var contextFileNames = []struct {
	file  string
	label string
}{
	{"AGENT.md", "Project instructions (AGENT.md)"},
	{"AGENTS.md", "Project instructions (AGENTS.md)"},
	{"CLAUDE.md", "Project instructions (CLAUDE.md)"},
	{"memory.md", "Memory (memory.md)"},
	{"skills.md", "Skills (skills.md)"},
}

// contextFileMaxBytes caps each file so a giant doc can't blow the
// system-prompt / context budget.
const contextFileMaxBytes = 32 * 1024

// loadContextFiles reads the known context files from dir and returns a
// single markdown block to append to the system prompt, or "" when none
// exist. AGENT.md / AGENTS.md / CLAUDE.md are mutually exclusive (first
// found wins) so three copies of the same instructions aren't injected;
// memory.md and skills.md are always added when present.
func loadContextFiles(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	var b strings.Builder
	agentSlotFilled := false
	for _, cf := range contextFileNames {
		isAgentSlot := cf.file == "AGENT.md" || cf.file == "AGENTS.md" || cf.file == "CLAUDE.md"
		if isAgentSlot && agentSlotFilled {
			continue
		}
		content := readCapped(filepath.Join(dir, cf.file))
		if content == "" {
			continue
		}
		if isAgentSlot {
			agentSlotFilled = true
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("## ")
		b.WriteString(cf.label)
		b.WriteString("\n\n")
		b.WriteString(content)
	}
	return b.String()
}

// readCapped reads a file, trims it, and truncates to contextFileMaxBytes.
// Missing / unreadable / empty files return "".
func readCapped(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(data))
	if len(s) > contextFileMaxBytes {
		s = s[:contextFileMaxBytes] + "\n…(truncated)"
	}
	return s
}
