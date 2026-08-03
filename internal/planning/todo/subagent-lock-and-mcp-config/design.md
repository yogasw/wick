# Sub-agent Lock, MCP full-config, dan satu picker provider (design)

Status: **terimplementasi** di branch `feat/agent-mention` (2026-08-03). Rencana
eksekusi + hasil verifikasi ada di [plan.md](plan.md).

Tiga keluhan yang ditutup dokumen ini: (1) sub-agent yang sudah mapan bisa
berubah behaviour-nya diam-diam karena tidak ada cara menguncinya, (2)
`create_agent` lewat MCP cuma menyentuh 12 dari 17 field profile sehingga AI
tidak bisa membuat role yang benar-benar sesuai, dan (3) provider + model di form
sub-agent masih dua field terpisah (dropdown tipe + text bebas) sementara project
settings sudah pakai picker composer yang bisa turun sampai model.

---

## TODO

### Terkunci

- ✓ **Lock = kolom `Locked` per profile, default false.** Locked → tidak bisa
  edit dan tidak bisa hapus, dari surface mana pun.
- ✓ **Unlock UI-only.** Otoritasnya ikut aturan yang sudah ada
  (`requireProfileScope`): global → admin, project → siapa pun yang bisa buka
  halaman project settings. Tidak ada kode otoritas baru.
- ✓ **MCP = ratchet satu arah.** `locked:true` boleh dikirim MCP; `locked:false`
  ditolak. AI boleh mengunci, tidak boleh membuka.
- ✓ **Unlock dan edit = dua kali save.** Save pada role locked hanya diterima
  kalau isinya persis "unlock"; field lain diabaikan.
- ✓ **Lock memblokir delete.** Kalau delete lolos, lock tidak menjaga apa pun —
  role tinggal dihapus lalu dibuat ulang dengan key sama, behaviour beda.
- ✓ **`create_agent` menutup 17 field, tanpa kecuali** — termasuk
  `allowed_native_tools` dan `strict_mcp` yang belum di-wire, dengan desc yang
  jujur bilang tersimpan-tapi-inert.
- ✓ **Satu field Provider, bukan dua.** `ProviderPicker` (composer-style) dipakai
  project settings dan editor sub-agent; builder option-nya diekstrak ke
  `common-ui`.
- ✓ **Dua kolom entity dipertahankan** (`Provider` + `Model`). Picker mengemas
  `type/name::model`, form memecahnya saat save. Tanpa migrasi.

### Belum diputuskan / di luar scope

- Tidak ada op `delete_agent` di MCP. Lock justru mengandalkan penghapusan tetap
  manual.
- `allowed_native_tools` dan `strict_mcp` tetap **tidak di-wire** ke jalur spawn.
  Dokumen ini hanya memperlebar permukaan konfigurasinya, bukan menegakkannya.
- Belum ada audit trail "siapa mengunci kapan". `UpdatedAt` + `CreatedBy` yang
  sudah ada dianggap cukup untuk sekarang.

---

## §1 Lock

### 1.1 Kolom

Di [internal/entity/agent_profile.go](../../../entity/agent_profile.go):

```go
// Locked freezes this role's behaviour. While true, no edit and no
// delete is accepted from ANY surface — web UI or MCP. Unlocking is a
// UI-only action, so an agent can never widen its own definition.
Locked bool `gorm:"not null;default:false" json:"locked"`
```

Default false, jadi tidak ada perubahan perilaku untuk baris yang sudah ada.

### 1.2 Guard tunggal

Aturannya ditulis sekali di package `delegation`, dipakai tiga pemanggil. Ditulis
tiga kali, ketiganya akan menyimpang:

```go
// ErrProfileLocked is returned when a mutation targets a locked role.
var ErrProfileLocked = errors.New("role is locked")

// CheckMutable reports whether a role may be changed at all. nil existing
// (a create) is always mutable.
func CheckMutable(existing *entity.AgentProfile) error
```

Pesan error menyebut jalan keluarnya, bukan cuma penolakannya — LLM yang ditolak
tanpa arahan akan mencoba lagi dengan payload yang sama.

### 1.3 Perilaku per surface

