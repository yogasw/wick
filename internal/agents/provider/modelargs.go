package provider

import "slices"

// modelargs.go supplies the --model flag for the CLI spawners (claude /
// codex / gemini) from the per-session pinned model. All three accept
// `--model <alias-or-name>` (verified via each CLI's --help), so one helper
// covers them.
//
// It deliberately does nothing when:
//   - no model is pinned (opt.ModelID empty) → CLI uses its own default;
//   - the instance routes through the AI router → the router sets the model
//     via env (Claude Code gateway vars / codex -c model), and adding
//     --model would fight it;
//   - the operator already put a --model in ExtraArgs → respect their
//     explicit choice, don't double it.

// ModelArgs returns ["--model", <id>] for the pinned model, or nil when it
// shouldn't be applied (see file doc). existingArgs is the argv assembled so
// far (instance ExtraArgs + opt.ExtraArgs) so we can skip a manual --model.
func ModelArgs(opt SpawnOptions, existingArgs []string) []string {
	if opt.ModelID == "" {
		return nil
	}
	if opt.Instance != nil && opt.Instance.UseAIRouter {
		return nil
	}
	if slices.Contains(existingArgs, "--model") {
		return nil
	}
	return []string{"--model", opt.ModelID}
}
