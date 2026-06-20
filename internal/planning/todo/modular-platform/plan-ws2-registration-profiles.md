# Registration Profiles (#2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add build-time connector profiles (`full` / `agent` / `lite`) so a wick binary registers only the builtin connectors its profile allows, selected via `wick build --profile <name>`.

**Architecture:** A new `BuildProfile` ldflag var in `internal/appname` is injected by `wick build --profile`. `internal/connectors` gains a pure `profileModules(profile)` selector + `RegisterProfile(profile)` layered over the existing `RegisterBuiltins()`. The three server/worker/mcp boot sites call `RegisterProfile(appname.BuildProfile)` instead of `RegisterBuiltins()`. Profiles are allow-lists keyed by connector `Meta().Key`. Default `full` preserves current behaviour (all 7 builtins).

**Tech Stack:** Go, cobra (CLI), `go test` (table-driven), `-ldflags -X` injection.

**Reference:** design at `internal/planning/todo/modular-platform/design.md` §4.

---

## File Structure

- `internal/connectors/registry.go` (modify) — extract `builtinModules()`, add `profileModules()`, `RegisterProfile()`, profile constants, `agentConnectors()`.
- `internal/connectors/profile_test.go` (create) — table-driven test for `profileModules()`.
- `internal/appname/appname.go` (modify) — add `BuildProfile` ldflag var (default `"full"`).
- `internal/pkg/api/server.go` (modify, ~line 163) — `RegisterBuiltins()` → `RegisterProfile(appname.BuildProfile)`.
- `internal/pkg/api/server_mcp.go` (modify, ~line 144) — same.
- `internal/pkg/worker/server.go` (modify, ~line 50) — same.
- `internal/builder/config.go` (modify) — add `Profile string` field.
- `internal/builder/ldflags.go` (modify) — inject `-X .../appname.BuildProfile=<profile>`.
- `internal/builder/ldflags_test.go` (create or modify) — assert profile ldflag present.
- `cmd/cli/build.go` (modify) — add `--profile` flag + validation.

---

## Task 1: Extract `builtinModules()` (behaviour-preserving refactor)

**Files:**
- Modify: `internal/connectors/registry.go` (`RegisterBuiltins`, ~line 119-165)
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
// The active profile is baked into appname.BuildProfile via
// `wick build --profile <name>`. "full" (default and any unknown value)
// preserves the historical all-connectors behaviour.
const (
	ProfileFull  = "full"
	ProfileAgent = "agent"
	ProfileLite  = "lite"
)

// agentConnectors is the curated allow-list for the "agent" profile —
// the builtin connectors a wick-agent build realistically calls. This
// is an intentional product default; widen or narrow it with a one-line
// edit here, no architecture change required.
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

## Task 3: Add `BuildProfile` ldflag var to `internal/appname`

**Files:**
- Modify: `internal/appname/appname.go`
- Test: `internal/appname/appname_test.go` (create if absent)

- [ ] **Step 1: Write the failing test**

```go
package appname

import "testing"

func TestBuildProfile_DefaultsToFull(t *testing.T) {
	if BuildProfile != "full" {
		t.Fatalf("BuildProfile default = %q, want \"full\"", BuildProfile)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/appname/ -run TestBuildProfile -v`
Expected: FAIL — `undefined: BuildProfile`.

- [ ] **Step 3: Add the var (next to `BuildAppName` / `BuildAppVersion`, ~line 32-37)**

```go
// BuildProfile is the ldflag injection target selecting which builtin
// connectors the binary registers. Builder writes here via
// `-X github.com/yogasw/wick/internal/appname.BuildProfile=<profile>`.
// Empty / unset is treated as "full" by connectors.RegisterProfile.
var BuildProfile = "full"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/appname/ -run TestBuildProfile -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/appname/appname.go internal/appname/appname_test.go
git commit -m "feat(appname): add BuildProfile ldflag var (default full)"
```

---

## Task 4: Wire the three boot sites to `RegisterProfile`

**Files:**
- Modify: `internal/pkg/api/server.go` (~line 163)
- Modify: `internal/pkg/api/server_mcp.go` (~line 144)
- Modify: `internal/pkg/worker/server.go` (~line 50)

