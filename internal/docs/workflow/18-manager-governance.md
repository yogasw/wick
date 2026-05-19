## 18. Manager & governance

### Folder ↔ state split

| Sisi | Tinggal di | Alasan |
|---|---|---|
| **Definisi** (graph, triggers, nodes, scripts, prompts) | File `<BaseDir>/workflows/<id>/` | Gitops, manual-edit |
| **Governance** (approved, approved_by, last_guard_result) | DB `workflow_state` atau file `<id>/.governance.json` | Tamper-resistant, audit-able |
| **Run history** (state.json, events.jsonl) | File `<id>/runs/<run-id>/` | Append-only, large, off-DB |

Pilihan **DB vs file** untuk governance: DB lebih atomic + queryable
(filter "unapproved workflows" cepat). File lebih simple + gitops.
Recommend **DB** karena query freq tinggi (list page selalu join state).

### Tabel `workflow_state` (DB)

| Kolom | Tipe | Catatan |
|---|---|---|
| `id` | UUID PK | = workflow.id |
| `workflow_id` | TEXT | folder name = workflow id |
| `approved_version` | INT NULL | last approved |
| `approved_hash` | TEXT NULL | snapshot hash |
| `approved_by` | TEXT | user id |
| `approved_at` | TIMESTAMPTZ | |
| `last_guard_at` | TIMESTAMPTZ | |
| `last_guard_result` | JSONB | cached |
| `override_reason` | TEXT NULL | force-approve reason |
| `created_at`, `updated_at` | TIMESTAMPTZ | |

### Approval flow + stale detection

3 state mirip routine doc:

| State | Kondisi | Action |
|---|---|---|
| **Fresh approved** | `approved_version == yaml.version && approved_hash == current_hash` | Jalan normal |
| **Edited (same version)** | version sama, hash beda | Auto guard verdict "cosmetic"|"material". Cosmetic → auto-extend; material → stale |
| **Stale (version bumped)** | yaml.version > approved_version | Selalu butuh user re-approve |

### Identitas

- Folder rename: `id` di YAML tetep → manager update `workflow_id`, approval
  nempel.
- File ilang `id`: treat sebagai workflow baru, approval reset.
- Duplicate `id` (copy folder): manager refuse load yang kedua.

---

