# Registration Profiles (#2) Implementation Plan — config-DB

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Decision (2026-06-20, supersedes the earlier build-time ldflag draft):** Per Yoga, the profile is a **config flag stored in the DB** and set via the existing `<app> config` command — **not** env, **not** a build flag. Rationale: `install.sh` downloads a *prebuilt* binary (no build step), so selection must be runtime. The `<app> config` command already runs **without booting connectors** (`withConfigsService` opens only a short-lived `configs.Service`), so `install.sh` can set the profile right after download. See design §4 and §9.

**Goal:** Add a connector profile (`full` / `agent` / `lite`) stored as a `configs` DB row, set via `<app> config profile <name>`, read at boot so a wick binary registers only the builtin connectors its profile allows. One prebuilt binary; profile chosen at install/runtime.

**Architecture:** `profileModules(profile)` + `RegisterProfile(profile)` layer over the existing `RegisterBuiltins()` (a pure, source-agnostic registration filter). The active profile is a `configs` row (`configs.KeyProfile`, default `"full"`) read at the three boot sites via `configsSvc.Profile()` and passed to `RegisterProfile`. A new `<app> config profile` subcommand writes that row, mirroring the existing `allowed-origins` command. Default `"full"` preserves current behaviour (all 7 builtins).

**Tech Stack:** Go, gorm, cobra, `internal/configs` service, `go test` (sqlite in-memory + table-driven), `-ldflags` NOT used for profiles.

**Reference:** design at `internal/planning/todo/modular-platform/design.md` §4 (config-DB mechanism) and §9 (decision).

---

## File Structure

- `internal/connectors/registry.go` (modify) — extract `builtinModules()`, add `profileModules()`, `RegisterProfile()`, `ProfileFull/Agent/Lite`, `agentConnectors()`. [Tasks 1-2]
- `internal/connectors/profile_test.go` (create) — table-driven tests for `builtinModules()` / `profileModules()`. [Tasks 1-2]
- `internal/configs/spec.go` (modify) — add `KeyProfile` + `DefaultProfile` + an `appDefaults()` seed row. [Task 3]
- `internal/configs/service.go` (modify) — add `Profile()` accessor. [Task 3]
- `internal/configs/profile_test.go` (create) — `Profile()` default + set-value. [Task 3]
- `internal/pkg/api/server.go` (modify) — remove `RegisterBuiltins()` (~line 163); add `RegisterProfile(configsSvc.Profile())` right after `configsSvc.Bootstrap()` (~line 194). [Task 4]
- `internal/pkg/api/server_mcp.go` (modify, ~line 144) — replace `RegisterBuiltins()` with `RegisterProfile(configsSvc.Profile())` (`configsSvc` already bootstrapped ~line 65). [Task 4]
- `internal/pkg/worker/server.go` (modify) — remove `RegisterBuiltins()` (~line 50); add `RegisterProfile(configsSvc.Profile())` after `configsSvc.Bootstrap()` (~line 69). [Task 4]
- `app/config_cmd.go` (modify) — add `parseProfileArg()` + `configProfileCmd()` + register it. [Task 5]
- `app/config_profile_test.go` (create) — `parseProfileArg()` validation. [Task 5]
- **NOT changed:** `cmd/lab/root.go` keeps `connectors.RegisterBuiltins()` (the dev/lab binary stays `full`). No `internal/appname`, `internal/builder`, or `cmd/cli/build.go` changes — no ldflag, no `--profile` flag.

---

## Task 1: Extract `builtinModules()` (behaviour-preserving refactor)

**Files:**
- Modify: `internal/connectors/registry.go` (`RegisterBuiltins`, ~line 119-164)
- Test: `internal/connectors/profile_test.go`

- [ ] **Step 1: Write the failing test**

```go
package connectors

import (
	"sort"
	"testing"
)

func TestBuiltinModules_RegistersTheSevenPublicConnectors(t *testing.T) {
	got := make([]string, 0)
	for _, m := range builtinModules() {
		got = append(got, m.Meta.Key)
	}
	sort.Strings(got)

	want := []string{
		bitbucket.Meta().Key,
		github.Meta().Key,
		googleworkspace.Meta().Key,
		httprest.Meta().Key,
		loki.Meta().Key,
		phoenix.Meta().Key,
		slack.Meta().Key,
	}
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("builtinModules() count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("builtinModules()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/connectors/ -run TestBuiltinModules -v`
Expected: FAIL — `undefined: builtinModules`.