- [ ] **Step 1: Replace the connector call at each site**

At each location, change:

```go
connectors.RegisterBuiltins()
```

to:

```go
connectors.RegisterProfile(appname.BuildProfile)
```

Leave `tools.RegisterBuiltins()` and `jobs.RegisterBuiltins()` untouched — this plan scopes profiles to connectors (the 7-vs-rest pain). Add the import `"github.com/yogasw/wick/internal/appname"` to any of the three files that does not already import it.

- [ ] **Step 2: Confirm no stray builtin connector calls remain in boot paths**

Run: `grep -rn "connectors.RegisterBuiltins()" internal/pkg/`
Expected: no output (all three boot sites now use `RegisterProfile`).

- [ ] **Step 3: Build the whole module**

Run: `go build ./...`
Expected: builds clean (no import-cycle / unused-import errors).

- [ ] **Step 4: Run the affected packages' tests**

Run: `go test ./internal/pkg/api/... ./internal/pkg/worker/... ./internal/connectors/...`
Expected: PASS. (Default `BuildProfile == "full"` means behaviour is unchanged — every builtin still registers.)

- [ ] **Step 5: Commit**

```bash
git add internal/pkg/api/server.go internal/pkg/api/server_mcp.go internal/pkg/worker/server.go
git commit -m "feat(boot): register connectors via build profile at server/worker/mcp boot"
```

---

## Task 5: Inject `BuildProfile` via ldflags in the builder

**Files:**
- Modify: `internal/builder/config.go` (add `Profile` field)
- Modify: `internal/builder/ldflags.go` (`assembleLDFlags`)
- Test: `internal/builder/ldflags_test.go`

- [ ] **Step 1: Write the failing test**

```go
package builder

import (
	"strings"
	"testing"
)

func TestAssembleLDFlags_InjectsProfile(t *testing.T) {
	cfg := Config{AppName: "demo", AppVersion: "v1.2.3", Profile: "agent"}
	got := strings.Join(assembleLDFlags(cfg), " ")
	want := "-X github.com/yogasw/wick/internal/appname.BuildProfile=agent"
	if !strings.Contains(got, want) {
		t.Fatalf("assembleLDFlags() = %q, missing %q", got, want)
	}
}

func TestAssembleLDFlags_DefaultsProfileToFull(t *testing.T) {
	cfg := Config{AppName: "demo", AppVersion: "v1.2.3"} // Profile zero-value
	got := strings.Join(assembleLDFlags(cfg), " ")
	want := "-X github.com/yogasw/wick/internal/appname.BuildProfile=full"
	if !strings.Contains(got, want) {
		t.Fatalf("assembleLDFlags() = %q, missing %q", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/builder/ -run TestAssembleLDFlags_InjectsProfile -v`
Expected: FAIL — `unknown field 'Profile' in struct literal` (compile error) or missing flag.

- [ ] **Step 3: Add `Profile` to `Config`**

In `internal/builder/config.go`, add to the `Config` struct:

```go
// Profile selects the connector build profile baked into the binary
// (full/agent/lite). Empty is normalised to "full" in assembleLDFlags.
Profile string
```

- [ ] **Step 4: Inject the flag in `assembleLDFlags`**

In `internal/builder/ldflags.go`, inside `assembleLDFlags`, after the existing `BuildAppName` / `BuildAppVersion` appends, add:

```go
profile := cfg.Profile
if profile == "" {
	profile = "full"
}
flags = append(flags, fmt.Sprintf("-X github.com/yogasw/wick/internal/appname.BuildProfile=%s", profile))
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/builder/ -run TestAssembleLDFlags -v`
Expected: PASS (both sub-tests).

- [ ] **Step 6: Commit**

```bash
git add internal/builder/config.go internal/builder/ldflags.go internal/builder/ldflags_test.go
git commit -m "feat(builder): inject BuildProfile ldflag (default full)"
```

---

## Task 6: Add `--profile` flag to `wick build` with validation

**Files:**
- Modify: `cmd/cli/build.go`
- Test: `cmd/cli/build_profile_test.go` (create)

