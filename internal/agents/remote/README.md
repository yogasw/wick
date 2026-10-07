# remote — agents whose brain lives outside wick

A remote agent is a Team agent with no local CLI: each turn goes to
something else (an A2A server, a person or bot in Slack) and the reply
comes back as if a local provider had written it. Chat, @mention,
schedules, the Slack bridge and the A2A server never learn the agent is
remote.

```
pool ──Spawn──▶ remote.Spawner ──▶ runner (fake process, stream-json out)
                                     │ Send(turn) → Handle
                                     │ Receive (push) / Fetch (pull)
                                     │ Done: terminal event, idle, or Max
                                     ▼
                               Source (adapter)
                     a2aremote.Source    slackremote.Source
```

## Pieces

- **Source** (`source.go`) — one adapter per kind (`a2a`, `slack`), built
  on three primitives:
  - **Send** delivers the turn and returns a `Handle` (task id, thread_ts)
    the reply is matched by.
  - **Receive** — two ways, an adapter offers one or both and lists them
    in `Listen()`, preferred first:
    - *push* (`Pusher.Receive`): a channel of `Event`s, closed when the
      source has no more to say. `ErrPushUnavailable` falls back to pull.
    - *pull* (`Puller.Fetch`): events since `Handle.Cursor`; the runner
      backs off along `PullSteps` (1→2→5→10 s).
  - **Done** — a terminal event (`done` / `error`), or the runner ends the
    turn: `Limits.Idle` after text then silence (`NoteNoMarker`), or
    `Limits.Max` as a timeout. `End(h)` is called once after every turn.
- **Runner** (`runner.go`) — `Spawner.Spawn` returns a `provider.Process`
  with no OS process. Stdin takes stream-json user envelopes; stdout
  writes claude-shaped lines (`assistant`, `result`, `system
  remote_status`), so the claude parser, store and SSE read it unchanged.
- **Shared listener + router** — push for Slack does not open a socket per
  agent. The Slack channel wick already runs hands every message event to
  `slackremote.Shared.Dispatch` (only while a turn waits), and the router
  sends it to the trackers waiting in that thread or DM. Pull
  (`conversations.replies`) covers what the app cannot see.
- **Hops** (`hops.go`) — an A2A remote agent sends `wick_hops` (one more
  than the request that started the turn) in its message metadata; the
  A2A server refuses a request past `MaxHops`, so a remote pointing back
  at wick cannot loop. Slack has no such field: a Slack remote must not
  target a conversation wick itself answers.

## Event schema v1

`SchemaVersion = 1`. Every adapter registers with `Schema:
remote.SchemaVersion`; `Register` panics on any other value.

| kind         | fields                    | meaning                                            |
|--------------|---------------------------|----------------------------------------------------|
| `text_delta` | `text`                    | append to the reply                                |
| `text`       | `text`                    | the reply so far in full; runner shows the new tail |
| `status`     | `status`, `detail`        | `thinking` / `working` / `input_required`, short label |
| `attachment` | `name`, `url`, `mime`     | a file the remote sent                             |
| `done`       | `text`?, `note`?          | ends the turn; `text` = whole reply if set         |
| `error`      | `text`                    | ends the turn as failed                            |

Adding an optional field keeps v1. Renaming/removing a field or changing
what a kind means is v2: bump `SchemaVersion` and move every adapter.

## Adding an adapter

1. New package under `internal/agents/remote/<kind>remote/` (or next to
   the protocol's client, as `a2aremote` is).
2. Register the kind in `init`:
   ```go
   func init() {
       remote.Register(remote.Adapter{
           Kind: "foo", Label: "Foo", Schema: remote.SchemaVersion,
           Listen: []remote.ListenMode{remote.ListenPush, remote.ListenPull},
       })
   }
   ```
3. Implement `Source` plus `Pusher` and/or `Puller`:
   ```go
   type Source struct{ cfg Config }

   func (s *Source) Kind() string                { return "foo" }
   func (s *Source) Label() string               { return "foo-remote (" + s.cfg.Host + ")" }
   func (s *Source) Listen() []remote.ListenMode { return []remote.ListenMode{remote.ListenPull} }
   func (s *Source) Limits() remote.Limits       { return remote.Limits{Max: 10 * time.Minute, Idle: 30 * time.Second} }

   func (s *Source) Send(ctx context.Context, t remote.Turn) (remote.Handle, error) {
       id, err := s.post(ctx, t.Text) // keep per-session state under t.SessionDir
       return remote.Handle{ID: id}, err
   }
   func (s *Source) Fetch(ctx context.Context, h remote.Handle) ([]remote.Event, remote.Handle, error) {
       msgs, cursor, err := s.since(ctx, h.ID, h.Cursor)
       // map msgs to remote.Event; end with EventDone / EventError
       h.Cursor = cursor
       return evs, h, err
   }
   func (s *Source) Cancel(context.Context, remote.Handle) error { return nil }
   func (s *Source) End(remote.Handle)                           {}
   func (s *Source) Describe(context.Context) (remote.Description, error) { … }
   func (s *Source) Test(context.Context) remote.TestResult              { … }
   ```
   Optional: `Resumer` to name the chat session from saved state.
4. Wire it into `RemoteSpawnerFor` and `remoteProviderKey`
   (`internal/tools/agents`) so a session bound to such an agent spawns
   `remote.Spawner{Source: …}`, plus the API/wizard that stores its config.
5. Tests: drive `remote.Spawner` with a fake backend and read stdout, as
   `slackremote/source_test.go` does; the A2A exposure path is covered by
   `a2aserver/remote_e2e_test.go`.

## Phase 3 (not built)

A generic templated HTTP adapter (URL/body/response path configured, no
code), a wick-specific HTTP protocol, and a `remote_source` plugin kind so
an adapter can ship as a plugin binary. All three slot in as a `Source`
behind the same runner and event schema.
