// What the send_mode setting actually does for one provider type, so the
// Detail page never offers a mode that is silently turned into another.
// Mirrors the backend: pool/factory.go sendModeFor (codex/omp/opencode are
// one-shot: "append" runs as queue) and provider.Injector (omp/opencode in
// server mode take a mid-turn message into the running turn).

const ONE_SHOT = new Set(["codex", "omp", "opencode"]);
const SERVER_TYPES = new Set(["omp", "opencode"]);

export type SendModeNote = {
  /** "ok" = the mode runs as chosen; "changed" = it runs as another mode. */
  level: "ok" | "changed";
  text: string;
};

/**
 * sendModeNote describes how `mode` (the send_mode value: default, append,
 * queue, spawn) behaves for provider `type`. serverOn is the instance's
 * server_mode switch (only meaningful for omp/opencode).
 */
export function sendModeNote(type: string, mode: string, serverOn: boolean): SendModeNote {
  const m = mode || "default";
  if (m === "spawn") {
    return { level: "ok", text: "Each message runs in its own process and session, in parallel — no shared history." };
  }
  if (!ONE_SHOT.has(type)) {
    return { level: "ok", text: "Append: mid-turn messages go straight into the running CLI process." };
  }
  if (SERVER_TYPES.has(type) && serverOn) {
    const verb = type === "omp" ? "steered into" : "added to";
    return {
      level: "ok",
      text: `Append supported (server mode): a message sent mid-turn is ${verb} the running turn.` +
        (m === "append" ? "" : " Queue behaves the same while server mode is on."),
    };
  }
  const why = SERVER_TYPES.has(type) ? "server mode is off (one process per turn)" : `${type} reads the prompt once per turn`;
  if (m === "append") {
    return { level: "changed", text: `Append not supported — ${why}: runs as queue — mid-turn messages wait and run together as one turn.` };
  }
  return { level: "ok", text: `Queue + combine: ${why}, so mid-turn messages wait and run together as one turn.` };
}
