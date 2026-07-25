# Plan: Per-instance model picker for CLI providers (claude/codex/gemini)

Bikin nested model picker generik untuk semua provider (bukan cuma wick): `provider-type ▸ instance ▸ model`, adaptif (level yang cuma punya 1 pilihan auto-select). Model list = **seed default per-provider (dari alias resmi CLI) + user tambah manual** — bukan hardcode buta, dan CLI-nya sendiri terbukti tak bisa list model (headless/`/model`/`--help` semua gagal; TUI scrape rapuh & Windows butuh ConPTY). Model diterapkan saat spawn via `--model <alias>` (semua 3 CLI terima alias, dikonfirmasi dari `--help`).

---

## 0. TODO

- [x] **M1** ✅ `ProviderInstance.ModelSelect bool` + `Models []string` (userconfig) + mirror `provider.Instance` + kedua converter.
- [x] **M2** ✅ `modelseed.go`: seed per-type (claude opus/sonnet/haiku/fable; codex gpt-5.5; gemini gemini-2.5) + `Instance.EffectiveModels()` (curated else seed, nil kalau off).
- [x] **M3** ✅ `modelChoicesFor` (was wickModelChoices) kirim CLI models via `providers/options` saat ModelSelect on; ≤1 → nil (no drill).
- [x] **M4** ✅ `provider.ModelArgs(opt, existing)` sisip `--model <id>` di claude/codex/gemini spawn; skip saat unpinned / AI-router / --model sudah ada.
- [x] **M5** ✅ Reuse `SetModelID` + create-session `::modelID` parse (fitur sebelumnya) — CLI ikut otomatis.
- [x] **M6** ✅ Composer 3-level nesting `type ▸ instance ▸ model`, adaptif collapse (1 entry auto-select). `rawType`/`providerGroups`/`pickType`/`pickInstance`.
- [x] **M7** ✅ Settings via config-tag (DTO-driven, no manual form): `CLIModelConfig{ModelSelect,Models}` appended non-wick di `SeedInstanceConfig` + `ApplyInstanceConfigKey` + save handler. ConfigsTable render + per-key save otomatis.
- [x] Build + test ✅ — Go+FE hijau, `TestEffectiveModels/ModelArgs/SeedModels` pass, provider suites hijau.

---

## 1. Fakta hasil investigasi (terverifikasi)

- **CLI tak bisa list model:** `claude -p "/model"` → treated as chat; `/model` via stream-json → **"/model isn't available in this environment."**; `claude --help` → no list command (tapi `--model` terima alias/full-name). Sama untuk codex/gemini. TUI `/model` (yg nampilin Opus/Fable/Sonnet/Haiku) cuma jalan interaktif — scrape butuh PTY/ConPTY (Windows), rapuh per-versi, per-CLI beda, dan cuma dapat 4-5 alias. **Keputusan user: seed default + manual.**
- **ModelID sudah threaded** ke `SpawnOptions.ModelID` (spawner.go:131) via factory→agent, TAPI cuma wick yg baca (`wick/spawn.go:62`). claude/codex/gemini abaikan → tinggal wire.
- **`--model` diterima semua CLI:** claude/codex/gemini `--help` + catalog punya `--model` flag. Alias (opus/sonnet) valid.
- **Config lokasi:** `userconfig.ProviderInstance` (config.go:152) punya ExtraArgs/Env/AIRouterModels/WickModels — tambah `Models`+`ModelSelect` di sini. Mirror di `provider.Instance` (provider.go:65).
- **`providers/options`** (handler.go:1855 providerOptionsJSON) sudah kirim `models []{id,label,default}` per provider — sekarang cuma keisi utk wick. Tinggal isi utk CLI instance kalau ModelSelect on.
- **create-session `::modelID`** parsing + `SetModelID` sudah ada (fitur sebelumnya). CLI provider otomatis ikut.

## 2. Desain

### Config (M1/M2)
```
ProviderInstance {
  ...
  ModelSelect bool       `json:"model_select,omitempty"` // toggle: expose model picker
  Models      []string   `json:"models,omitempty"`       // user-curated aliases; empty+ModelSelect → seed
}
```
Seed defaults (`internal/agents/provider/modelseed.go`):
- claude → ["opus","sonnet","haiku","fable"]
- codex  → ["gpt-5.5","gpt-5.5-codex"] (sesuai catalog placeholder; user edit)
- gemini → ["gemini-2.5-pro","gemini-2.5-flash"]
Effective models = `Models` kalau non-empty, else seed. Label = alias itu sendiri (title-case optional). ID = alias (dipakai langsung sbg `--model`).

### providers/options (M3)
Di providerOptionsJSON, untuk instance non-wick: kalau `ModelSelect` → `models = effectiveModels(type, inst)` jadi `[{id:alias,label:alias,default:i==0}]`. Kalau off → models kosong (picker flat, no drill) — persis perilaku sekarang.

### Spawn apply (M4)
claude/codex/gemini `Spawn`: setelah `opt.ExtraArgs`, kalau `opt.ModelID != ""` DAN belum ada `--model` di ExtraArgs (hindari dobel) → `args = append(args, "--model", opt.ModelID)`. AI-router path TIDAK di-sentuh (router set model via env sendiri; skip `--model` kalau UseAIRouter).

### FE picker nesting (M6)
Sekarang Composer: flat options + 1-level model drill (`opt.models.length>1`). Yang perlu: options di-**group by provider type**. Dua pendekatan:
- (a) FE group: terima flat instance list, group by `type` jadi parent option; parent.models = instances (kalau >1) atau langsung; instance.models = model aliases. Butuh Composer dukung 2-level drill (type→instance→model).
- (b) Backend kirim sudah ter-nest.
Pilih (a) minim-churn kalau Composer bisa 2-level; else sederhanakan: **group by type hanya jika >1 instance** (kalau 1 instance, tampil flat spt sekarang). Model drill tetap di level instance.

Aturan adaptif: tiap level dengan 1 entri auto-collapse (tak perlu klik). Contoh:
- claude 1 instance, model-select off → `claude` (langsung)
- claude 2 instance (timA/timB), off → `claude ▸ timA|timB`
- claude 1 instance, on → `claude ▸ [model]`
- claude 2 instance, on → `claude ▸ timA ▸ [model]`

### FE settings (M7)
Di claude/codex/gemini instance detail: toggle "Allow model selection" + list model (add alias / remove), placeholder seed shown when empty. Reuse pola wick model list UI kalau ada.

## 3. DoD
- [ ] Instance dengan ModelSelect on + >1 model → picker drill ke model; pilih → spawn pakai `--model`.
- [ ] Multi-instance provider → nested by type; 1 instance → flat (adaptif).
- [ ] Model list = seed default sampai user edit; add/remove manual persist.
- [ ] claude/codex/gemini spawn argv berisi `--model <alias>` saat dipilih; AI-router path tak terganggu.
- [ ] Build hijau, test seed + options + spawn argv.

## 4. Out of scope
- Auto-list model dari CLI/API (terbukti tak feasible dari CLI; API = fitur lain).
- Per-model per-instance advanced config (thinking tokens dll) — cukup alias.