- [ ] **Step 3: Refactor `RegisterBuiltins` to use `builtinModules()`**

In `internal/connectors/registry.go`, replace the body of `RegisterBuiltins()` with a loop over a new extracted function. The module literals are moved verbatim from the current `RegisterBuiltins` body — do not change any `Meta`/`Configs`/`Operations`/`HealthCheck`/`OAuth`/`AllowSessionConfig` field.

```go
// builtinModules returns the in-house connector modules every "full"
// wick build registers. Extracted from RegisterBuiltins so profile
// selection (profileModules) can filter the same list by Meta.Key
// without duplicating the definitions.
func builtinModules() []connector.Module {
	return []connector.Module{
		{
			Meta:        withConnectorTag(github.Meta(), tags.Development),
			Configs:     entity.StructToConfigs(github.Configs{}),
			Operations:  github.Operations(),
			HealthCheck: github.HealthCheck,
		},
		{
			Meta:               withConnectorTag(httprest.Meta(), tags.API),
			Configs:            entity.StructToConfigs(httprest.Configs{}),
			Operations:         httprest.Operations(),
			AllowSessionConfig: true,
		},
		{
			Meta:        withConnectorTag(slack.Meta(), tags.Communication),
			Configs:     entity.StructToConfigs(slack.Configs{}),
			Operations:  slack.Operations(),
			HealthCheck: slack.HealthCheck,
			OAuth:       slack.SlackOAuthMeta(),
		},
		{
			Meta:       withConnectorTag(bitbucket.Meta(), tags.Development),
			Configs:    entity.StructToConfigs(bitbucket.Configs{}),
			Operations: bitbucket.Operations(),
		},
		{
			Meta:       withConnectorTag(loki.Meta(), tags.Observability),
			Configs:    entity.StructToConfigs(loki.Configs{}),
			Operations: loki.Operations(),
		},
		{
			Meta:       withConnectorTag(phoenix.Meta(), tags.Observability),
			Configs:    entity.StructToConfigs(phoenix.Configs{}),
			Operations: phoenix.Operations(),
		},
		{
			Meta:        withConnectorTag(googleworkspace.Meta(), tags.API),
			Configs:     entity.StructToConfigs(googleworkspace.Configs{}),
			Operations:  googleworkspace.Operations(),
			HealthCheck: googleworkspace.HealthCheck,
			OAuth:       googleworkspace.OAuthMeta(),
		},
	}
}

// RegisterBuiltins seeds in-house connectors every downstream wick app
// gets by default. Idempotent on Meta.Key via registerOnce.
func RegisterBuiltins() {
	for _, m := range builtinModules() {
		registerOnce(m)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/connectors/ -run TestBuiltinModules -v`
Expected: PASS.

- [ ] **Step 5: Run the full connectors package to confirm no regression**

Run: `go test ./internal/connectors/...`
Expected: PASS (existing service/registry tests unaffected — behaviour is identical).

- [ ] **Step 6: Commit**

```bash
git add internal/connectors/registry.go internal/connectors/profile_test.go
git commit -m "refactor(connectors): extract builtinModules() from RegisterBuiltins"
```

---

## Task 2: Add `profileModules()`, profile constants, and `RegisterProfile()`

**Files:**
- Modify: `internal/connectors/registry.go`
- Test: `internal/connectors/profile_test.go`

- [ ] **Step 1: Write the failing test (append to `profile_test.go`)**