- [ ] **Step 1: Write the failing test**

```go
package cli

import "testing"

func TestValidateProfile(t *testing.T) {
	for _, ok := range []string{"full", "agent", "lite"} {
		if err := validateProfile(ok); err != nil {
			t.Errorf("validateProfile(%q) = %v, want nil", ok, err)
		}
	}
	if err := validateProfile("nope"); err == nil {
		t.Errorf("validateProfile(\"nope\") = nil, want error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/cli/ -run TestValidateProfile -v`
Expected: FAIL — `undefined: validateProfile`.

- [ ] **Step 3: Implement `validateProfile` + wire the flag**

In `cmd/cli/build.go`, add the validator:

```go
func validateProfile(p string) error {
	switch p {
	case "full", "agent", "lite":
		return nil
	default:
		return fmt.Errorf("invalid --profile %q (want full|agent|lite)", p)
	}
}
```

In `buildCmd()`, declare the flag variable alongside the others:

```go
var profile string
```

register it (next to the other `cmd.Flags()` calls):

```go
cmd.Flags().StringVar(&profile, "profile", "full", "Connector build profile: full|agent|lite → app.BuildProfile")
```

validate it at the top of the `RunE` body, then pass it into the builder config:

```go
if err := validateProfile(profile); err != nil {
	return err
}
```

and set `Profile: profile` on the `builder.Config{...}` literal constructed in this command.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/cli/ -run TestValidateProfile -v`
Expected: PASS.

- [ ] **Step 5: Build + smoke-check the flag is wired**

Run: `go build ./... && go run . build --help`
Expected: `--profile` appears in the help output with the `full|agent|lite` description.

- [ ] **Step 6: Commit**

```bash
git add cmd/cli/build.go cmd/cli/build_profile_test.go
git commit -m "feat(cli): add wick build --profile flag (full|agent|lite)"
```

---

## Task 7: End-to-end verification (lite build registers zero builtin connectors)

**Files:** none (verification only)

- [ ] **Step 1: Full test sweep**

Run: `go test ./internal/connectors/... ./internal/appname/... ./internal/builder/... ./cmd/cli/...`
Expected: PASS.

- [ ] **Step 2: Manual profile trace (optional but recommended)**

Build a lite binary and confirm builtin connectors are absent, then a full binary and confirm they return:

```bash
go build -ldflags "-X github.com/yogasw/wick/internal/appname.BuildProfile=lite" -o /tmp/wick-lite .
go build -ldflags "-X github.com/yogasw/wick/internal/appname.BuildProfile=full" -o /tmp/wick-full .
```

Expected: the `lite` binary's MCP/admin connector list shows none of the 7 builtins (downstream-registered connectors only); the `full` binary shows all 7. (How to list depends on the running surface — admin UI connectors page or the MCP `wick_list` op.)

- [ ] **Step 3: Update install.sh (handoff note, not this plan)**

`scripts/install.sh` gaining a profile picker that calls `wick build --profile <name>` is tracked separately (design §4.1, §9). It is NOT part of this plan — this plan delivers the `--profile` flag the installer will call.

---

## Self-Review notes

- **Spec coverage:** design §4.1 (profile concept + `wick build --profile`, default full) → Tasks 2-6. §4.2 (build tags for binary-size lite) → intentionally DEFERRED (see below). §4.3/§4.4 (reuse registry pattern, no module split) → honoured: profiles layer over `RegisterBuiltins`, no Go-module change.
- **Deferred from this plan:** (1) build-tag compilation exclusion for a genuinely smaller `lite` binary (§4.2) — the registration-profile mechanism here delivers selectable module *sets*; physical binary shrink via build tags is a follow-up. (2) profiles for tools/jobs — out of scope; connectors are the pain. (3) `install.sh` profile picker (Task 7 Step 3). Each is a small, independent follow-up.
- **Type consistency:** `ProfileFull/Agent/Lite`, `builtinModules()`, `profileModules()`, `RegisterProfile()`, `agentConnectors()`, `appname.BuildProfile`, `Config.Profile`, `validateProfile()` are used consistently across tasks.
