# Sub-agent Lock, MCP full-config, dan satu picker provider — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Kunci sub-agent role supaya behaviour-nya tidak bisa berubah dari surface mana pun, lengkapi `create_agent` MCP jadi 17 field penuh, dan ganti dua field Provider+Model di form sub-agent dengan satu picker composer yang dipakai bareng project settings.

**Architecture:** Kolom `Locked` baru di `agent_profiles`, dengan satu fungsi guard murni di package `delegation` yang dipanggil tiga surface (save UI, delete UI, `create_agent` MCP). `create_agent` bertambah tujuh input dengan semantik PATCH yang sudah berlaku. Di FE, `ProviderPicker` yang sudah ada di `common-ui` dipakai ulang; builder option-nya diekstrak dari `ProjectSettingsForm` supaya dua pemakai tidak menyalin logika yang sama.

**Tech Stack:** Go + GORM + `pkg/connector` (MCP op contract), Svelte 5 runes + Tailwind, vitest + @testing-library/svelte, `go test`.

Spec: [design.md](design.md).

## Global Constraints

- Semua teks UI dan semua `desc=` pada tag `wick:"..."` ditulis **bahasa Inggris**.
- Tidak ada nama "qiscus" di contoh/placeholder — pakai nama generik.
- Logging pakai pola zerolog: `l := log.With().Str("component", "x").Logger()`, bukan `log.Debug()` langsung.
- Jangan pernah mengedit file `_templ.go`; ubah `.templ` lalu regenerate. (Task di plan ini tidak menyentuh templ.)
- `{@const}` di Svelte hanya sah tepat di bawah `{#if}` / `{#each}` — bukan di dalam `<div>` / `<button>`. Kalau butuh nilai turunan di dalam elemen, hoist jadi `$derived` di `<script>`.
- Commit per task. Jangan commit apa pun di luar file yang task itu sebut.
- Dua kolom `Provider` + `Model` di `entity.AgentProfile` **tetap**. Tidak ada migrasi skema selain penambahan kolom `Locked`.
- `allowed_native_tools` dan `strict_mcp` **tidak** di-wire ke jalur spawn di plan ini; hanya permukaan konfigurasinya yang dilebarkan, dan desc-nya harus menyatakan itu.

---

### Task 1: Kolom `Locked` + guard murni

**Files:**
- Modify: `internal/entity/agent_profile.go` (setelah field `AllowTakeOver` / `DefaultMaxTokens`, sebelum `Disabled`)
- Create: `internal/agents/delegation/lock.go`
- Test: `internal/agents/delegation/lock_test.go`

**Interfaces:**
- Consumes: `entity.AgentProfile`.
- Produces: `delegation.ErrProfileLocked` (`error`), `delegation.CheckMutable(existing *entity.AgentProfile) error`. Task 2, 3 memanggil `CheckMutable`.

- [ ] **Step 1: Tulis test yang gagal**

Buat `internal/agents/delegation/lock_test.go`:

```go
package delegation

import (
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// A create has no existing row, and an unlocked role is ordinary. Only a
// locked row refuses — and the refusal has to say where the way out is,
// because an agent told nothing but "no" retries the same payload.
func TestCheckMutable(t *testing.T) {
	if err := CheckMutable(nil); err != nil {
		t.Fatalf("create refused: %v", err)
	}
	if err := CheckMutable(&entity.AgentProfile{Key: "reviewer"}); err != nil {
		t.Fatalf("unlocked role refused: %v", err)
	}

	err := CheckMutable(&entity.AgentProfile{Key: "reviewer", Locked: true})
	if err == nil {
		t.Fatal("locked role accepted a mutation")
	}
	if !errors.Is(err, ErrProfileLocked) {
		t.Fatalf("err = %v, want it to wrap ErrProfileLocked", err)
	}
	if !strings.Contains(err.Error(), "reviewer") {
		t.Fatalf("err = %q, want it to name the role", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unlock") {
		t.Fatalf("err = %q, want it to say how to unlock", err)
	}
}
```

- [ ] **Step 2: Jalankan, pastikan gagal**

Run: `go test ./internal/agents/delegation/ -run TestCheckMutable -v`
Expected: FAIL — `undefined: CheckMutable`, `undefined: ErrProfileLocked`, `unknown field Locked`.

- [ ] **Step 3: Tambah kolom `Locked`**

Di `internal/entity/agent_profile.go`, tepat sebelum field `Disabled`:

```go
	// Locked freezes this role's behaviour. While true, no edit and no
	// delete is accepted from ANY surface — web UI or MCP. Unlocking is a
	// UI-only action, so an agent can never widen its own definition.
	// Distinct from Disabled: a disabled role is switched off, a locked
	// role is switched in stone.
	Locked bool `gorm:"not null;default:false" json:"locked"`
```

GORM AutoMigrate menambahkan kolom ini sendiri saat boot; default `false` berarti tidak ada baris lama yang berubah perilaku.

- [ ] **Step 4: Tulis guard-nya**

Buat `internal/agents/delegation/lock.go`:

```go
package delegation

import (
	"errors"
	"fmt"

	"github.com/yogasw/wick/internal/entity"
)

// ErrProfileLocked reports a mutation aimed at a role whose Locked flag
// is set.
var ErrProfileLocked = errors.New("role is locked")

// CheckMutable reports whether a role may be changed at all.
//
// Pure on purpose: the web save path, the web delete path and the MCP
// create_agent op all have to apply exactly the same rule, and a rule
// written three times becomes three rules. A nil existing row is a
// create, which is always allowed.
//
// The message names the way out rather than only refusing — an LLM told
// nothing but "no" retries the identical payload.
func CheckMutable(existing *entity.AgentProfile) error {
	if existing == nil || !existing.Locked {
		return nil
	}
	return fmt.Errorf("%w: %q is locked; untick Locked in the web UI (Sub-agents → %s) and save, then retry",
		ErrProfileLocked, existing.Key, existing.Key)
}
```

- [ ] **Step 5: Jalankan test, pastikan lulus**

Run: `go test ./internal/agents/delegation/ -run TestCheckMutable -v`
Expected: PASS

- [ ] **Step 6: Pastikan package lain tidak rusak**

Run: `go build ./... && go test ./internal/agents/delegation/ ./internal/entity/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/entity/agent_profile.go internal/agents/delegation/lock.go internal/agents/delegation/lock_test.go
git commit -m "feat(agents): add a Locked flag to sub-agent roles"
```

---

### Task 2: Web save + delete menghormati lock

**Files:**
- Modify: `internal/tools/agents/api_agent_profiles.go` (DTO `AgentProfileItem`, `profileToItem`, `apiAgentProfileSave`, `apiAgentProfileDelete`)
- Test: `internal/tools/agents/api_agent_profiles_test.go`

**Interfaces:**
- Consumes: `delegation.CheckMutable`, `delegation.ErrProfileLocked` (Task 1).
- Produces: `lockGuardSave(existing *entity.AgentProfile, reqLocked bool) (unlockOnly bool, err error)`; field JSON `locked` pada payload `/api/agent-profiles` yang dipakai Task 7 dan 8.

