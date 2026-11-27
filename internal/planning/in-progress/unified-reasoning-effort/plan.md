# Unified provider reasoning effort

Status: in progress. Initial resolver contract only; not yet wired to production UI or spawn paths.

## User-facing behavior

- Compact reasoning/gauge icon in the composer beside microphone/send. Open a lightweight effort popover, without sending a message.
- Dynamic effort gauge icon: use a reusable SVG arc and pivoting needle, not a static speedometer glyph. Map the current provider's ordered native effort stops to needle angles from left (lowest) through center to right (highest); continuously follow the slider's drag preview and settle at the committed stop. Default/unknown and automatic/non-ordinal modes are neutral, not falsely represented as a known effort; a reported concrete default may use its actual stop. Refresh on model/provider/agent switch and failover, revert on cancellation, and restore the saved angle after reopen. Use theme colors, a subtle transform transition (disabled for reduced motion), and accessible current-effort labels. No animation or UI state may mutate the provider's supported options.
- Mobile: follow the supplied ChatGPT reference, not a carousel or list of cards. Opening the gauge icon shows a compact overlay above the composer/keyboard, with a centered large current-effort label and one wide rounded horizontal discrete slider. A circular thumb snaps to native effort stops, with dot markers and highlighted selected track. Drag left/right or tap a stop; update the label immediately during drag, commit once on release/tap (cancel restores the prior choice). Include Default as its own stop; generate all other stops from the selected model's capability callback. Preserve the composer draft and keyboard layout; dim the background lightly, dismiss outside, and support touch, keyboard accessibility and reduced motion. Provider/model changes re-resolve the stop list and default; never reuse a stale slider index.
- Desktop: reuse the same discrete slider in a compact anchored popover with keyboard arrows, Home/End, Enter and Escape; accessible labels and focus restoration. Reuse the existing provider switch's visual tokens and interaction conventions.
- Always offer Default; show the actual model/provider default when known and "Provider default" when unknown. Display only native supported options, never a universal fabricated list. No selectable effort if provider cannot apply it.
- Composer changes affect the selected agent on its next turn, not the whole provider or an in-flight turn.
- Shared model picker drill: provider → instance → optional nested provider/account/live set → model → effort. Keep back/cancel navigation and pin intact; use the same effort component for all entry points.
- Agent defaults, project defaults, team defaults and per-member overrides, subagents, workflow agent nodes, and each failover target may select Default or an explicit effort. Default-model picks can choose effort only when the resolved model supports it.

## Backend contract

- Each provider integration registers a model-aware resolver callback taking context, instance and full model pin, returning native effort IDs, labels, known default and evidence source. Use live capability data when available and versioned verified metadata otherwise. Unknown, unsupported, and temporarily unavailable are distinct states. Cache per account/model/binary; invalidate with model refresh and config changes.
- Keep provider key, model pin and effort separate. Empty means inherit; explicit default means stop inheritance and delegate to selected provider/model config. Off/none are explicit native values, not aliases of Default. Never silently remap an unsupported explicit level.
- Suggested precedence: turn override > per-agent session selection > configured agent/member > team/project default > instance/model default > native default. Failover targets resolve their own effort independently and return-home restores home selection.
- Use existing ModelSets registry and full grouped pins, sessionoverride persistence where appropriate, and common provider picker components. Do not store a team member's override at session-only scope.
- Resolve/validate before spawn or live runtime update. Show requested and effective effort (unknown if not reported). If target model changed and the selection is stale, require a new selection or explicit Default. Avoid resetting running turns; handle warm RPC/CLI processes with supported runtime update or next-turn respawn.
- Preserve legacy raw CLI args/env and Wick thinking config when no new selection is set. Define precedence and avoid conflicting duplicate flags.

## Integration checklist

- [x] Initial resolver and selection contract with unit-test cases (not executed yet).
- [ ] Register capability resolvers and implement native application for Claude Code, Codex, Gemini CLI, OMP, OpenCode, Wick Native.
- [ ] Use correct vendor wire formats in Wick Native (OpenAI Chat Completions vs Responses/OpenRouter, Anthropic adaptive/budget, Gemini levels/budget).
- [ ] Discover model-specific options/defaults for nested model sets and actual supported CLI versions; no inferred capability from vendor name alone.
- [ ] Persist selection across provider, model, project, agent, team, workflow and failover config; migrate legacy settings safely.
- [ ] Wire REST/session APIs, permissions, model list metadata and typed frontend contract.
- [ ] Build reusable discrete horizontal effort slider (not a card carousel) and compact composer button; integrate both Composer and ProviderPicker model drill stacks.
- [ ] Integrate instance/project/agent/team/member/subagent/workflow settings and failover target picker.
- [ ] Carry effective effort through spawn, warm sessions, model retry, failover and return-home; prevent stale state or cross-agent leaks.
- [ ] Tests: capability contract, default/inherit/off, per-model validation, argv/env/API payloads, persistence, failover, live provider probes, mobile drag/release/tap/keyboard, dynamic gauge needle/preview/cancel/default/provider switch, desktop keyboard, reduced motion, cancel, busy turn behavior.
- [ ] Run Go and frontend tests/build; verify desktop and mobile; only then request merge to develop.

## Acceptance examples

- Two models from the same provider can report different option lists and defaults.
- An unknown default is not displayed as medium. A non-configurable model never shows bogus effort levels.
- Dragging the thumb previews the label, then commits exactly once on release; cancel restores the previous value. Switching agent immediately displays that agent's choice and regenerates the stop list for the selected model.
- Failover from a high-effort home model to a target with Default delegates to the target; returning home restores high. A target configured with a valid native effort runs with that effort.
