package provider

// PromptDelivery says HOW a reproduce command hands wick's user prompt to the
// CLI. wick itself never puts the prompt in argv for the stdin providers — it
// writes a stream-json line into the subprocess stdin pipe (see
// provider.Agent.Send) — which is why a reproduce command copied from the
// spawn log is silent unless it recreates that pipe.
type PromptDelivery string

const (
	// PromptInArgv means the spawn already carries the prompt as a
	// positional token (codex spawns one process per message), so the
	// reproduce command needs nothing added.
	PromptInArgv PromptDelivery = ""
	// PromptStdinStreamJSON means the prompt must be piped into stdin as
	// one stream-json user line, exactly what Agent.Send writes.
	PromptStdinStreamJSON PromptDelivery = "stdin_stream_json"
	// PromptPositional means the prompt is appended as a final argv token
	// (how every one of these CLIs takes an opening prompt when run by
	// hand in its normal interactive mode).
	PromptPositional PromptDelivery = "positional"
)

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
	// HeadlessPrompt / InteractivePrompt say how the reproduce command
	// feeds the prompt in each mode. PromptInArgv (the zero value) means
	// the argv already has it and nothing is added.
	HeadlessPrompt    PromptDelivery
	InteractivePrompt PromptDelivery
}

// reproSpecs is keyed by Type. Mirrors the argv each spawner builds:
//   - claude/spawn.go: -p --verbose --input-format/-output-format stream-json
//     --include-partial-messages, resume via --resume.
//   - codex/spawn.go: `exec` subcommand + --json, resume via `resume <id>`.
//   - gemini/spawn.go: -p, resume via --resume.
//
// Prompt delivery mirrors each spawner's Send path: claude and gemini keep one
// long-lived process and write the message to its stdin, so reproduce has to
// pipe it in; codex respawns per message with the text already appended to
// argv, so its reproduce command is complete as logged.
var reproSpecs = map[Type]ReproSpec{
	TypeClaude: {
		HeadlessFlags:      []string{"-p", "--print", "--verbose", "--include-partial-messages"},
		HeadlessValueFlags: []string{"--input-format", "--output-format"},
		ResumeValueFlags:   []string{"--resume"},
		HeadlessPrompt:     PromptStdinStreamJSON,
		InteractivePrompt:  PromptPositional,
	},
	TypeCodex: {
		HeadlessFlags:      []string{"--json", "--skip-git-repo-check"},
		HeadlessValueFlags: []string{"--sandbox", "--ask-for-approval"},
		HeadlessSubcmds:    []string{"exec"},
		ResumeSubcmds:      []string{"resume"},
	},
	TypeGemini: {
		HeadlessFlags:     []string{"-p", "--prompt"},
		ResumeValueFlags:  []string{"--resume"},
		HeadlessPrompt:    PromptStdinStreamJSON,
		InteractivePrompt: PromptPositional,
	},
}

// ReproSpecFor returns the reproduce spec for a provider type. An unknown type
// yields a zero spec (nothing stripped) so reproduce falls back to the raw argv.
func ReproSpecFor(t Type) ReproSpec {
	return reproSpecs[t]
}