| Surface | Kondisi | Perilaku |
|---|---|---|
| `POST /api/agent-profiles` | locked, body `locked:true` | 409 `role "x" is locked — untick Locked and save first` |
| `POST /api/agent-profiles` | locked, body `locked:false` | Terima **hanya** unlock: baris disimpan dengan `Locked=false`, semua field lain memakai nilai tersimpan. Field lain di body diabaikan tanpa error. |
| `DELETE /api/agent-profiles/{id}` | locked | 409, pesan sama |
| MCP `create_agent` | role tujuan locked | error `role "x" is locked; unlock it in the web UI (Sub-agents → x), then retry` |
| MCP `create_agent` | body `locked:false` pada role locked | ditolak dengan pesan yang sama — MCP tidak pernah membuka lock |

Alasan "unlock hanya menerima unlock": kalau satu save boleh membawa unlock +
perubahan sekaligus, lock berhenti jadi pelindung dan jadi sekadar satu klik
ekstra. Dua langkah membuat perubahan behaviour selalu jadi keputusan sadar.

### 1.4 Otoritas

Tidak ada aturan baru. Save dan delete sudah lewat `requireProfileScope`
([api_agent_profiles.go](../../../tools/agents/api_agent_profiles.go)), yang
sudah menjawab persis pertanyaan "siapa yang boleh menyentuh role di scope ini":
global → admin, project → pemegang akses project. Unlock ikut jalur itu.

### 1.5 Form

[AgentProfileEditor.svelte](../../../../fe/common/ui/src/AgentProfileEditor.svelte):

- Saat `draft.locked`, semua kontrol `disabled` **kecuali** checkbox Locked.
  Kalau checkbox itu ikut mati, lock jadi permanen dan satu-satunya jalan keluar
  adalah query SQL.
- Tombol Delete disembunyikan saat locked, sejalan §1.3.
- Banner di atas form: `Locked — untick Locked and save to edit this role.`
- `readonly` (cara non-admin melihat role global) tetap seperti sekarang dan
  menang atas lock: readonly mematikan checkbox Locked juga.

---

## §2 `create_agent` full-config

`createAgentInput` di
[connector.go](../../../connectors/sub-agents/connector.go) bertambah tujuh
field. Semantik PATCH yang sudah berlaku dipertahankan: field yang tidak
disebut mempertahankan nilai lama, supaya "naikkan turn budget-nya" tidak jadi
kesempatan menghapus system prompt.

| Field baru | Kolom | Catatan pada desc |
|---|---|---|
| `icon` | `Icon` | — |
| `max_tokens` | `DefaultMaxTokens` | 0 = tidak dibatasi profile; budget per-tree tetap berlaku |
| `workspace` | `DefaultWorkspace` | `shared` (default) \| `worktree`; divalidasi seperti `mode` |
| `disabled` | `Disabled` | dua arah — AI boleh mematikan dan menghidupkan role |
| `locked` | `Locked` | ratchet satu arah, lihat §1.3 |
| `allowed_native_tools` | `AllowedNativeTools` | desc menyebut **stored but not enforced today** |
| `strict_mcp` | `StrictMCP` | desc menyebut **stored but not enforced today**; `WICK_STRICT_MCP` global yang menentukan |

Dua yang terakhir masuk supaya permukaan MCP benar-benar cermin form web. Desc
yang jujur adalah harganya: tanpa itu, LLM akan mengira sudah membatasi tool
sebuah role padahal tidak ada yang membaca field-nya.

**Deteksi "tidak disebut" untuk bool.** `c.InputBool` mengembalikan false baik
untuk "false" maupun "tidak dikirim". Handler sudah memakai pola
`c.Input("can_delegate") == ""` untuk membedakannya; field bool baru mengikuti
pola yang sama, bukan `InputBool` polos — kalau tidak, satu update `max_turns`
akan mematikan `disabled` yang sebelumnya true.

**Urutan di handler `createAgent`:**

1. resolve caller + gate yang sudah ada (harus di project, harus `can_delegate`);
2. `GetProfileExact` → `existing`;
3. **`CheckMutable(existing)`** — sebelum satu field pun dibaca;
4. tolak `locked:false` saat `existing.Locked`;
5. bangun profile seperti sekarang, plus tujuh field baru dengan carry-forward.

---

## §3 Satu picker provider

### 3.1 Yang sudah ada

