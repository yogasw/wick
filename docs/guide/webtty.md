# Web Terminal

The Web Terminal gives you a browser-based shell session running on the Wick server. No SSH client needed.

<!-- TODO: screenshot of WebTTY page showing terminal with bash session running -->

## Why it exists

Some LLM providers require an interactive login flow — for example:

```bash
claude login     # opens a browser OAuth flow, writes token to ~/.claude/
codex login      # similar OAuth flow
```

Without a terminal, you would need SSH access to the server to run these commands. The Web Terminal lets you do it directly from the Wick admin panel.

**Typical workflow with Provider Storage:**

1. Open **Tools → Web Terminal**.
2. Click **Start**.
3. Run `claude login` (or `codex login`, etc.) and follow the prompts.
4. The provider writes credentials to disk.
5. [Provider Storage](./provider-storage) picks them up on the next sync and backs them up to the database.
6. On the next restart, credentials are restored automatically — no re-login needed.

## Starting a session

1. Go to **Tools → Web Terminal**.
2. Click **Start** — the terminal connects and shows a shell prompt.
3. Use the terminal normally.
4. Click **Stop** when done.

The session runs on the server. Any commands you run execute on the host where Wick is deployed.

## Enabling / disabling

An administrator can toggle the terminal on or off from **Tools → Web Terminal → Settings**:

- **Enabled** — terminal is accessible (default).
- **Disabled** — the page shows a notice instead of the terminal. Use this to restrict access without removing the tool.

> The Web Terminal is tagged **System** — only admin users can access it.
