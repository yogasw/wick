# Host Access

Optional, single-instance connector for local host maintenance and development.
It is not registered in Wick core. Install it separately and keep the System
tag restricted to trusted administrators.

## Security model

The saved `policy_json` is the only policy source. Missing or invalid policy
fails closed. Explicit deny wins over approval and allow. Approval is a
blocking verdict, not a grant provided by an MCP argument: only a trusted
administrator can change the policy in instance settings. Do not enable
"Allow others to configure" for this connector.

Native file operations use descriptor-relative, no-follow operations on
Linux/Termux. Symlinks, hardlinked files, special files, traversal, and
recursive deletion are refused. Directory traversal needs search permission on
ancestors, while listing still needs read permission on the target directory.
This is not an OS sandbox against other
processes with the same host UID; protect the Wick installation, config,
database, plugin binaries and credentials with appropriately narrow rules.
External concurrent changes can still race optimistic edits.

Terminal execution uses Wick's `pkg/safeexec`, which avoids Android's
faccessat2 incompatibility. **safeexec is not a security sandbox.** A trusted
program may invoke other programs or read files outside native file rules.
Terminal operations are disabled by default, and require explicit
`trust_host: true` and separate operation enablement. Do not grant terminal
access to an untrusted agent. Shell mode additionally requires unrestricted
command mode, no command exclusions and `allow_shell: true`. Environment
variables are filtered; files and subprocesses are not isolated.

The native filesystem backend is currently Linux/Termux only. Other OSes
fail closed. Background process management and real OS sandboxing are not
part of this initial release. Do not confuse a command filter with either.

## Example policy

```json
{
  "version": 1,
  "filesystem": {
    "default": "deny",
    "rules": [
      {
        "path": "~/projects",
        "recursive": true,
        "allow": ["list", "read", "stat", "search", "create", "write", "edit", "mkdir"],
        "approval": ["delete", "move"]
      }
    ],
    "protected_patterns": ["**/.env", "**/*.pem", "**/*.key"]
  },
  "terminal": { "mode": "disabled" },
  "system": { "allow": ["system_info"] }
}
```

The manager's custom Policy Manager offers Overview and Raw JSON views,
formatting, validation, a default-deny draft, and reload from storage. Only
Validate & Apply writes the hidden policy_json setting through Wick's normal
config UI. Preview and formatting do not change permissions.
`policy_get`, `policy_validate`, and `policy_check` are non-mutating. Native
mutations and terminal operations are destructive-marked so they start
disabled; enable only the necessary operations. There is deliberately no
MCP policy-write operation.

## Development

From repository root:

```sh
gofmt -w plugins/connector/host_access
go test ./plugins/connector/host_access/...
```

Build and install separately from the core binary:

```sh
cd plugins
wick plugin build --kind connector host_access --target linux/arm64
wick-agent plugin install bin/host_access-0.1.2-linux-arm64.zip
```

The test suite exercises policy precedence, invalid JSON, path traversal,
symlink/hardlink protection, optimistic edits, bounded search and permission
defaults. On Android, the hardlink fixture test is skipped only if the OS
itself refuses to create a hardlink; other platforms exercise the plugin's
hardlink rejection directly. Wick's connector execution history provides the usual run audit;
do not put secrets in command arguments or file contents sent to an agent.
