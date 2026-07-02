package provider

// ReproSpec declares, per provider type, which argv tokens wick's spawner adds
// that a "reproduce this spawn" command may want to strip:
//
//   - HeadlessFlags / HeadlessValueFlags / HeadlessSubcmds turn the headless
//     (JSON-streaming) spawn back into a normal interactive session.
//   - ResumeValueFlags / ResumeSubcmds drop the resume-session id so the
//     command starts fresh.
//
// This is the single source of truth for those flags. It lives beside the Type
// constants because the same knowledge drives the spawners' argv construction —
// adding a new provider type means declaring its ReproSpec here too, so the
// reproduce UI (internal/tools/agents/view) never hardcodes flag names.
type ReproSpec struct {
	// HeadlessFlags are bare flags dropped for interactive mode (e.g. -p).
	HeadlessFlags []string
	// HeadlessValueFlags are flags that also consume the following token
	// (e.g. --output-format stream-json).
	HeadlessValueFlags []string
	// HeadlessSubcmds are subcommand tokens dropped for interactive mode
	// (e.g. codex's `exec`).
	HeadlessSubcmds []string
	// ResumeValueFlags are flags carrying a resume id (e.g. --resume <id>).
	ResumeValueFlags []string
	// ResumeSubcmds are subcommand tokens carrying a resume id as their
	// following token (e.g. codex's `resume <id>`).
	ResumeSubcmds []string
}

// reproSpecs is keyed by Type. Mirrors the argv each spawner builds:
//   - claude/spawn.go: -p --verbose --input-format/-output-format stream-json
//     --include-partial-messages, resume via --resume.
//   - codex/spawn.go: `exec` subcommand + --json, resume via `resume <id>`.
//   - gemini/spawn.go: -p, resume via --resume.
var reproSpecs = map[Type]ReproSpec{
	TypeClaude: {
		HeadlessFlags:      []string{"-p", "--print", "--verbose", "--include-partial-messages"},
		HeadlessValueFlags: []string{"--input-format", "--output-format"},
		ResumeValueFlags:   []string{"--resume"},
	},
	TypeCodex: {
		HeadlessFlags:      []string{"--json", "--skip-git-repo-check"},
		HeadlessValueFlags: []string{"--sandbox", "--ask-for-approval"},
		HeadlessSubcmds:    []string{"exec"},
		ResumeSubcmds:      []string{"resume"},
	},
	TypeGemini: {
		HeadlessFlags:    []string{"-p", "--prompt"},
		ResumeValueFlags: []string{"--resume"},
	},
}

// ReproSpecFor returns the reproduce spec for a provider type. An unknown type
// yields a zero spec (nothing stripped) so reproduce falls back to the raw argv.
func ReproSpecFor(t Type) ReproSpec {
	return reproSpecs[t]
}
