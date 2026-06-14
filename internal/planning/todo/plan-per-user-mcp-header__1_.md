# Plan: Per-User MCP Header saat Spawn Agent (Claude / Codex) di Wick

Tujuan: tiap kali spawn agent (Claude atau Codex), header MCP-nya beda per-user
(tergantung siapa yang pakai wick), TANPA pernah menulis credential ke file.

---

## 1. Prinsip dasar

- File config TIDAK PERNAH berisi nilai rahasia - cuma placeholder ${VAR}.
  Alasan: file di disk gampang ke-expose (cd + cat, ke-commit git, ke-backup,
  kebaca user lain di OS-account yang sama).
- Rahasia disimpan sebagai token wick_enc_ (enkripsi per-user).
  Alasan: kunci nempel di identitas user, bukan di file. Orang lain baca token
  tetap nggak bisa decrypt.
- Nilai asli cuma hidup di env proses saat agent jalan (memory), bukan di disk.
  Alasan: cd + cat nggak akan nemu apa-apa, karena memang nggak ada file-nya.
- Env di-scope ke proses, BUKAN di-export ke shell.
  Alasan: env per-proses, jadi 2 spawn paralel terisolasi, token A & B nggak ketuker.

Threat yang ditutup: user lain cd ke direktori -> yang kebaca cuma ${USER_TOKEN}
(placeholder) atau wick_enc_... (ciphertext tanpa kunci). Dua-duanya useless.

---

## 2. Arsitektur alur

    trigger user A --+
                     +--> wick decrypt (key A) -> token A -> env proses A -> spawn -> header A
    trigger user B --+
                         wick decrypt (key B) -> token B -> env proses B -> spawn -> header B
    (paralel, no clash - tiap proses punya env block sendiri di memory)

- File config: SATU, sama untuk semua. Isinya cuma ${USER_TOKEN}.
- Yang beda cuma nilai env yang di-inject saat runtime - dan nilai itu nggak pernah disimpan.

---

## 3. Komponen yang dibangun

### 3.1 Penyimpanan credential
- [ ] Tiap user mint credential-nya jadi token wick_enc_ (lewat Wick UI / wick_encrypt).
- [ ] Token disimpan di connector/job config wick - BUKAN di file repo.
- [ ] Pastikan tidak ada plaintext token yang ke-commit (.gitignore, scan repo).

### 3.2 File config MCP (placeholder only)
- [ ] Header MCP pakai ekspansi env, BUKAN nilai literal.
      (Claude Code dan Codex punya schema config beda - logika sama, tempat naruh beda.)
- [ ] Commit file ini aman karena isinya placeholder.

Claude Code (.mcp.json / config MCP):

    {
      "mcpServers": {
        "myserver": {
          "url": "https://api.internal/mcp",
          "headers": {
            "Authorization": "Bearer ${USER_TOKEN}",
            "X-User-Id": "${WICK_USER_ID}"
          }
        }
      }
    }

Codex (config MCP Codex - sesuaikan dengan schema-nya):

    [mcp_servers.myserver]
    url = "https://api.internal/mcp"
    # header di-set lewat env yang diwariskan ke proses (lihat 3.3)

TODO verifikasi: apakah runtime (Claude/Codex) yang kamu pakai mendukung ekspansi
${VAR} di field header. Kalau tidak, header harus di-inject lewat proxy/wrapper
yang baca env dan nambah header ke request MCP.

### 3.3 Spawner (orchestrator) - resolve & inject per-proses
- [ ] Saat spawn: resolve wick_enc_ -> token sesuai user aktif -> masukkan ke env object proses anak.
- [ ] JANGAN export ke shell. JANGAN rangkai string shell dengan token literal.
- [ ] Resolve di dalam scope tiap spawn (jangan reuse satu variabel global untuk loop).

Node:

    async function spawnAgent({ cmd, args, userId }) {
      const token = await resolveWickToken(userId); // decrypt per-user, di scope ini
      return spawn(cmd, args, {
        env: { ...process.env, USER_TOKEN: token, WICK_USER_ID: userId },
      });
      // token cuma ada di env block proses anak ini - proses lain nggak lihat
    }

Python:

    def spawn_agent(cmd, args, user_id):
        token = resolve_wick_token(user_id)  # decrypt per-user, di scope ini
        return subprocess.Popen(
            [cmd, *args],
            env={**os.environ, "USER_TOKEN": token, "WICK_USER_ID": user_id},
        )

---

## 4. Aturan keamanan (jangan dilanggar)

- [ ] TIDAK ADA token plaintext di file mana pun (config, .env yang ke-commit, log).
- [ ] TIDAK ADA "export TOKEN=..." global di shell - selalu inline / env object per-proses.
- [ ] TIDAK nge-print token ke stdout / log / chat.
- [ ] Resolve token per-spawn di scope lokal - bukan variabel global yang dishare 2 proses.
- [ ] Cek /proc/<pid>/environ aman: cuma owner + root yang bisa baca (default OK).
- [ ] Token literal jangan muncul di ps / shell history -> pakai spawn programatik
      (env object), bukan "TOKEN=xxx cmd".

---

## 5. Kenapa aman untuk 2 proses bareng

- Env block tiap proses terpisah di memory - A nggak bisa baca env-nya B.
- /proc/<pid>/environ hanya kebaca oleh owner proses + root.
- Selama token di-resolve di scope tiap spawn (bukan variabel bersama), nggak ada jalan ketuker.

---

## 6. Verifikasi / test

- [ ] Spawn 2 agent berbeda user paralel, cek masing-masing pakai header sesuai user-nya.
- [ ] cat file config -> pastikan cuma keluar ${USER_TOKEN}, bukan rahasia.
- [ ] grep -r "wick_enc_|Bearer " <repo> -> pastikan nggak ada plaintext credential.
- [ ] Kill proses -> pastikan nggak ada file temp berisi token yang ketinggalan.
- [ ] User B coba baca token user A -> harus gagal decrypt (kunci per-user).

---

## 7. Open questions (perlu diisi)

1. Spawn agent lewat wick workflow/job atau script sendiri? (nentuin di mana 3.3 ditaruh)
2. Runtime spawner: Node atau Python?
3. Header isinya credential (token/API key) atau cuma identifier (user id/tenant)?
4. Runtime agent (Claude/Codex) mendukung ekspansi ${VAR} di header, atau butuh wrapper/proxy?
