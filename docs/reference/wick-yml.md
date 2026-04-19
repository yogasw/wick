---
outline: deep
---

# wick.yml Reference

`wick.yml` is the task configuration file for the built-in cross-platform task runner. Place it at the root of your project.

---

## Structure

```yaml
vars:
  KEY: value

tasks:
  task-name:
    cmds:
      - command
    done: |
      Message shown when task completes successfully.
```

---

## vars

Define variables to reuse across tasks. Supports plain strings and OS/arch detection via `$arch`.

### Plain string

```yaml
vars:
  TAILWIND_VERSION: 3.4.17
```

### `$arch` — OS/arch detection

Resolves based on `GOOS/GOARCH`. Tries exact match first (`darwin/arm64`), then OS-only fallback (`darwin`), then `default`.

::: raw
```yaml
vars:
  TAILWIND_OS:
    $arch:
      windows: windows-x64.exe
      darwin/arm64: macos-arm64
      darwin: macos-x64
      linux/arm64: linux-arm64
      linux: linux-x64
```
:::

Use variables in commands with <code v-pre>{{.VAR_NAME}}</code>:

::: raw
```yaml
- "{{.TAILWIND_BIN}} -i input.css -o output.css"
```
:::

---

## tasks

Each task has a list of `cmds` to run sequentially.

### Plain command

```yaml
cmds:
  - go mod tidy
  - templ generate ./...
```

### Built-in: `mkdir -p`

Cross-platform directory creation.

```yaml
- mkdir -p bin static
```

### Built-in: `chmod +x`

No-op on Windows.

```yaml
- chmod +x bin/mytool
```

### `if_missing` — conditional execution

Skip a command if a file or binary already exists.

**Check file:**

::: raw
```yaml
- if_missing:
    file: "{{.TAILWIND_BIN}}"
  download:
    dest: "{{.TAILWIND_BIN}}"
    url: "https://example.com/tool-{{.TAILWIND_OS}}"
    cache: true
```
:::

**Check binary in PATH:**

```yaml
- if_missing:
    cmd: templ
  run: go install github.com/a-h/templ/cmd/templ@latest
```

### `download`

Downloads a file. Typically used inside `if_missing`.

| Field | Description |
|-------|-------------|
| `dest` | Destination path |
| `url`  | Download URL (supports variable interpolation) |
| `cache` | `true` (default) — cache in OS cache dir. `false` — always re-download. |

Cache location:
- macOS/Linux: `~/.cache/wick/`
- Windows: `%LOCALAPPDATA%\wick\`

### Background process: `bg`

Run a command in the background (cross-platform, no `&` needed).

::: raw
```yaml
- bg: true
  run: "{{.TAILWIND_BIN}} -i input.css -o output.css --watch"
```
:::

### `done` — completion message

Printed after all cmds complete successfully.

```yaml
tasks:
  setup:
    cmds:
      - ...
    done: |
      Setup complete!
      Run: wick dev
```

---

## Full Example

::: raw
```yaml
vars:
  TAILWIND_OS:
    $arch:
      windows: windows-x64.exe
      darwin/arm64: macos-arm64
      darwin: macos-x64
      linux/arm64: linux-arm64
      linux: linux-x64
  TAILWIND_VERSION: 3.4.17
  TAILWIND_BIN:
    $arch:
      windows: bin/tailwindcss.exe
      default: bin/tailwindcss

tasks:
  setup:
    cmds:
      - mkdir -p bin static
      - if_missing:
          file: "{{.TAILWIND_BIN}}"
        download:
          dest: "{{.TAILWIND_BIN}}"
          url: "https://github.com/tailwindlabs/tailwindcss/releases/download/v{{.TAILWIND_VERSION}}/tailwindcss-{{.TAILWIND_OS}}"
          cache: true
      - if_missing:
          cmd: templ
        run: go install github.com/a-h/templ/cmd/templ@latest
      - go mod tidy
    done: |
      Setup complete!
      Run: wick dev

  dev:
    cmds:
      - templ generate ./...
      - "{{.TAILWIND_BIN}} -i web/input.css -o static/output.css"
      - go run . server

  build:
    cmds:
      - templ generate ./...
      - "{{.TAILWIND_BIN}} -i web/input.css -o static/output.css --minify"
      - go build -o bin/app .

  test:
    cmds:
      - go test ./... -race

  tidy:
    cmds:
      - go fmt ./...
      - go mod tidy -v
```
:::
