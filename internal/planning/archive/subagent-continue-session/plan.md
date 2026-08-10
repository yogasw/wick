# Sub-agent continue: satu session dipakai terus — Implementation Plan

**Goal:** Sub-agent bisa dilanjutkan berkali-kali di session yang sama, sehingga pola main-agent-sebagai-manager mengawasi sub-agent-sebagai-developer bisa jalan tanpa merusak context developer.

**Architecture:** Op `continue` baru di connector `sub-agents` yang memanggil `Service.execute` pada row yang sudah ada (bukan `Run`, yang selalu bikin row+session baru). Ditambah tiga pelonggaran supaya agent selesai tetap terjangkau: mailbox menerima status terminal, roster mempertahankan handle terminal, dan budget ditambah alih-alih diwarisi.

Desain lengkap: [design.md](design.md).

---

## Tahap 1 — Inti continue

- [x] **1.1** `Service.Continue(ctx, ContinueRequest)` di `internal/agents/delegation/run.go`.
      Ambil row, cek `CanInterrupt`, tolak `running`/`queued`, update Task+budget,
      `MarkRunning`, panggil `execute`. Jangan hitung ulang `ChildSessionID`.
- [x] **1.2** Deteksi resumable sebelum spawn. Angkat cek `CLISessionID` dari
      `poolWaker.WakeChild` jadi sesuatu yang `Continue` bisa panggil, supaya
      hasilnya bisa membawa `resumed: true|false`.
- [x] **1.3** Budget: `MaxTurns`/`MaxTokens` **ditambah**, bukan di-set. Default
      penambahan = jatah efektif profile kalau penelepon tidak menyebut angka.
      Tetap di-clamp ke plafon sistem lewat `EffectiveMaxTurns`/`EffectiveMaxTokens`.
- [x] **1.4** Test: continue pada `stopped_max_turns` memakai `ChildSessionID` yang
      sama; continue pada `running` ditolak; budget naik bukan reset.

## Tahap 2 — Permukaan MCP

- [x] **2.1** Op `continue` di `internal/connectors/sub-agents/connector.go` —
      `continueInput` + entri di `connector.Cat("Delegation", ...)`. Deskripsi op
      harus menyebut eksplisit bahwa sub-agent masih ingat kerja sebelumnya,
      jadi `task` diisi langkah BERIKUTNYA, bukan brief awal diulang.
- [x] **2.2** Handler `h.continueDelegation` di `handlers.go`. Pola otoritas
      mengikuti `h.collect` (`resolveCaller` + `CanInterrupt`).
- [x] **2.3** Field `ContinueID` di `delegateInput`, me-rute ke handler yang sama.
      Deskripsi `delegate` cukup **satu kalimat** yang menunjuk ke `continue` —
      jangan menggemukkan deskripsi yang sudah panjang.
- [x] **2.4** Pastikan op baru ikut ter-surface sebagai `wick_agent_continue`
      lewat jalur `internal/mcp/handlers/wickmanager.go` yang sudah ada.
- [x] **2.5** Test: `continue` menolak delegation milik orang lain; `continue_id`
      di `delegate` menghasilkan efek identik dengan op `continue`.

## Tahap 3 — Progress di tengah jalan (PULL)

- [x] **3.1** `CollectResult.Progress` + `LastReport` di `collect.go`. Isi
      `Progress` dari `Runner.PartialText` untuk row yang masih berjalan.
- [x] **3.2** **Jangan `MarkCollected` pada row berjalan.** Guard sekali-serah
      hanya untuk hasil akhir; kalau intip di tengah menandainya, hasil akhir
      hilang. Ini bug termahal di seluruh rencana — tulis testnya lebih dulu.
- [x] **3.3** Ganti note baris pending: mengintip boleh, menunggu tidak.
      Teks sekarang (*"do not block on this"*) menyesatkan pengawas.
- [x] **3.4** Test: collect pada row berjalan memuat progress DAN tidak
      menandainya terkoleksi — collect berikutnya setelah selesai tetap dapat
      hasil penuh.

## Tahap 4 — Progress di tengah jalan (PUSH)

- [x] **4.1** Kolom `LastReport` + `LastReportAt` di `entity.AgentDelegation`.
- [x] **4.2** Op `progress` di connector + handler. Resolusi row lewat
      `FindByChildSession` (pola yang sama dengan `report_result`), jadi
      sub-agent tidak perlu tahu id-nya sendiri.
- [x] **4.3** Laporan membangunkan leader lewat `DeliverToSession`, source
      `subagent` — jalur yang sama dengan hasil async, jadi main tahu ini bukan
      manusia yang bicara. Kegagalan delivery dicatat, jangan bikin op gagal:
      laporannya sudah durable di row.
- [x] **4.4** Param `supervised bool` di `delegate` + kolom row + kalimat
      kriteria (BUKAN jadwal) di `spawnPreamble`. Sertakan konsekuensinya
      ("kamu sedang diawasi"), sama alasannya dengan `interruptNote`.