```go
func keySet(mods []connector.Module) map[string]bool {
	out := map[string]bool{}
	for _, m := range mods {
		out[m.Meta.Key] = true
	}
	return out
}

func TestProfileModules(t *testing.T) {
	cases := []struct {
		profile  string
		wantKeys []string
		wantNone bool
	}{
		{profile: ProfileFull, wantKeys: []string{
			github.Meta().Key, httprest.Meta().Key, slack.Meta().Key,
			bitbucket.Meta().Key, loki.Meta().Key, phoenix.Meta().Key,
			googleworkspace.Meta().Key,
		}},
		{profile: ProfileAgent, wantKeys: []string{
			github.Meta().Key, httprest.Meta().Key, slack.Meta().Key,
		}},
		{profile: ProfileLite, wantNone: true},
		{profile: "totally-unknown", wantKeys: []string{
			github.Meta().Key, httprest.Meta().Key, slack.Meta().Key,
			bitbucket.Meta().Key, loki.Meta().Key, phoenix.Meta().Key,
			googleworkspace.Meta().Key,
		}},
	}

	for _, tc := range cases {
		t.Run(tc.profile, func(t *testing.T) {
			got := keySet(profileModules(tc.profile))
			if tc.wantNone {
				if len(got) != 0 {
					t.Fatalf("profile %q: want no modules, got %v", tc.profile, got)
				}
				return
			}
			if len(got) != len(tc.wantKeys) {
				t.Fatalf("profile %q: got %d modules, want %d", tc.profile, len(got), len(tc.wantKeys))
			}
			for _, k := range tc.wantKeys {
				if !got[k] {
					t.Errorf("profile %q: missing key %q", tc.profile, k)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/connectors/ -run TestProfileModules -v`
Expected: FAIL — `undefined: ProfileFull` / `profileModules`.

- [ ] **Step 3: Implement profiles in `registry.go`**

```go
// Build profiles select which builtin connectors a binary registers.
// The active profile is read at boot from the configs DB row
// (configs.KeyProfile) via configsSvc.Profile(). "full" (default and
// any unknown value) preserves the historical all-connectors behaviour.
const (
	ProfileFull  = "full"
	ProfileAgent = "agent"
	ProfileLite  = "lite"
)

// agentConnectors is the curated allow-list for the "agent" profile —
// the builtin connectors a wick-agent build realistically calls. This
// is an intentional product default; widen or narrow it with a one-line
// edit here, no architecture change required.
//
// Candidate to add (product decision — design §9.3): googleworkspace. Since
// the latest upstream sync it covers gmail/calendar/meet/drive/docs/sheets/
// slides, a strong default for a flagship agent. Left out of the default
// below until the product call is made (keeps the Task 2 test stable).
func agentConnectors() map[string]bool {
	return map[string]bool{
		github.Meta().Key:   true,
		httprest.Meta().Key: true,
		slack.Meta().Key:    true,
	}
}

// profileModules is the pure selector behind RegisterProfile: given a
// profile name it returns the builtin modules that profile should
// register, without touching global registry state (so it is trivially
// unit-testable).
func profileModules(profile string) []connector.Module {
	switch profile {
	case ProfileLite:
		return nil
	case ProfileAgent:
		allow := agentConnectors()
		out := make([]connector.Module, 0, len(allow))
		for _, m := range builtinModules() {
			if allow[m.Meta.Key] {
				out = append(out, m)
			}
		}
		return out
	default: // ProfileFull and any unknown value
		return builtinModules()
	}
}

// RegisterProfile seeds the builtin connectors permitted by the named
// profile. Idempotent on Meta.Key via registerOnce.
func RegisterProfile(profile string) {
	for _, m := range profileModules(profile) {
		registerOnce(m)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/connectors/ -run TestProfileModules -v`
Expected: PASS (all four sub-tests).

- [ ] **Step 5: Commit**

```bash
git add internal/connectors/registry.go internal/connectors/profile_test.go
git commit -m "feat(connectors): add build-profile connector selection (full/agent/lite)"
```

---

## Task 3: Add the `profile` config row + `Profile()` accessor to `internal/configs`

**Files:**
- Modify: `internal/configs/spec.go` (key constant + default + `appDefaults()` seed row)
- Modify: `internal/configs/service.go` (`Profile()` accessor)
- Test: `internal/configs/profile_test.go` (create)

- [ ] **Step 1: Write the failing test**

