## 21. Replay

### Inline replay (in-editor debug)

Tombol **↻ Replay in editor** di tiap row Runs panel — fetch state +
events lewat `/runs/<id>/state`, paint node badge di canvas, populate
Logs tab dengan tiap event, plus cache outputs:

- Trigger node → cache full `state.event` jadi "output" trigger
- Setiap node `node_completed` → cache `data.output`

Hasilnya: setelah Replay, klik node manapun → INPUT pane nampilin
parent output, OUTPUT pane nampilin output node tsb (dari run history).
Tidak fire run baru — purely reload state. Workflow ngak berubah.

Plus: tombol **Export JSON** di tiap row → copy full state+events ke
clipboard buat paste ke bug report / chat.

### Re-run via manual trigger

Untuk fire run baru dengan event yang sama (test fix, audit, regression
check): pakai `?prefill=<runID>` link ke manual runner — UI replay nav,
not auto-execute (memory `replay-navigate-not-autoexecute`). Replay
skip dedup (event_id stamp baru `replay-<uuid>`), tetep lewat whitelist +
queue + guard.

### Per-node replay (debug, future)

Klik node di run timeline → "Re-run from here" → bikin run baru yg state
di-restore sampai node tersebut, lalu lanjut dari node tsb. Berguna pas
debug "node X kasih output beda dari ekspektasi". **Belum diimplementasi.**

### Resume vs Replay

- **Resume** — workflow paused atau crash. Lanjut dari state terakhir.
  Run ID sama.
- **Inline Replay** — workflow selesai (success/fail). State reload ke
  editor, tidak fire run baru. Run ID + state tetap.
- **Manual Replay** — fire run baru dgn event yg sama. Run ID baru.

---