- [ ] **Step 1: Tulis test yang gagal**

Tambahkan di akhir `internal/tools/agents/api_agent_profiles_test.go`:

```go
// Lock is the whole point of the feature, and save + delete both route
// through the same decision — so it is tested as a pure function rather
// than through two HTTP handlers that could drift apart.
func TestLockGuardSave(t *testing.T) {
	locked := &entity.AgentProfile{Key: "reviewer", Locked: true}
	open := &entity.AgentProfile{Key: "reviewer"}

	if unlockOnly, err := lockGuardSave(nil, false); unlockOnly || err != nil {
		t.Fatalf("create: unlockOnly=%v err=%v", unlockOnly, err)
	}
	if unlockOnly, err := lockGuardSave(open, false); unlockOnly || err != nil {
		t.Fatalf("unlocked role: unlockOnly=%v err=%v", unlockOnly, err)
	}
	// Editing a locked role while leaving it locked is the case the whole
	// feature exists to refuse.
	if _, err := lockGuardSave(locked, true); err == nil {
		t.Fatal("a locked role accepted an edit")
	}
	// Unticking Locked is the one save a locked role accepts, and it is
	// accepted ALONE: unlockOnly tells the handler to keep every stored
	// field, so an unlock cannot smuggle an edit through with it.
	unlockOnly, err := lockGuardSave(locked, false)
	if err != nil {
		t.Fatalf("unlock refused: %v", err)
	}
	if !unlockOnly {
		t.Fatal("unlock did not report unlockOnly")
	}
}

func TestProfileToItemCarriesLocked(t *testing.T) {
	if got := profileToItem(entity.AgentProfile{Locked: true}); !got.Locked {
		t.Fatal("locked did not survive profileToItem")
	}
}
```

- [ ] **Step 2: Jalankan, pastikan gagal**

Run: `go test ./internal/tools/agents/ -run 'TestLockGuardSave|TestProfileToItemCarriesLocked' -v`
Expected: FAIL — `undefined: lockGuardSave`, `got.Locked undefined`.

- [ ] **Step 3: DTO + mapper**

Di `AgentProfileItem`, setelah field `Disabled`:

```go
	Locked             bool     `json:"locked"`
```

Di `profileToItem`, setelah `Disabled: p.Disabled,`:

```go
		Locked:             p.Locked,
```

- [ ] **Step 4: Tulis `lockGuardSave`**

Letakkan tepat di atas `apiAgentProfileSave`:

```go
// lockGuardSave decides what a save may do to a role that is already
// locked.
//
// unlockOnly=true means "keep every stored field and flip Locked to
// false". Unlock and edit are deliberately two separate saves: if one
// request could do both, the lock would stop being a guard and become a
// single extra click on the way to the same change.
func lockGuardSave(existing *entity.AgentProfile, reqLocked bool) (unlockOnly bool, err error) {
	if existing == nil || !existing.Locked {
		return false, nil
	}
	if reqLocked {
		return false, delegation.CheckMutable(existing)
	}
	return true, nil
}
```

- [ ] **Step 5: Pasang di `apiAgentProfileSave`**

Tepat setelah blok `existing, err := globalDelegation.Repo.GetProfileExact(...)` dan pengecekan error-nya, sebelum `p := &entity.AgentProfile{`:

```go
	unlockOnly, lerr := lockGuardSave(existing, req.Locked)
	if lerr != nil {
		c.JSON(http.StatusConflict, map[string]string{"error": lerr.Error()})
		return
	}
	// An unlock is applied to the STORED row, not to the submitted one:
	// the form disables every other control while locked, but a hand-made
	// request must not be able to ride along on the unlock.
	if unlockOnly {
		row := *existing
		row.Locked = false
		if err := globalDelegation.Repo.SaveProfile(c.Context(), &row); err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, profileToItem(row))
		return
	}
```

Lalu di literal `p := &entity.AgentProfile{...}`, setelah `Disabled: req.Disabled,`:

```go
		Locked:             req.Locked,
```

- [ ] **Step 6: Pasang di `apiAgentProfileDelete`**

Di `apiAgentProfileDelete`, tepat setelah `if !requireProfileScope(c, existing.ProjectID) { return }`:

```go
	// Locked blocks delete too. If it did not, the lock would guard
	// nothing: the role could be removed and recreated under the same key
	// with different behaviour.
	if err := delegation.CheckMutable(existing); err != nil {
		c.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
```

Kalau `existing` di sini bukan pointer (`entity.AgentProfile`), panggil `delegation.CheckMutable(&existing)`.

- [ ] **Step 7: Jalankan test, pastikan lulus**

Run: `go test ./internal/tools/agents/ -run 'TestLockGuardSave|TestProfileToItemCarriesLocked' -v`
Expected: PASS

Run: `go build ./... && go test ./internal/tools/agents/`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/tools/agents/api_agent_profiles.go internal/tools/agents/api_agent_profiles_test.go
git commit -m "feat(agents): refuse edit and delete on a locked role"
```

---

### Task 3: MCP `create_agent` menghormati lock

**Files:**
- Modify: `internal/connectors/sub-agents/handlers.go` (`createAgent`)
- Test: `internal/connectors/sub-agents/lock_test.go` (baru)

**Interfaces:**
- Consumes: `delegation.CheckMutable` (Task 1).
- Produces: tidak ada simbol baru; hanya perilaku `createAgent`.

Catatan implementasi: `locked:false` dari MCP terhadap role yang terkunci **tidak** butuh cabang penolakan sendiri — `CheckMutable` sudah menolak lebih dulu, dengan pesan yang sudah menyebut jalan keluarnya. Yang perlu dijaga hanyalah urutannya: guard dipanggil sebelum satu field pun dibaca.

- [ ] **Step 1: Tulis test yang gagal**

Buat `internal/connectors/sub-agents/lock_test.go`:

```go
package subagents

import (
	"errors"
	"testing"

	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/entity"
)

// The MCP op and the web handler must refuse a locked role for the same
// reason and with the same words. Both go through delegation.CheckMutable,
// so this test pins the contract the handler relies on rather than
// re-deriving it here.
func TestCreateAgentRefusesLockedRole(t *testing.T) {
	err := delegation.CheckMutable(&entity.AgentProfile{Key: "reviewer", Locked: true})
	if !errors.Is(err, delegation.ErrProfileLocked) {
		t.Fatalf("err = %v, want ErrProfileLocked", err)
	}
}

