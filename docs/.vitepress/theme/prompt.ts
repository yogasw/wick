export const AGENT_PROMPT = `Install Wick Agent (the no-Go runtime — Claude / Codex / Gemini as a Slack + Telegram + web agent host).

1. Detect my OS + arch (uname -s, uname -m). If Windows, switch to PowerShell.
2. Run the installer:
   - Linux / macOS / Termux: curl -fsSL https://yogasw.github.io/wick/install.sh | sh
   - Windows: iwr -useb https://yogasw.github.io/wick/install.ps1 | iex
3. Start it: wick-agent start  (daemon — pid + log under ~/.wick-agent/)
4. Tail the daemon log and read me the admin URL + the one-time INITIAL_CREDENTIALS so I can log into http://localhost:9425.
5. Ask me which AI CLI I have (claude / codex / gemini) and walk me through wiring it up at /tools/agents.`

export const FRAMEWORK_PROMPT = `Check if Go is installed by running: go version
If Go is not installed, help me install it first for my OS, then come back to this.

Once Go is ready:
1. Install wick: go install github.com/yogasw/wick@v0.14.20
2. Ask me: what do you want to name your project?
3. Scaffold it: wick init <name>
4. Run: wick dev — then show me what was created.`

// Backwards-compat alias for any importer that still pulls START_PROMPT —
// keep until every consumer migrates to the explicit AGENT / FRAMEWORK pair.
export const START_PROMPT = FRAMEWORK_PROMPT
