# Decouple Upgrade from Scaffold (#1) — Investigation + Fix Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.
>
> **This is an investigation-first plan.** The exact mechanism behind "update terkendala di wick CLI" is NOT yet confirmed (design §3.3). Phase A reproduces and pins down the real pain; its final task is to author Phase B (the concrete TDD fix) from the findings. Do NOT skip Phase A — guessing the fix risks solving the wrong problem.

**Goal:** Confirm why upgrading wick via the CLI is painful, then decouple core upgrade from the user's scaffolded files so `wick upgrade` never clobbers `main.go` / customisations and updating core does not force a re-scaffold.

**Architecture:** Phase A traces the `wick upgrade` + `wick init` scaffold paths and reproduces the pain in a throwaway downstream app. Phase B implements the decoupling the findings justify — the design's target is a *stable entrypoint contract* (`main.go` only picks a profile + calls `app.Run()`; see design §3.2 and the ws2 plan).

**Tech Stack:** Go, cobra, `go.mod` / `go get`, `wick init` / `wick upgrade`.

**Reference:** design at `internal/planning/todo/modular-platform/design.md` §3.

---

## Phase A — Investigation (concrete; produces a findings doc + Phase B)

### Task A1: Trace the upgrade path

**Files:** read-only — `cmd/cli/upgrade.go`, `cmd/cli/version.go`, `internal/appname/appname.go`

- [ ] **Step 1: Read `cmd/cli/upgrade.go` end to end**

Document, in `internal/planning/todo/modular-platform/findings-upgrade.md`, exactly what `runUpgrade()` mutates:
- CLI binary (`installCLI`) — how it is replaced and from where.
- `go.mod` dep version (`readWickDepVersion` / the dep bump) — what command runs (`go get`? rewrite?).
- Whether it runs `go mod tidy`, regenerates anything, or touches any scaffolded file.

- [ ] **Step 2: Identify the three update axes from design §3.1**

In the findings doc, map each axis to the code that handles it today: (a) CLI binary, (b) `go.mod` core dep, (c) user scaffold (`main.go`, registration list, `wick.yml`). Mark which axis currently has NO handling — that gap is the suspected pain.

### Task A2: Trace the scaffold / re-init path

**Files:** read-only — `cmd/cli/init.go`, `template/main.go`, `template/wick.yml`, `template/go.mod.tmpl`

- [ ] **Step 1: Determine init's clobber behaviour**

In the findings doc, record: does `wick init` / any re-scaffold path overwrite an existing `main.go` / `wick.yml`? Is there a guard (refuse if dir non-empty) or does it overwrite? Quote the relevant lines (`scaffold`, `copyInstallScripts`, `copySkillFromFS`).

- [ ] **Step 2: Note what in `main.go` is wick-version-coupled**

`template/main.go` calls `app.RegisterTool/RegisterConnector/...` then `app.Run()`. Record which of these signatures could change across a wick core bump and would force the user to hand-edit `main.go` on upgrade.

### Task A3: Reproduce the pain in a throwaway app

**Files:** none in-repo — scratch dir under `/tmp`

- [ ] **Step 1: Scaffold a scratch downstream app**

```bash
cd /tmp && rm -rf wick-upgrade-probe
# Use the locally built wick CLI:
( cd /home/agung/qiscus/tools/wick && go build -o /tmp/wick . )
/tmp/wick init wick-upgrade-probe --skip-setup
```

- [ ] **Step 2: Pin an older core dep, add a fake customisation, then upgrade**

In `/tmp/wick-upgrade-probe`: add a recognizable comment / extra `RegisterTool` block to `main.go`, set the `go.mod` `github.com/yogasw/wick` require to a version one release behind latest, then run the upgrade and record what happens.

```bash
cd /tmp/wick-upgrade-probe
# record main.go + go.mod BEFORE
cp main.go /tmp/main.before && cp go.mod /tmp/gomod.before
/tmp/wick upgrade   # answer prompts; capture full stdout to findings doc
# record AFTER and diff
diff /tmp/main.before main.go    | tee -a /home/agung/qiscus/tools/wick/internal/planning/todo/modular-platform/findings-upgrade.md
diff /tmp/gomod.before go.mod    | tee -a /home/agung/qiscus/tools/wick/internal/planning/todo/modular-platform/findings-upgrade.md
```

- [ ] **Step 2 (verify):** Confirm in the findings doc whether the user's `main.go` customisation survived, whether `go mod tidy` succeeded, and whether the CLI/dep versions ended aligned. This is the empirical answer to "what does terkendala mean".

### Task A4: Conclude and author Phase B

- [ ] **Step 1: Write the verdict**

In `findings-upgrade.md`, state the confirmed root cause in one paragraph and pick the fix shape from these candidates (or a better one the findings reveal):
- **C1 — Entrypoint contract stability:** shrink scaffolded `main.go` to `app.Run()` only (profile-driven, per ws2), so a core bump never requires editing `main.go`.
- **C2 — Non-destructive upgrade:** make any re-scaffold path refuse to overwrite existing user files (or write `.new` siblings + a diff prompt).
- **C3 — Version-drift fix:** if the pain is CLI-binary-vs-go.mod-dep drift, make `wick upgrade` bump both atomically and verify alignment.

- [ ] **Step 2: Author Phase B tasks**

Append a "Phase B — Fix" section to THIS file with bite-sized TDD tasks (failing test → run → implement → run → commit) implementing the chosen candidate, using the ws2 plan's task format. Each task names exact files and full code. Do not implement before this section exists and the findings justify it.

- [ ] **Step 3: Commit the findings**

```bash
git add internal/planning/todo/modular-platform/findings-upgrade.md internal/planning/todo/modular-platform/plan-ws1-decouple-upgrade.md
git commit -m "docs(planning): upgrade-decouple investigation findings + Phase B tasks"
```

---

## Phase B — Fix (authored at the end of Phase A)

> Intentionally empty until Task A4 fills it in from the confirmed root cause. Authoring concrete TDD steps here before the investigation would be guessing — see the header.

---

## Dependencies & sequencing

- Shares the **entrypoint contract** with the ws2 (registration profiles) plan: candidate C1 assumes `main.go` becomes `app.Run()`-only with the profile baked at build time. If ws2 lands first, C1 is mostly a `template/main.go` simplification + a guard. Recommend running Phase A in parallel with ws2, then Phase B after both ws2 and the findings are in.