// The ratchet: an agent may freeze a role, never thaw one. Once Locked is
// true, CheckMutable stops every later create_agent call — including one
// that sends locked=false — so there is no MCP path back to an editable
// role.
func TestLockIsOneWayFromMCP(t *testing.T) {
	frozen := &entity.AgentProfile{Key: "reviewer", Locked: true}
	if err := delegation.CheckMutable(frozen); err == nil {
		t.Fatal("MCP can still mutate a locked role")
	}
}
```

- [ ] **Step 2: Jalankan, pastikan lulus dulu (test kontrak)**

Run: `go test ./internal/connectors/sub-agents/ -run 'TestCreateAgentRefusesLockedRole|TestLockIsOneWayFromMCP' -v`
Expected: PASS — dua test ini mengunci kontrak Task 1. Perilaku handler-nya diverifikasi di Step 5 lewat pembacaan urutan kode + build.

- [ ] **Step 3: Pasang guard di handler**

Di `internal/connectors/sub-agents/handlers.go`, fungsi `createAgent`, tepat setelah blok:

```go
	existing, err := h.deps.svc().Repo.GetProfileExact(c.Context(), caller.projectID, key)
	if err != nil && !errors.Is(err, delegation.ErrProfileNotFound) {
		return nil, fmt.Errorf("look up role: %w", err)
	}
```

sisipkan:

```go
	// Before a single field is read: a locked role is frozen for MCP
	// entirely, and unlocking is a human action in the web UI. An agent
	// that could unlock what it locked would be guarding nothing.
	if err := delegation.CheckMutable(existing); err != nil {
		return nil, err
	}
```

- [ ] **Step 4: Bawa `locked` ikut PATCH**

Masih di `createAgent`, di literal `p := &entity.AgentProfile{...}` tambahkan setelah `AllowTakeOver:`:

```go
		Locked:             c.InputBool("locked"),
```

dan di blok carry-forward (`if existing != nil { ... }`) tambahkan:

```go
		if c.Input("locked") == "" {
			p.Locked = existing.Locked
		}
```

`c.InputBool` mengembalikan false baik untuk "false" maupun "tidak dikirim", jadi pengecekan string mentahnya yang membedakan — pola yang sudah dipakai `can_delegate` dan `allow_take_over` di fungsi yang sama.

(Field `locked` pada struct input ditambahkan di Task 4 bersama enam field lainnya. Sampai task itu selesai, `c.Input("locked")` selalu kosong dan perilakunya sama dengan sebelumnya — aman.)

- [ ] **Step 5: Verifikasi**

Run: `go build ./... && go test ./internal/connectors/sub-agents/`
Expected: PASS

Baca ulang `createAgent` dan pastikan `CheckMutable` berada **sebelum** pembacaan `description` / `system_prompt` / field lain. Kalau tidak, sebuah call yang ditolak masih akan menghasilkan pesan validasi field lebih dulu, dan pemanggil akan mengira masalahnya di payload.

- [ ] **Step 6: Commit**

```bash
git add internal/connectors/sub-agents/handlers.go internal/connectors/sub-agents/lock_test.go
git commit -m "feat(agents): make create_agent refuse a locked role"
```

---

### Task 4: `create_agent` menutup semua field profile

**Files:**
- Modify: `internal/connectors/sub-agents/connector.go` (`createAgentInput`)
- Modify: `internal/connectors/sub-agents/handlers.go` (`createAgent`)
- Test: `internal/connectors/sub-agents/connector_test.go`

**Interfaces:**
- Consumes: `delegation.NormalizeWorkspace`, `delegation.WorkspaceShared`, `delegation.WorkspaceWorktree`, `splitList`, `encodeTagIDs` (semua sudah ada di package ini).
- Produces: tujuh input baru pada op `create_agent` — `icon`, `max_tokens`, `workspace`, `disabled`, `locked`, `allowed_native_tools`, `strict_mcp`.

- [ ] **Step 1: Tulis test yang gagal**

Tambahkan di `internal/connectors/sub-agents/connector_test.go`:

```go
// create_agent is the only way an agent can define a role, so a field it
// cannot reach is a role it cannot get right. This pins the full set
// against the entity — a column added later without an input here shows
// up as a failure rather than as a silently unreachable setting.
func TestCreateAgentCoversEveryProfileField(t *testing.T) {
	var op *connector.Operation
	for _, cat := range Module(Deps{}).Operations {
		for i := range cat.Ops {
			if cat.Ops[i].Key == "create_agent" {
				op = &cat.Ops[i]
			}
		}
	}
	if op == nil {
		t.Fatal("create_agent is missing")
	}
	got := map[string]bool{}
	for _, f := range op.Input {
		got[f.Key] = true
	}
	want := []string{
		"key", "name", "description", "system_prompt", "provider", "model",
		"max_turns", "max_tokens", "allowed_tags", "allowed_native_tools",
		"strict_mcp", "can_delegate", "allow_take_over", "mode", "workspace",
		"icon", "disabled", "locked",
	}
	for _, k := range want {
		if !got[k] {
			t.Fatalf("create_agent cannot set %q", k)
		}
	}
}

// Two of those fields are stored and read by nobody. Saying so in the desc
// is the difference between an inert setting and a false promise the LLM
// will act on.
func TestUnwiredFieldsSaySo(t *testing.T) {
	var op *connector.Operation
	for _, cat := range Module(Deps{}).Operations {
		for i := range cat.Ops {
			if cat.Ops[i].Key == "create_agent" {
				op = &cat.Ops[i]
			}
		}
	}
	for _, f := range op.Input {
		if f.Key != "allowed_native_tools" && f.Key != "strict_mcp" {
			continue
		}
		if !strings.Contains(strings.ToUpper(f.Description), "NOT ENFORCED") {
			t.Fatalf("%q must say it is not enforced yet, got %q", f.Key, f.Description)
		}
	}
}
```

Nama field pada `entity.Config` yang dihasilkan `StructToConfigs` adalah `Key` dan `Description` — kalau build mengeluh soal nama field, sesuaikan ke nama yang dipakai `entity.Config` di repo ini, jangan mengubah maksud test-nya.

- [ ] **Step 2: Jalankan, pastikan gagal**

Run: `go test ./internal/connectors/sub-agents/ -run 'TestCreateAgentCoversEveryProfileField|TestUnwiredFieldsSaySo' -v`
Expected: FAIL — `create_agent cannot set "max_tokens"`.

- [ ] **Step 3: Lebarkan struct input**

Di `internal/connectors/sub-agents/connector.go`, ganti `createAgentInput` jadi:

```go
type createAgentInput struct {
	Key         string `wick:"required;desc=Stable handle other calls use, lowercase-kebab (e.g. code-reviewer)."`
	Description string `wick:"required;textarea;desc=What this role is for. Read by the delegating agent to decide when to pick it — a vague description makes the role unusable."`
	SystemPrompt string `wick:"required;textarea;desc=The role's instructions. This becomes the sub-agent's system prompt."`
	Name         string `wick:"desc=Display name. Defaults to the key."`
	Icon         string `wick:"desc=A single emoji shown beside this role in lists. Optional."`
	Provider     string `wick:"desc=Agent runtime: claude (default), codex, wick, gemini. A specific instance may be named as type/name (e.g. codex/abc)."`
	Model        string `wick:"desc=Provider-specific model id. Empty uses the provider default."`
	MaxTurns     int    `wick:"desc=Default turn budget for this role. Clamped to the system ceiling."`
	MaxTokens    int    `wick:"desc=Default token budget for one delegation of this role. 0 = the role adds no cap of its own; the per-tree budget still applies."`
	// Tool access. Narrowed against your own tags server-side, so this can
	// only ever restrict a role — it can never grant it something you do
	// not already have.
	AllowedTags   string `wick:"desc=Comma-separated tag ids limiting which tools/connectors this role may use. See list_access for what you can grant. Empty = the role inherits everything you can reach."`
	// Stored, returned, and read by nothing. Said plainly because an
	// inert field the model believes in is worse than an absent one.
	AllowedNativeTools string `wick:"desc=Comma-separated provider-native tool names (e.g. Read, Grep, WebSearch). NOT ENFORCED today: the value is stored but nothing forwards it to the spawn, so it does not restrict what this role can call."`
	StrictMCP          bool   `wick:"desc=Drop the host's own MCP servers from this role's spawn. NOT ENFORCED today: whether a spawn gets --strict-mcp-config is decided globally by the WICK_STRICT_MCP environment variable, identically for every role."`
	CanDelegate        bool   `wick:"desc=Let this role delegate and define roles of its own. Off by default: most roles should do their own work."`
	AllowTakeOver      bool   `wick:"desc=Let a human send messages into this role mid-run. Its answers are then flagged as human-steered."`
	Mode               string `wick:"desc=sync (default) returns the answer to the caller. async returns immediately and delivers later."`
	Workspace          string `wick:"desc=Default working directory for this role: shared (default), or worktree for a private git worktree. Falls back to shared with a note on a non-git project."`
	Disabled           bool   `wick:"desc=Keep the role on record but hide it from every roster. A disabled role cannot be delegated to."`
	Locked             bool   `wick:"desc=Freeze this role. Once locked, no further edit or delete is accepted over MCP — only a human can unlock it in the web UI. One-way from here: you can lock, you cannot unlock."`
}
```

- [ ] **Step 4: Pakai field baru di handler**

Di `internal/connectors/sub-agents/handlers.go`, fungsi `createAgent`:

Ganti tiga baris di literal `p := &entity.AgentProfile{...}` yang sekarang menetapkan konstanta:

```go
		AllowedNativeTools: "[]",
		StrictMCP:          true,