`ProviderPicker` ([ProviderPicker.svelte](../../../../fe/common/ui/src/ProviderPicker.svelte))
sudah tinggal di `common-ui` dan sudah dipakai project settings serta composer.
Ia sudah turun tipe ▸ instance ▸ model, dengan filter model saat daftarnya
panjang. Tidak ada komponen baru yang perlu ditulis.

Yang kurang: builder option-nya masih terkubur sebagai `$derived` di dalam
`ProjectSettingsForm`, dan editor sub-agent belum memakainya sama sekali.

### 3.2 Ekstraksi

- Tipe `ProviderListItem` / `ProviderModelItem` pindah dari
  [fe/agents/project-settings/src/lib/types.ts](../../../../fe/agents/project-settings/src/lib/types.ts)
  ke `@wick-fe/common-api`.
- Builder pindah ke `fe/common/ui/src/provider-options.ts`:

  ```ts
  export function buildProviderOptions(
    list: ProviderListItem[],
    current: string,
  ): ComposerSelectOption[]
  ```

  Termasuk logika opsi trailing `(unavailable)` supaya instance yang dihapus
  tidak diam-diam menggeser nilai tersimpan ke provider lain.
- Dua pemakai (project settings + editor sub-agent) memenuhi ambang dedup
  fe-module, jadi ekstraksi ini wajib, bukan opsional.

### 3.3 Editor sub-agent

Field **Provider** (`Select`) dan **Model** (`TextInput`) diganti satu field
`Provider` berisi `ProviderPicker`. Slot kosong di grid dua-kolom diisi **Max
turns** yang naik dari baris bawah, supaya layout tidak berlubang.

Pengemasan nilai:

```
picker  "wick/wick::cc/claude-fable-5"
        ↓ split saat save
Provider = "wick/wick"     Model = "cc/claude-fable-5"
        ↑ join saat load
```

Dua kolom entity tetap seperti sekarang — tanpa migrasi, dan `profile.Model`
tetap dibaca `runner.go` untuk `session.SetModelID` tanpa perubahan.

Baris lama bernilai bare `claude` tetap sah: jalur spawn menormalkan lewat
`normalizeProviderKey` (`claude` → `claude/claude`), dan builder option akan
memunculkannya sebagai `(unavailable)` hanya kalau instance-nya memang hilang.

`canLeadDelegation` di
[agentProfiles.ts](../../../../fe/common/api/src/agentProfiles.ts) harus
memotong instance dan model dulu (`wick/wick::x` → `wick`) sebelum mencocokkan
`LEADER_CAPABLE_PROVIDERS` — kalau tidak, checkbox "Can delegate" akan mati
untuk setiap role yang provider-nya kini berbentuk `type/name`.

### 3.4 Backend

`GET /api/agent-profiles` mengembalikan `provider_list []ProviderListItem`
memakai `projectProviderList` yang sudah ada, menggantikan `providers []string`.
`leaderProviderOptions` dihapus — fallback "kalau cache dingin" sudah ada di
`projectProviderList`'s sumber yang sama dengan composer, jadi dua daftar
provider tidak lagi bisa berbeda pendapat soal apa yang sehat.

`AgentProfileItem` bertambah `Locked bool`, dan `apiAgentProfileSave`
menerapkan §1.3.

---

## §4 Test

**Go**

- `create_agent` menolak role locked; pesannya menyebut jalan keluar.
- `create_agent` dengan `locked:false` pada role locked ditolak; dengan
  `locked:true` pada role bebas → tersimpan locked.
- Round-trip tujuh field baru: create penuh, lalu update satu field, sisanya
  tidak berubah.
- Bool carry-forward: update `max_turns` saja tidak mematikan `disabled`.
- `POST /api/agent-profiles` pada role locked → 409; body `locked:false` →
  200 dan hanya `Locked` yang berubah.
- `DELETE` pada role locked → 409.

**FE**

- `buildProviderOptions`: instance biasa, instance hilang → `(unavailable)`,
  daftar kosong.
- `AgentProfileEditor`: picker merender, nilai terkemas dipecah benar saat save,
  mode locked hanya menyisakan checkbox Locked dan menyembunyikan Delete.
- `canLeadDelegation("wick/wick::x")` → true.
