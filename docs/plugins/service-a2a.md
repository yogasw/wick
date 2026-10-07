# Walkthrough: an A2A repeater service

Goal: a client's chatbot only has its own HTTP API. Put it behind the A2A
protocol with a service plugin, then add it to wick as a remote agent —
without changing wick.

The working reference is `plugins/service/example_a2a_repeater` in the wick
repo (its "bot" is simulated in-process; a real one calls the client's API).

## 1. Read the bot API

Note three things from the client's documentation:

- how to open a conversation (and the id it returns),
- how to send a user message and get the reply (sync, poll, or stream),
- the credentials it needs.

## 2. Write the adapter

```
service/a2a_botx/
├── main.go     service.ServeService(Module())
└── VERSION     0.1.0
```

- `Configs`: base URL + API key (secret → encrypted by wick, edited on the
  plugin's admin page).
- `Routes`:
  - `/.well-known/agent.json` → `service.Public` (the agent card),
  - `/` → `service.Token` for JSON-RPC (`message/send`, `message/stream`).
  (The example registers a single `/` route as `service.Public` to keep the
  demo short; use `service.Token` for the JSON-RPC route in production.)
- `Register`: serve the agent card and the JSON-RPC endpoint. Keep a map A2A
  `contextId` → bot conversation id; create the conversation on the first
  message of a context.
- `message/stream` answers with SSE; the host flushes each event, so stream
  as the bot replies.
- Optional: implement `RemoteSource` to offer the same bot directly as a Team
  remote source.

## 3. Build and install

```bash
wick plugin build --kind service a2a_botx --target linux/amd64
<app> plugin install bin/a2a_botx-0.1.0-linux-amd64.zip
```

Then on **Manager → Services → a2a_botx**: fill the config, generate the
access token, and check the status is *running*.

## 4. Check it

```bash
curl https://<wick>/x/a2a_botx/.well-known/agent.json
curl -H "Authorization: Bearer <token>" -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"message/send","params":{"message":{"role":"user","parts":[{"kind":"text","text":"hi"}],"messageId":"m1"}}}' \
  https://<wick>/x/a2a_botx/
```

Any A2A client (for example `a2a-go`) can now talk to it.

## 5. Use it from Team

Either add an **A2A remote agent** pointing at
`https://<wick>/x/a2a_botx/.well-known/agent.json` with the token, or — when
the plugin implements `RemoteSource` — pick source **Plugin** in the Remote
agent wizard. The agent can be exposed again through the A2A/REST connection
like any other agent.
