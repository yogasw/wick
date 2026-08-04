package provider

import (
	"slices"
	"strings"
)

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
	// A wick-shaped pin is not a CLI model name. wick keys its registry with
	// opaque ids ("m_0370951f-68d") and addresses a live-set leaf as
	// "<entryID>@<vendorModelID>"; handing either to a CLI produces
	// `--model m_0370951f-68d`, which the binary rejects and the spawn dies.
	//
	// This happens when a pin outlives the instance it was chosen on — a
	// session switched from wick to claude, or a sub-agent that inherited its
	// parent's wick pin. The switch path clears the pin, but a session whose
	// entry predates that (or was repaired elsewhere) still carries one, so
	// the check belongs here, at the point of use.
	//
	// Dropping the flag lets the CLI use its own default, which is what "this
	// pin does not apply to me" should mean. Refusing to spawn would strand
	// the session on a value the user cannot see.
	if isForeignModelPin(opt.ModelID) {
		return nil
	}
	return []string{"--model", opt.ModelID}
}

// isForeignModelPin reports whether a pinned model id belongs to wick's
// registry rather than being a CLI model name.
//
// Keyed on shape, not on a provider list: this runs on the spawn path, where
// the only thing available is the string itself. Both wick id forms are
// recognizable and neither is a legal CLI model name, so the test is precise
// in practice.
func isForeignModelPin(modelID string) bool {
	// A live-set leaf: "<entryID>@<vendorModelID>". No CLI model name has an
	// '@' in it.
	if strings.ContainsRune(modelID, '@') {
		return true
	}
	// A registered wick model: "m_" + uuid-ish. Real CLI aliases are words
	// ("opus", "sonnet", "gpt-5-codex"), never this shape.
	return strings.HasPrefix(modelID, "m_")
}