```

jadi:

```go
		Icon:               strings.TrimSpace(c.Input("icon")),
		AllowedNativeTools: encodeTagIDs(splitList(c.Input("allowed_native_tools"))),
		StrictMCP:          c.InputBool("strict_mcp"),
		DefaultMaxTokens:   c.InputInt("max_tokens"),
		Disabled:           c.InputBool("disabled"),
```

(`encodeTagIDs` hanya me-render `[]string` jadi JSON array; namanya menyebut tag, isinya generik. Pakai apa adanya daripada menambah encoder kedua yang identik.)

Setelah blok validasi `mode`, tambahkan validasi `workspace` dengan bentuk yang sama:

```go
	if ws := strings.TrimSpace(c.Input("workspace")); ws != "" {
		if ws != delegation.WorkspaceShared && ws != delegation.WorkspaceWorktree {
			return nil, fmt.Errorf("workspace must be %q or %q, got %q",
				delegation.WorkspaceShared, delegation.WorkspaceWorktree, ws)
		}
		p.DefaultWorkspace = ws
	}
```

Di blok carry-forward `if existing != nil { ... }`, **hapus** tiga baris yang memaksa nilai lama:

```go
		p.AllowedNativeTools = existing.AllowedNativeTools
		p.StrictMCP = existing.StrictMCP
		p.DefaultWorkspace = existing.DefaultWorkspace
```

dan ganti dengan carry-forward per-field yang sebenarnya:

```go
		if strings.TrimSpace(c.Input("icon")) == "" {
			p.Icon = existing.Icon
		}
		if strings.TrimSpace(c.Input("allowed_native_tools")) == "" {
			p.AllowedNativeTools = existing.AllowedNativeTools
		}
		if c.Input("strict_mcp") == "" {
			p.StrictMCP = existing.StrictMCP
		}
		if c.InputInt("max_tokens") <= 0 {
			p.DefaultMaxTokens = existing.DefaultMaxTokens
		}
		if c.Input("disabled") == "" {
			p.Disabled = existing.Disabled
		}
		if strings.TrimSpace(c.Input("workspace")) == "" {
			p.DefaultWorkspace = existing.DefaultWorkspace
		}
```

Untuk role BARU, `StrictMCP` harus tetap default `true` seperti sebelumnya kalau pemanggil diam. Tambahkan tepat setelah literal `p := ...`:

```go
	// A new role keeps the historical default when the caller says nothing.
	// Reading the raw input distinguishes "false" from "not mentioned".
	if existing == nil && c.Input("strict_mcp") == "" {
		p.StrictMCP = true
	}
