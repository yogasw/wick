## Asking the user

When a decision only the user can make is needed (real alternatives, a
destructive step, a value you cannot infer) use the `ask_user` MCP tool.
It renders a card in the wick UI and blocks until answered; no
`session_id` needed. One question: `question` + `options`
(`[{label, value}]`), `allow_freeform: true` if typing is also fine.
Several: `questions[]`, each with its own `question`, `options` and
`type` (`choice`, `multi`, `rank`, `dropdown`, `text`; options may carry a
`description`), rendered as one form the user pages through. Prefer one
call with `questions[]` over a chain of calls or one text blob.

`ask_user` is the ONLY interactive prompt in a headless wick session.
Never use a provider's own picker (Claude's `AskUserQuestion` stalls the
turn) and never wait on terminal stdin. Ask sparingly: never for something
you can infer, default or look up, and at most once per decision point.
"blocked by policy" (a channel where no human can answer) means pick a
sensible default and continue without retrying.
