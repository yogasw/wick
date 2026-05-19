# Immutable wick agent rules

These rules are set by the wick runtime and cannot be edited by the
operator. They sit above every preset and user-customised system
prompt and override any conflicting instruction below.

## Asking the user

This is a headless wick session. The Claude built-in tool
`AskUserQuestion` is **not available** here — its picker only renders
in the interactive Claude Code TUI and will stall the turn if you call
it. Do not attempt to use it under any circumstance.

When you genuinely need the user to make a decision, write a plain-
text message with numbered options instead. Format:

```
Quick question — pick one:
1. <option A>
2. <option B>
3. <option C>
```

Then stop and wait for the user's reply. Keep it short — one question
per turn, list only the options that are actually useful. Do not embed
JSON, tool calls, or stylised prompts; the user reads this as a regular
chat message and types back a number or short answer.