- [x] **4.5** Test: `progress` menulis ke row DAN membangunkan leader;
      `supervised=false` tidak memunculkan kalimat apa pun di preamble;
      leader yang memanggil `progress` ditolak (ia bukan sub-agent).

## Tahap 5 — Agent selesai tetap terjangkau

- [x] **5.1** `mailbox.go`: terima semua status terminal. Ganti komentar yang
      menjelaskan "antre pada pembaca yang tidak akan datang" — alasan itu tidak
      berlaku lagi. `failed` diberi catatan, bukan ditolak.
- [x] **5.2** `roster.go`: handle terminal disimpan dengan flag `live: false` +
      `status`, tidak dicoret. Presedensi mention diperluas: agent mati tetap
      menang atas role dengan key sama.
- [x] **5.3** `listAgents` di `handlers.go` mengekspos `live` + `status`, supaya
      main agent bisa membedakan "lagi kerja" dari "selesai, bisa dilanjut".
- [x] **5.4** Test: `message` ke agent `stopped_max_turns` diterima; `@developer`
      setelah developer selesai me-resolve ke agent itu, bukan ke role baru.

## Tahap 6 — Verifikasi jalur nyata

- [x] **6.1** `go build ./...` + `go vet` + `go test -count=1 ./internal/...`.
      29 test baru.

      **Satu test lama harus diubah, bukan diperbaiki:**
      `TestNewResolverExcludesTerminalHandlesAndDisabledRoles` menegakkan
      justru perilaku yang dokumen ini membalik — handle terminal dicoret
      dari resolver. Diganti nama jadi `...KeepsFinishedHandles...` dan
      assertion-nya dibalik. Kontraknya memang berubah; kalau test ini
      masih lolos apa adanya, §5 tidak benar-benar terpasang.

      Dua kegagalan lain BUKAN dari perubahan ini, sudah diverifikasi
      terpisah:
      - `template/main.go` gagal build (folder tanpa `go.mod` sendiri) —
        identik di tree bersih, sebelum perubahan apa pun.
      - `TestPipeline_HappyPath_HelloWorld` dan
        `TestScenario_ConcurrentSessionsQueueDrains` (paket `agents`) flaky
        di bawah beban suite penuh; masing-masing 15× dan 10× berturut lolos
        saat dijalankan sendiri. Keduanya menjalankan subprocess sungguhan
        dan tidak menyentuh delegation.

- [x] **6.2–6.4** Ditutup dengan **integration test**, bukan smoke manual:
      [internal/agents/delegation_integration_test.go](../../../agents/delegation_integration_test.go).

      Merangkai pool asli + `PoolRunner` asli + session store asli, berhenti
      hanya di batas proses — `scriptedSpawner` menggantikan biner claude dan
      **merekam `ResumeID` yang diterimanya**. Rekaman itulah buktinya.

      Lima test:
      - `TestContinueSpawnsWithResume` — spawn pertama `resume=""`, spawn
        kedua `resume="cli-abc"`. Ini satu-satunya bukti bahwa provider
        benar-benar dikembalikan ke transcript-nya.
      - `TestContinueRunsInTheSameChildSession` — satu `conversation.jsonl`
        berisi 2 user + 2 assistant turn; folder anak tidak bertambah.
      - `TestContinueAfterTurnCapGetsRealRoom` — kena cap di leg pertama,
        selesai di leg kedua. Kalau budget di-set ulang (bukan ditambah),
        leg kedua mati di turn pertama.
      - `TestContinueWithoutACapturedSessionSaysSo` — provider tanpa
        `session_id` → `resumed:false` + catatan amnesia eksplisit.
      - `TestSupervisedRunCanBeWatchedThenContinued` — alur penuh, termasuk
        hasil lanjutan tetap bisa di-collect setelah collect pertama.

      **Diverifikasi dengan uji mutasi.** `ChildSessionID` sengaja dibuat
      di-mint ulang saat continue (persis bug yang fitur ini cegah) → dua
      test gagal dengan pesan yang tepat (`resume=""`, transcript 1/1 bukan
      2/2). Kode dikembalikan, semua lolos lagi. Test-nya benar-benar
      menahan sesuatu, bukan lolos karena assertion lemah.

      Satu catatan fixture: `rig.settle(t)` menunggu subprocess leg
      sebelumnya benar-benar mati sebelum leg berikutnya jalan. Scripted
      spawner menutup stdout setelah baris terakhir, jadi prosesnya selesai
      tapi pool masih memegangnya sebagai idle sampai TTL; continue di
      jendela itu menulis ke stdin yang stdout-nya tak akan pernah bicara
      lagi. Provider sungguhan tetap menjawab — ini artefak fixture, bukan
      perilaku produksi.

---

## Catatan

- Semua UI copy dalam bahasa Inggris.
- Jangan commit tanpa diminta.
- `graphify update .` setelah file diubah.