```

- [ ] **Step 5: Jalankan test, pastikan lulus**

Run: `go test ./internal/connectors/sub-agents/ -v`
Expected: PASS (termasuk `TestEveryOperationIsDescribed` yang tetap menghitung 9 op — plan ini tidak menambah op)

Run: `go build ./...`
Expected: sukses

- [ ] **Step 6: Commit**

```bash
git add internal/connectors/sub-agents/connector.go internal/connectors/sub-agents/handlers.go internal/connectors/sub-agents/connector_test.go
git commit -m "feat(agents): let create_agent set every sub-agent field"
```

---

### Task 5: API profil mengirim daftar provider berikut model-nya

**Files:**
- Modify: `internal/tools/agents/api_agent_profiles.go` (`leaderProviderOptions` dihapus, payload `providers` → `provider_list`, helper tipe provider)
- Test: `internal/tools/agents/api_agent_profiles_test.go`

**Interfaces:**
- Consumes: `projectProviderList(c *tool.Ctx) []ProviderListItem` dari `api_projects.go` (sudah ada).
- Produces: `providerTypeOf(v string) string`; kunci JSON `provider_list` pada `GET /api/agent-profiles`, dipakai Task 7.

- [ ] **Step 1: Tulis test yang gagal**

Tambahkan di `internal/tools/agents/api_agent_profiles_test.go`:

```go
// Provider values now carry an instance and may carry a pinned model
// ("wick/wick::cc/claude-fable-5"). Every rule that used to key off a
// bare type — leader capability above all — has to strip both first, or
// a role on a named instance silently loses can_delegate on every save.
func TestProviderTypeOf(t *testing.T) {
	cases := map[string]string{
		"claude":                    "claude",
		"claude/claude":             "claude",
		"codex/abc":                 "codex",
		"wick/wick::cc/claude-fable-5": "wick",
		"":                          "",
	}
	for in, want := range cases {
		if got := providerTypeOf(in); got != want {
			t.Fatalf("providerTypeOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLeaderCapabilityUsesProviderType(t *testing.T) {
	if !leaderCapableProviders[providerTypeOf("wick/wick::cc/claude-fable-5")] {
		t.Fatal("a pinned wick instance lost its leader capability")
	}
	if leaderCapableProviders[providerTypeOf("gemini/gemini")] {
		t.Fatal("gemini must not be leader-capable")
	}
}
```

- [ ] **Step 2: Jalankan, pastikan gagal**

Run: `go test ./internal/tools/agents/ -run 'TestProviderTypeOf|TestLeaderCapabilityUsesProviderType' -v`
Expected: FAIL — `undefined: providerTypeOf`.

- [ ] **Step 3: Tulis helper**

Di `internal/tools/agents/api_agent_profiles.go`, tepat di bawah `leaderCapableProviders`:

```go
// providerTypeOf reduces a stored provider value to its bare TYPE.
//
// A profile's Provider is now "type/name", optionally with a pinned
// model appended as "::modelID" — the same shape the composer and the
// project defaults already store. Every rule expressed in types (leader
// capability, above all) has to strip both parts first; comparing the
// whole string would quietly drop can_delegate the moment a role moved
// to a named instance.
func providerTypeOf(v string) string {
	if i := strings.Index(v, "::"); i >= 0 {
		v = v[:i]
	}
	if i := strings.Index(v, "/"); i >= 0 {
		v = v[:i]
	}
	return v
}
```

- [ ] **Step 4: Pakai helper di jalur save**

Di `apiAgentProfileSave`, ganti:

```go
	if !leaderCapableProviders[req.Provider] {
		req.CanDelegate = false
	}
```

jadi:

```go
	if !leaderCapableProviders[providerTypeOf(req.Provider)] {
		req.CanDelegate = false
	}
```

- [ ] **Step 5: Ganti payload provider**

Hapus fungsi `leaderProviderOptions` seluruhnya. Di `apiAgentProfileList`, ganti baris:

```go
		"providers": leaderProviderOptions(c),
```

jadi:

```go
		// The same list the composer and the project defaults picker use,
		// so the three surfaces cannot disagree about which providers are
		// healthy or which models an instance offers.
		"provider_list": projectProviderList(c),
```

- [ ] **Step 6: Jalankan test, pastikan lulus**

Run: `go test ./internal/tools/agents/ -v`
Expected: PASS

Run: `go build ./...`
Expected: sukses (kalau ada pemanggil `leaderProviderOptions` yang tersisa, build akan menunjukkannya — hapus juga)

- [ ] **Step 7: Commit**

```bash
git add internal/tools/agents/api_agent_profiles.go internal/tools/agents/api_agent_profiles_test.go
git commit -m "feat(agents): serve the full provider list to the role editor"
```

---

### Task 6: Tipe + builder option provider dipindah ke shared

**Files:**
- Modify: `fe/common/api/src/agentProfiles.ts` (tipe `AgentProfile`, `AgentProfileList`, `emptyAgentProfile`, `listAgentProfiles`, `canLeadDelegation`)
- Create: `fe/common/api/src/providerList.ts`
- Modify: `fe/common/api/src/index.ts`
- Create: `fe/common/ui/src/provider-options.ts`
- Modify: `fe/common/ui/src/index.ts`
- Modify: `fe/agents/project-settings/src/lib/types.ts` (hapus tipe lokal, re-export dari common-api)
- Modify: `fe/agents/project-settings/src/lib/components/ProjectSettingsForm.svelte` (pakai builder bersama)
- Test: `fe/common/ui/src/__tests__/provider-options.test.ts`

**Interfaces:**
- Produces:
  - `ProviderModelItem { id: string; label: string; default: boolean; desc?: string }`
  - `ProviderListItem { type: string; name: string; models?: ProviderModelItem[] }`
  - `buildProviderOptions(list: ProviderListItem[], current: string): ComposerSelectOption[]`
  - `AgentProfile.locked: boolean`, `AgentProfileList.provider_list: ProviderListItem[]`
  - `canLeadDelegation` sekarang menerima `"type/name::model"`.
- Consumes: `ComposerSelectOption` dari `fe/common/ui/src/composer-types.ts`.

- [ ] **Step 1: Tulis test yang gagal**

Buat `fe/common/ui/src/__tests__/provider-options.test.ts`:

```ts
import { describe, test, expect } from "vitest";
import { buildProviderOptions } from "../provider-options.js";

describe("buildProviderOptions", () => {
  test("labels the canonical instance by its type alone", () => {
    const opts = buildProviderOptions([{ type: "claude", name: "claude" }], "");
    expect(opts[0].value).toBe("claude/claude");
    expect(opts[0].label).toBe("Claude");
  });

  test("keeps the full key as the label for a named instance", () => {
    const opts = buildProviderOptions([{ type: "codex", name: "abc" }], "");
    expect(opts[0].value).toBe("codex/abc");
    expect(opts[0].label).toBe("codex/abc");
  });

  test("carries an instance's models through", () => {
    const opts = buildProviderOptions(
      [{ type: "wick", name: "wick", models: [{ id: "m1", label: "M1", default: true }] }],
      "",
    );
    expect(opts[0].models?.[0].id).toBe("m1");
  });

  // A deleted or renamed instance must stay selectable, or opening the
  // form would silently move a saved role onto some other provider.
  test("surfaces a saved value that is no longer offered", () => {
    const opts = buildProviderOptions([{ type: "claude", name: "claude" }], "codex/gone::m9");
    const last = opts[opts.length - 1];
    expect(last.value).toBe("codex/gone");
    expect(last.label).toContain("unavailable");
  });

  test("does not duplicate a saved value that is still offered", () => {
    const opts = buildProviderOptions([{ type: "claude", name: "claude" }], "claude/claude::m1");
    expect(opts).toHaveLength(1);
  });

  // Every role stored before instances existed holds a bare type. It must
  // match its canonical instance, not render as a phantom "(unavailable)".
  test("a legacy bare provider matches its canonical instance", () => {
    const opts = buildProviderOptions([{ type: "claude", name: "claude" }], "claude");
    expect(opts).toHaveLength(1);
  });
});
```

- [ ] **Step 2: Jalankan, pastikan gagal**

Run: `npm --workspace=@wick-fe/common-ui run test:unit`
Expected: FAIL — cannot resolve `../provider-options.js`.

- [ ] **Step 3: Tipe provider di common-api**

Buat `fe/common/api/src/providerList.ts`:

```ts
/* The provider instances a picker may offer, as the server reports them.
   Shared because three surfaces render the same list — the composer, the
   project defaults, and the sub-agent role editor — and a fourth copy of
   these shapes would be a fourth chance to drift. */

/** One model choice under a provider instance. */
export interface ProviderModelItem {
  id: string;
  label: string;
  default: boolean;
  desc?: string;
}

/** One selectable provider instance. `type/name` is the stored key. */
export interface ProviderListItem {
  type: string;
  name: string;
  models?: ProviderModelItem[];
}
```

Tambahkan di `fe/common/api/src/index.ts`:

```ts
export type { ProviderListItem, ProviderModelItem } from "./providerList.js";
```

- [ ] **Step 4: Perbarui tipe profil**

Di `fe/common/api/src/agentProfiles.ts`:

- import tipe-nya di baris atas:

```ts
import type { ProviderListItem } from "./providerList.js";
```

- di `interface AgentProfile`, setelah `disabled: boolean;`:

```ts
  /** Frozen: no edit and no delete is accepted while true. Only the web
      UI can clear it — MCP may set it, never unset it. */
  locked: boolean;
```

- di `interface AgentProfileList`, ganti `providers: string[];` jadi:

```ts
  /** Provider instances a role may run on, with their models — the same
      list the composer and the project defaults picker use. */
  provider_list: ProviderListItem[];
```

- di `emptyAgentProfile`, setelah `disabled: false,`:

```ts
    locked: false,
```

- di `listAgentProfiles`, ganti `providers: r.providers ?? [],` jadi:

```ts
    provider_list: r.provider_list ?? [],
```

- ganti `canLeadDelegation`:

```ts
/** Reduces a stored provider value ("wick/wick::cc/claude-fable-5") to its
    bare type. Mirrors providerTypeOf in
    internal/tools/agents/api_agent_profiles.go. */
export function providerTypeOf(v: string): string {
  return v.split("::")[0].split("/")[0];
}

/** Returns the "type/name" form a picker option carries. A bare type —
    what every role stored before instances existed — becomes its canonical
    instance ("claude" → "claude/claude"), so an old row matches a real
    option instead of rendering as "(unavailable)". Mirrors
    normalizeProviderKey in internal/agents/provider/switcher.go. */
export function normalizeProviderKey(key: string): string {
  return key.includes("/") ? key : `${key}/${key}`;
}

export function canLeadDelegation(provider: string): boolean {
  return LEADER_CAPABLE_PROVIDERS.includes(providerTypeOf(provider));
}
```

- ekspor `providerTypeOf` dan `normalizeProviderKey` dari `fe/common/api/src/index.ts`, di sebelah `canLeadDelegation`.

- [ ] **Step 5: Tulis builder-nya**

Buat `fe/common/ui/src/provider-options.ts`:

```ts
import { normalizeProviderKey, type ProviderListItem } from "@wick-fe/common-api";
import type { ComposerSelectOption } from "./composer-types.js";

/**
 * buildProviderOptions turns the server's provider instances into picker
 * options.
 *
 * Each option's value is the "type/name" key the caller stores; the label
 * drops the redundant name for a canonical instance (claude/claude →
 * Claude). When `current` names an instance the server no longer offers —
 * deleted, renamed, or simply unhealthy — it is appended as an
 * "(unavailable)" option so opening the form cannot silently move a saved
 * value onto some other provider.
 *
 * `current` may carry a pinned model as "type/name::modelID"; only the
 * instance half is compared.
 */
export function buildProviderOptions(
  list: ProviderListItem[],
  current: string,
): ComposerSelectOption[] {
  const opts: ComposerSelectOption[] = list.map((p) => ({
    value: `${p.type}/${p.name}`,
    label:
      p.name === p.type ? p.type.charAt(0).toUpperCase() + p.type.slice(1) : `${p.type}/${p.name}`,
    models: (p.models ?? []).map((m) => ({
      id: m.id,
      label: m.label,
      default: m.default,
      desc: m.desc,
    })),
  }));
  const bare = (current ?? "").split("::")[0];
  const key = bare ? normalizeProviderKey(bare) : "";
  if (key && !opts.some((o) => o.value === key)) {
    opts.push({ value: key, label: `${key} (unavailable)`, models: [] });
  }
  return opts;
}
```

Tambahkan di `fe/common/ui/src/index.ts`:

```ts
export { buildProviderOptions } from "./provider-options.js";
```

- [ ] **Step 6: Jalankan test, pastikan lulus**

Run: `npm --workspace=@wick-fe/common-ui run test:unit`
Expected: PASS

- [ ] **Step 7: Pakai builder di project settings**

Di `fe/agents/project-settings/src/lib/types.ts`, hapus `interface ProviderModelItem` dan `interface ProviderListItem`, ganti dengan re-export supaya import yang sudah ada tidak putus:

```ts
export type { ProviderListItem, ProviderModelItem } from "@wick-fe/common-api";
```

Di `fe/agents/project-settings/src/lib/components/ProjectSettingsForm.svelte`, ganti seluruh blok `let providerOptions = $derived.by(() => { ... })` (beserta komentar di atasnya) dengan:

```svelte
  // Options come from the shared builder: the sub-agent role editor renders
  // the same list, and two copies of this mapping would drift.
  let providerOptions = $derived(buildProviderOptions(data?.provider_list ?? [], provider));
```

dan tambahkan `buildProviderOptions` ke import `@wick-fe/common-ui` di file itu.

- [ ] **Step 8: Verifikasi**

Run: `npm --workspace=@wick-fe/agents-project-settings run test:unit`
Expected: PASS

Run: `npm run check`
Expected: 0 error (peringatan yang sudah ada sebelumnya boleh tetap)

- [ ] **Step 9: Commit**

```bash
git add fe/common/api/src/providerList.ts fe/common/api/src/agentProfiles.ts fe/common/api/src/index.ts fe/common/ui/src/provider-options.ts fe/common/ui/src/index.ts fe/common/ui/src/__tests__/provider-options.test.ts fe/agents/project-settings/src/lib/types.ts fe/agents/project-settings/src/lib/components/ProjectSettingsForm.svelte
git commit -m "refactor(fe): share the provider option builder"
```

---

### Task 7: Satu field Provider di editor sub-agent

**Files:**
- Modify: `fe/common/ui/src/AgentProfileEditor.svelte`
- Test: `fe/common/ui/src/__tests__/AgentProfileEditor.test.ts`

**Interfaces:**
- Consumes: `buildProviderOptions` (Task 6), `ProviderPicker`, `providerTypeOf`, `canLeadDelegation`, `AgentProfile.locked`.
- Produces: prop `providerList: ProviderListItem[]` menggantikan `providers?: string[]`. Task 8 memperbarui dua pemanggilnya.

- [ ] **Step 1: Tulis test yang gagal**

Tambahkan di `fe/common/ui/src/__tests__/AgentProfileEditor.test.ts`:

```ts
  // Provider and model used to be two controls: a type dropdown and a free
  // text box nobody could fill correctly. One picker that descends to the
  // model replaces both, and the packed value is split on save.
  test("saves a picked provider and model into their own fields", async () => {
    const onsave = vi.fn();
    render(AgentProfileEditor, {
      profile: profile({
        key: "k",
        description: "d",
        provider: "wick/wick",
        model: "cc/claude-fable-5",
      }),
      providerList: [
        { type: "wick", name: "wick", models: [{ id: "cc/claude-fable-5", label: "Fable", default: true }] },
      ],
      onsave,
    });

    await fireEvent.click(screen.getByRole("button", { name: /save/i }));
    const saved = onsave.mock.calls[0][0];
    expect(saved.provider).toBe("wick/wick");
    expect(saved.model).toBe("cc/claude-fable-5");
  });

  // A role stored before instances existed holds a bare type. It must show
  // as the instance it means, not as a phantom "(unavailable)" option.
  test("renders a legacy bare provider as its canonical instance", () => {
    render(AgentProfileEditor, {
      profile: profile({ key: "k", description: "d", provider: "claude" }),
      providerList: [{ type: "claude", name: "claude" }],
      onsave: vi.fn(),
    });
    expect(screen.queryByText(/unavailable/i)).toBeNull();
  });

  test("there is no separate model field any more", () => {
    const { container } = render(AgentProfileEditor, {
      profile: profile({ key: "k", description: "d" }),
      providerList: [{ type: "claude", name: "claude" }],
      onsave: vi.fn(),
    });
    expect(container.textContent).not.toContain("Empty uses the provider default");
  });

  // can_delegate keys off the provider TYPE. A role pinned to an instance
  // and a model must not lose the checkbox.
  test("keeps can_delegate editable on a pinned leader-capable instance", () => {
    const { container } = render(AgentProfileEditor, {
      profile: profile({ key: "k", description: "d", provider: "wick/wick", model: "m1" }),
      providerList: [{ type: "wick", name: "wick" }],
      onsave: vi.fn(),
    });
    const boxes = container.querySelectorAll('input[type="checkbox"]');
    expect((boxes[1] as HTMLInputElement).disabled).toBe(false);
  });
```

- [ ] **Step 2: Jalankan, pastikan gagal**

Run: `npm --workspace=@wick-fe/common-ui run test:unit`
Expected: FAIL — masih ada teks "Empty uses the provider default", dan `providerList` bukan prop yang dikenal.

- [ ] **Step 3: Ganti prop + tambah pengemasan nilai**

Di `<script>` `AgentProfileEditor.svelte`:

- import tambahan:

```ts
  import type { AgentProfile, ProviderListItem } from "@wick-fe/common-api";
  import { canLeadDelegation, normalizeProviderKey } from "@wick-fe/common-api";
  import ProviderPicker from "./ProviderPicker.svelte";
  import { buildProviderOptions } from "./provider-options.js";
```

- di `type Props`, ganti `providers?: string[];` jadi:

```ts
    /** Provider instances this role may run on, with their models. */
    providerList?: ProviderListItem[];
```

- di destructuring props, ganti `providers = ["claude", "codex", "wick", "gemini"],` jadi:

```ts
    providerList = [],
```

- tambahkan nilai turunan setelah `const isNew = ...`:

```ts
  // Provider and model are two columns but one choice. The picker speaks
  // the composer's packed form ("type/name::modelID"); the form splits it
  // back apart on the way to the server, so nothing downstream changes.
  // normalizeProviderKey is what keeps a role stored before instances
  // existed ("claude") pointing at a real option ("claude/claude").
  const providerKey = $derived(draft.provider ? normalizeProviderKey(draft.provider) : "");
  const pickerValue = $derived(draft.model ? `${providerKey}::${draft.model}` : providerKey);
  const providerOptions = $derived(buildProviderOptions(providerList, pickerValue));

  function pickProvider(v: string) {
    const i = v.indexOf("::");
    draft.provider = i < 0 ? v : v.slice(0, i);
    draft.model = i < 0 ? "" : v.slice(i + 2);
  }
```

- [ ] **Step 4: Ganti markup dua field jadi satu**

Ganti seluruh blok:

```svelte
  <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
    <LabeledInput label="Provider"> ... </LabeledInput>
    <LabeledInput label="Model" helper="Empty uses the provider default"> ... </LabeledInput>
  </div>
```

dengan:

```svelte
  <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
    <LabeledInput label="Provider" helper="Pick the instance, and its model where the provider offers a choice">
      <ProviderPicker
        options={providerOptions}
        value={pickerValue}
        onChange={pickProvider}
        placeholder="Select provider"
      />
    </LabeledInput>

    <LabeledInput label="Max turns" helper="Clamped to the system ceiling">
      <NumberInput
        value={draft.default_max_turns}
        onChange={(v) => (draft.default_max_turns = v)}
        min={1}
        disabled={readonly}
      />
    </LabeledInput>
  </div>
```

Lalu di grid bawah yang sekarang berisi "Max turns" + "Allowed native tools", hapus blok Max turns (sudah naik) dan biarkan Allowed native tools berdiri sendiri di grid yang sama — kolom keduanya kosong, yang benar karena field itu memang lebar.

`ProviderPicker` belum punya prop `disabled`. Untuk mode `readonly`, bungkus:

```svelte
      {#if readonly}
        <div class="pointer-events-none opacity-60">
          <ProviderPicker options={providerOptions} value={pickerValue} onChange={pickProvider} placeholder="Select provider" />
        </div>
      {:else}
        <ProviderPicker options={providerOptions} value={pickerValue} onChange={pickProvider} placeholder="Select provider" />
      {/if}
```

- [ ] **Step 5: Jalankan test, pastikan lulus**

Run: `npm --workspace=@wick-fe/common-ui run test:unit`
Expected: PASS. Test lama `disables can_delegate on a provider that cannot lead` memakai `provider: "gemini"` dan tetap harus lulus lewat `providerTypeOf`.

- [ ] **Step 6: Commit**

```bash
git add fe/common/ui/src/AgentProfileEditor.svelte fe/common/ui/src/__tests__/AgentProfileEditor.test.ts
git commit -m "feat(fe): one provider picker in the sub-agent editor"
```

---

### Task 8: UI lock + dua pemanggil ikut prop baru

**Files:**
- Modify: `fe/common/ui/src/AgentProfileEditor.svelte`
- Modify: `fe/agents/agent-profiles/src/App.svelte`
- Modify: `fe/agents/project-settings/src/lib/components/SubAgentsTab.svelte`
- Test: `fe/common/ui/src/__tests__/AgentProfileEditor.test.ts`

**Interfaces:**
- Consumes: `AgentProfile.locked` (Task 6), `AgentProfileList.provider_list` (Task 6), prop `providerList` (Task 7).
- Produces: tidak ada simbol baru.

- [ ] **Step 1: Tulis test yang gagal**

Tambahkan di `fe/common/ui/src/__tests__/AgentProfileEditor.test.ts`:

```ts
  // A locked role is frozen everywhere. The Locked checkbox itself has to
  // stay live, though — if it went dead with the rest, the only way out of
  // a lock would be a SQL statement.
  test("locked disables every control except Locked itself", () => {
    const { container } = render(AgentProfileEditor, {
      profile: profile({ key: "k", description: "d", locked: true }),
      providerList: [{ type: "claude", name: "claude" }],
      onsave: vi.fn(),
    });
    const boxes = Array.from(container.querySelectorAll('input[type="checkbox"]')) as HTMLInputElement[];
    // Order follows the template: strict_mcp, can_delegate, allow_take_over, disabled, locked.
    const lockedBox = boxes[boxes.length - 1];
    expect(lockedBox.disabled).toBe(false);
    expect(lockedBox.checked).toBe(true);
    for (const b of boxes.slice(0, -1)) expect(b.disabled).toBe(true);
    expect(container.querySelector("textarea")?.disabled).toBe(true);
  });

  test("locked hides Delete", () => {
    render(AgentProfileEditor, {
      profile: profile({ id: "p1", key: "k", description: "d", locked: true }),
      providerList: [{ type: "claude", name: "claude" }],
      onsave: vi.fn(),
      ondelete: vi.fn(),
    });
    expect(screen.queryByRole("button", { name: /delete/i })).toBeNull();
  });

  test("an unlocked role still offers Delete", () => {
    render(AgentProfileEditor, {
      profile: profile({ id: "p1", key: "k", description: "d" }),
      providerList: [{ type: "claude", name: "claude" }],
      onsave: vi.fn(),
      ondelete: vi.fn(),
    });
    expect(screen.getByRole("button", { name: /delete/i })).toBeTruthy();
  });
```

- [ ] **Step 2: Jalankan, pastikan gagal**

Run: `npm --workspace=@wick-fe/common-ui run test:unit`
Expected: FAIL — belum ada checkbox Locked, kontrol lain tidak ikut mati, Delete masih muncul.

- [ ] **Step 3: Turunkan state "frozen" di editor**

Di `<script>` `AgentProfileEditor.svelte`, tepat setelah `const isNew = ...`:

```ts
  // Locked freezes the role; readonly is how a non-admin sees a global
  // role. Both disable the form, but only readonly hides the way out —
  // the Locked checkbox stays live under a lock, or the lock would have
  // no key.
  const frozen = $derived(readonly || draft.locked);
```

Ganti setiap `disabled={readonly}` di markup jadi `disabled={frozen}`, **kecuali** checkbox Locked (Step 4). Field Key sudah memakai `disabled={readonly || !isNew}` → jadikan `disabled={frozen || !isNew}`. Blok `readonly` yang membungkus ProviderPicker (Task 7 Step 4) memakai `frozen` juga.

- [ ] **Step 4: Tambah checkbox Locked + banner**

Di daftar checkbox, setelah blok `Disabled`, tambahkan:

```svelte
    <label class="flex items-start gap-2 text-xs text-black-800 dark:text-white-100">
      <input
        type="checkbox"
        class="mt-0.5 accent-green-500"
        bind:checked={draft.locked}
        disabled={readonly}
      />
      <span>
        Locked
        <span class="block text-[11px] text-black-700 dark:text-black-600">
          Freezes this role: no edit and no delete, from this page or from an
          agent over MCP. Untick and save to change it again.
        </span>
      </span>
    </label>
```

Tepat di bawah blok `{#if error}` di awal `<form>`, tambahkan banner:

```svelte
  {#if draft.locked && !readonly}
    <p
      class="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-200"
    >
      Locked — untick Locked and save to edit this role. Its fields and the
      Delete button stay frozen until you do.
    </p>
  {/if}
```

- [ ] **Step 5: Sembunyikan Delete saat locked**

Ganti kondisi tombol Delete:

```svelte
      {#if ondelete && !isNew && !draft.locked}
```

- [ ] **Step 6: Jalankan test, pastikan lulus**

Run: `npm --workspace=@wick-fe/common-ui run test:unit`
Expected: PASS

- [ ] **Step 7: Perbarui dua pemanggil**

Di `fe/agents/agent-profiles/src/App.svelte`:
- ganti `let providers = $state<string[]>([]);` jadi `let providerList = $state<ProviderListItem[]>([]);` dan tambahkan `type ProviderListItem` ke import `@wick-fe/common-api`;
- di `load()`, ganti `providers = r.providers;` jadi `providerList = r.provider_list;`;
- di markup, ganti `{providers}` jadi `{providerList}`.

Di `fe/agents/project-settings/src/lib/components/SubAgentsTab.svelte`:
- ganti `let providers = $state<string[]>([]);` jadi `let providerList = $state<ProviderListItem[]>([]);` dan tambahkan tipe-nya ke import;
- di `load()`, ganti `providers = r.providers;` jadi `providerList = r.provider_list;`;
- di `$derived` `shadowed`, ganti argumen `providers` jadi `provider_list: providerList`;
- di markup, ganti `{providers}` jadi `{providerList}`.

- [ ] **Step 8: Verifikasi seluruh FE**

Run: `npm run check`
Expected: 0 error

Run: `npm test`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add fe/common/ui/src/AgentProfileEditor.svelte fe/common/ui/src/__tests__/AgentProfileEditor.test.ts fe/agents/agent-profiles/src/App.svelte fe/agents/project-settings/src/lib/components/SubAgentsTab.svelte
git commit -m "feat(fe): lock a sub-agent role from the editor"
```

---

### Task 9: Dokumentasi + graph

**Files:**
- Modify: `docs/guide/agents/sub-agents.md`
- Modify: `internal/planning/todo/subagent-lock-and-mcp-config/design.md` (baris Status)

- [ ] **Step 1: Baca halaman guide-nya**

Run: `rg -n "create_agent" docs/guide/agents/sub-agents.md`
Buka bagian yang mendaftar field `create_agent` dan bagian yang menjelaskan pengelolaan role dari web UI.

- [ ] **Step 2: Tulis bagian Locking**

Tambahkan satu bagian setelah penjelasan pengelolaan role:

```markdown
## Locking a role

A role you rely on can be frozen. Tick **Locked** on the role and save: from
then on nothing edits or deletes it — not the web form, and not an agent
calling `create_agent` over MCP. An agent that tries is told the role is
locked and where to unlock it.

Unlocking is a web-UI action, and only a web-UI action. Untick **Locked**
and save; that save changes nothing else, so editing a locked role is
deliberately two steps. An agent may lock a role it created, but it can
never unlock one — otherwise the lock would guard nothing.

Whoever can edit a role can lock and unlock it: an admin for a global role,
anyone with access to the project for a project role.
```

- [ ] **Step 3: Perbarui daftar field `create_agent`**

Tambahkan baris untuk `icon`, `max_tokens`, `workspace`, `disabled`, `locked`, `allowed_native_tools`, `strict_mcp`. Untuk dua yang terakhir, tulis apa adanya: nilainya tersimpan tapi belum ada yang membacanya, jadi tidak membatasi apa pun hari ini.

- [ ] **Step 4: Tandai design selesai**

Di `internal/planning/todo/subagent-lock-and-mcp-config/design.md`, ubah baris Status jadi:

```markdown
Status: **terimplementasi.** Rencana eksekusi + hasil verifikasi ada di
[plan.md](plan.md).
```

- [ ] **Step 5: Verifikasi penuh sekali lagi**

Run: `go build ./... && go test ./internal/...`
Expected: PASS

Run: `npm run check && npm test`
Expected: PASS

- [ ] **Step 6: Segarkan knowledge graph**

Run: `graphify update .`
Expected: selesai tanpa error (AST-only, tanpa biaya API)

- [ ] **Step 7: Commit**

```bash
git add docs/guide/agents/sub-agents.md internal/planning/todo/subagent-lock-and-mcp-config/design.md graphify-out
git commit -m "docs(agents): cover role locking and the full create_agent surface"
```