```go
package configs

import (
	"context"
	"testing"
)

func TestProfile_DefaultsToFull(t *testing.T) {
	svc := newTestSvc(t)
	if err := svc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if got := svc.Profile(); got != "full" {
		t.Fatalf("Profile() default = %q, want \"full\"", got)
	}
}

func TestProfile_ReturnsSetValue(t *testing.T) {
	svc := newTestSvc(t)
	ctx := context.Background()
	if err := svc.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if err := svc.Set(ctx, KeyProfile, "agent"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := svc.Profile(); got != "agent" {
		t.Fatalf("Profile() = %q, want \"agent\"", got)
	}
}
```

(`newTestSvc` is the existing sqlite-in-memory helper in `internal/configs/service_test.go`, same package.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/configs/ -run TestProfile -v`
Expected: FAIL — `undefined: KeyProfile` / `svc.Profile undefined`.

- [ ] **Step 3: Add the key constant + default + seed row in `spec.go`**

In the const block in `internal/configs/spec.go` (next to `KeyAllowedOrigins`), add:

```go
	KeyProfile     = "profile"
	DefaultProfile = "full"
```

Then add a seed row inside `appDefaults()` (alongside the other `entity.Config{...}` entries):

```go
		{
			Key:         KeyProfile,
			Type:        "text",
			Value:       DefaultProfile,
			Description: "Connector profile this instance registers at boot: full (all builtin connectors), agent (curated subset), or lite (none). Set via `<app> config profile <name>`; takes effect on restart.",
		},
```

- [ ] **Step 4: Add the `Profile()` accessor in `service.go`**

In `internal/configs/service.go`, next to the other typed accessors (`AppName()`, `AllowedOrigins()`, ~line 479-500), add:

```go
// Profile returns the connector profile this instance should register
// at boot (full/agent/lite). Empty/unset falls back to DefaultProfile
// ("full"), so a fresh DB behaves exactly like today.
func (s *Service) Profile() string {
	if p := strings.TrimSpace(s.Get(KeyProfile)); p != "" {
		return p
	}
	return DefaultProfile
}
```

(`strings` is already imported in `service.go` — used by `AllowedOrigins`.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/configs/ -run TestProfile -v`
Expected: PASS (both sub-tests). Then `go test ./internal/configs/...` → PASS (no regression).

- [ ] **Step 6: Commit**

```bash
git add internal/configs/spec.go internal/configs/service.go internal/configs/profile_test.go
git commit -m "feat(configs): add profile config row + Profile() accessor (default full)"
```

---

## Task 4: Wire the three boot sites to `RegisterProfile(configsSvc.Profile())`

The connector registration must run **after** `configsSvc.Bootstrap(...)` (so the profile row is in cache) and **before** anything walks `connectors.All()`. Boot order differs per site (validated against master), so the edit differs per file.

**Files:**
- Modify: `internal/pkg/api/server_mcp.go` (~line 144 — `configsSvc.Bootstrap` is already at ~line 65, before this)
- Modify: `internal/pkg/api/server.go` (remove ~line 163; add after `configsSvc.Bootstrap` ~line 194)
- Modify: `internal/pkg/worker/server.go` (remove ~line 50; add after `configsSvc.Bootstrap` ~line 69)

- [ ] **Step 1: `server_mcp.go` — replace in place (Bootstrap already precedes it)**

`configsSvc` is created and `Bootstrap`-ed at ~line 64-65, well above the connector call at ~line 144. Replace:

```go
connectors.RegisterBuiltins()
```

with:

```go
connectors.RegisterProfile(configsSvc.Profile())
```

- [ ] **Step 2: `server.go` — move the call below `configsSvc.Bootstrap`**

`connectors.RegisterBuiltins()` is at ~line 163, but `configsSvc` is only created at ~line 180 and bootstrapped at ~line 194. So:

1. **Delete** the `connectors.RegisterBuiltins()` line (~163). Leave `tools.RegisterBuiltins()` and `jobs.RegisterBuiltins()` untouched (the tools/jobs validation right below them depends on `tools.All()` / `jobs.All()`).
2. Immediately **after** the `configsSvc.Bootstrap(context.Background(), extraConfigs...)` error-check block (~line 194-196), add:

```go
	connectors.RegisterProfile(configsSvc.Profile())
```

This is well before `connectorsSvc.Bootstrap(context.Background(), connectors.All())` (~line 1032), the first consumer of `connectors.All()`.

- [ ] **Step 3: `worker/server.go` — move the call below `configsSvc.Bootstrap`**

`connectors.RegisterBuiltins()` is at ~line 50, but `configsSvc` is created at ~line 55 and bootstrapped at ~line 69. So:

1. **Delete** the `connectors.RegisterBuiltins()` line (~50). Leave `tools.RegisterBuiltins()` / `jobs.RegisterBuiltins()` untouched.
2. Immediately **after** the `configsSvc.Bootstrap(context.Background(), extraConfigs...)` error-check block (~line 69), add:

```go
	connectors.RegisterProfile(configsSvc.Profile())
```

- [ ] **Step 4: Confirm no stray builtin connector calls remain in boot paths**

Run: `grep -rn "connectors.RegisterBuiltins()" internal/pkg/`
Expected: no output. (`cmd/lab/root.go` still calls it — intentional: the dev/lab binary stays `full`. Out of scope here.)

- [ ] **Step 5: Build the whole module**

Run: `go build ./...`
Expected: builds clean (no import-cycle / unused-import errors). `template/` package errors are pre-existing and unrelated (scaffolding, not a real module).

- [ ] **Step 6: Run the affected packages' tests**

Run: `go test ./internal/pkg/api/... ./internal/pkg/worker/... ./internal/connectors/... ./internal/configs/...`
Expected: PASS. (Default profile `"full"` means behaviour is unchanged — every builtin still registers.)

- [ ] **Step 7: Commit**

```bash
git add internal/pkg/api/server.go internal/pkg/api/server_mcp.go internal/pkg/worker/server.go
git commit -m "feat(boot): register connectors via DB profile (configsSvc.Profile()) at boot"
```

---

## Task 5: Add `<app> config profile <full|agent|lite>` subcommand

**Files:**
- Modify: `app/config_cmd.go` (add `parseProfileArg` + `configProfileCmd` + register; import `internal/connectors`)
- Test: `app/config_profile_test.go` (create)

- [ ] **Step 1: Write the failing test**

```go
package app

import "testing"

func TestParseProfileArg(t *testing.T) {
	for _, ok := range []string{"full", "agent", "lite"} {
		got, err := parseProfileArg(ok)
		if err != nil || got != ok {
			t.Errorf("parseProfileArg(%q) = (%q, %v), want (%q, nil)", ok, got, err, ok)
		}
	}
	if _, err := parseProfileArg("nope"); err == nil {
		t.Errorf("parseProfileArg(\"nope\") = nil error, want error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./app/ -run TestParseProfileArg -v`
Expected: FAIL — `undefined: parseProfileArg`.

- [ ] **Step 3: Implement `parseProfileArg` + `configProfileCmd` and register it**

Add the import `"github.com/yogasw/wick/internal/connectors"` to `app/config_cmd.go`. Then add:

```go
// parseProfileArg validates a profile name for the `config profile`
// command. Returns the normalised value or an error naming the valid set.
func parseProfileArg(p string) (string, error) {
	switch p {
	case connectors.ProfileFull, connectors.ProfileAgent, connectors.ProfileLite:
		return p, nil
	default:
		return "", fmt.Errorf("invalid profile %q (want %s|%s|%s)",
			p, connectors.ProfileFull, connectors.ProfileAgent, connectors.ProfileLite)
	}
}

func configProfileCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "profile <full|agent|lite>",
		Short: "Set the connector profile this instance registers at boot. Takes effect on restart.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := parseProfileArg(args[0])
			if err != nil {
				return err
			}
			return withConfigsService(func(ctx context.Context, svc *configs.Service) error {
				if err := svc.Set(ctx, configs.KeyProfile, p); err != nil {
					return err
				}
				fmt.Printf("profile = %s (restart to apply)\n", p)
				return nil
			})
		},
	}
}
```

Then register it in `configCmd()`'s `cmd.AddCommand(...)` list (~line 49-54):

```go
	cmd.AddCommand(
		configListCmd(),
		configGetCmd(),
		configSetCmd(),
		configProfileCmd(),
		configAllowedOriginsCmd(),
	)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./app/ -run TestParseProfileArg -v`
Expected: PASS.

- [ ] **Step 5: Build + smoke-check the command is wired**

Run: `go build ./... && go run . config profile --help`
Expected: builds clean; help shows `Set the connector profile this instance registers at boot`. (Also `go run . config profile nope` should print the `invalid profile` error.)

- [ ] **Step 6: Commit**

```bash
git add app/config_cmd.go app/config_profile_test.go
git commit -m "feat(app): add <app> config profile <full|agent|lite> command"
```

---

## Task 6: End-to-end verification

**Files:** none (verification only)

- [ ] **Step 1: Full test sweep**

Run: `go test ./internal/connectors/... ./internal/configs/... ./internal/pkg/api/... ./internal/pkg/worker/... ./app/...`
Expected: PASS.

- [ ] **Step 2: Manual profile trace (one binary, no rebuild between profiles)**

```bash
go build -o /tmp/wick-agent .
/tmp/wick-agent config profile lite      # writes the DB row
# start the server, then inspect the connector list (admin UI / MCP wick_list)
/tmp/wick-agent config profile full      # flip back
# restart, inspect again
```

Expected: under `lite` the connector list shows **none of the 7 builtins**; under `full` all 7 return. **NOTE:** `lite` is NOT an empty connector surface — the 4 runtime-registered platform connectors (`wickmanager`, `workflow`, `notifications`, `customconnector`) still appear in every profile because they register via `connectors.Register(...)` outside `RegisterBuiltins` (design §4.5). Verify "none of the 7 builtins," not "zero connectors total." The same binary changes behaviour purely from the DB row — no env, no rebuild.

- [ ] **Step 3: Update install.sh (handoff note, not this plan)**

`scripts/install.sh` gaining a profile picker that, after downloading the prebuilt binary, runs `<app> config profile <name>` is tracked separately (design §4.1, §9). It is NOT part of this plan — this plan delivers the `config profile` command + the boot-time read the installer relies on. The command works pre-server-start because `withConfigsService` opens only a short-lived `configs.Service` (no connectors, no HTTP).

---

## Self-Review notes

- **Spec coverage:** design §4.1 (profile = DB config row, set via `<app> config`, read at boot, default full) → Tasks 2-5. §4.3 (reuse registry pattern, no module split) → honoured: `profileModules`/`RegisterProfile` layer over `RegisterBuiltins`. §4.5 (4 runtime connectors out of profile scope) → honoured: Task 4 only swaps the `RegisterBuiltins` call; the inline `connectors.Register(...)` sites are untouched. §4.2 (build-tags for a genuinely smaller `lite` binary) → intentionally DEFERRED (registration-only here; no physical size reduction).
- **Validated vs upstream master (latest sync):** `RegisterBuiltins` (registry.go:119, exactly 7); boot sites + Bootstrap timing — `server.go` register@163 / `configsSvc`@180 / `Bootstrap`@194 / `connectors.All()`@1032; `server_mcp.go` `Bootstrap`@65 before register@144; `worker/server.go` register@50 / `configsSvc`@55 / `Bootstrap`@69. `configs.Service` API: `NewService`, `Bootstrap`, `Get`, `Set`, typed accessors (`AllowedOrigins`), `appDefaults()` in spec.go, `newTestSvc` sqlite harness. `<app> config` command + `withConfigsService` (no-connectors boot) in `app/config_cmd.go`.
- **Changed vs the earlier ldflag draft:** DROPPED `appname.BuildProfile` ldflag, `internal/builder` ldflag injection, and `wick build --profile`. Profile source is now the `configs` DB row, set via `<app> config profile`, per Yoga's decision (design §9). `cmd/lab/root.go` intentionally keeps `RegisterBuiltins()` (dev/lab = `full`).
- **Type consistency:** `ProfileFull/Agent/Lite` (`internal/connectors`), `builtinModules()`, `profileModules()`, `RegisterProfile()`, `agentConnectors()`, `configs.KeyProfile` / `configs.DefaultProfile`, `configsSvc.Profile()`, `parseProfileArg()`, `configProfileCmd()` are used consistently across tasks.
