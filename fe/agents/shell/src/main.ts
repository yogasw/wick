/*
 * agents-shell island — sidebar DOM enhancer.
 *
 * Attaches interactive behaviors to the AgentsLayout sidebar that is
 * server-rendered by layout.templ. Loaded by every agents page via the
 * <script type="module"> tag emitted by AgentsLayout.
 *
 * Behaviors ported from agents.js:
 *   - Pin project as personal default  (POST /projects/{id}/pin)
 *   - Drag session → project to move   (POST /sessions/{id}/project)
 *
 * The mobile drawer open/close and backdrop are already wired by an
 * inline <script> in layout.templ and are NOT duplicated here.
 */

function resolveBase(): string {
  const el = document.querySelector<HTMLElement>("[data-base]");
  if (el?.dataset["base"]) {
    return el.dataset["base"];
  }
  const session = document.querySelector<HTMLElement>("[data-session-id]");
  if (session?.dataset["base"]) {
    return session.dataset["base"];
  }
  return "";
}

function wirePin(): void {
  document.querySelectorAll<HTMLButtonElement>("[data-pin-project]").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const id = btn.dataset["pinProject"];
      const base = resolveBase();
      if (!id || !base) {
        return;
      }
      btn.disabled = true;
      fetch(`${base}/projects/${encodeURIComponent(id)}/pin`, { method: "POST" })
        .then((r) => r.json())
        .then(() => {
          location.reload();
        })
        .catch(() => {
          btn.disabled = false;
        });
    });
  });
}

function wireDragToMove(): void {
  let dragSid: string | null = null;
  const dropHi = "ring-2 ring-green-400 ring-inset";
  const dropClasses = dropHi.split(" ");

  document.querySelectorAll<HTMLElement>("[data-session-drag]").forEach((row) => {
    row.addEventListener("dragstart", (e) => {
      dragSid = row.dataset["sessionDrag"] ?? null;
      if (e.dataTransfer) {
        e.dataTransfer.setData("text/plain", dragSid ?? "");
        e.dataTransfer.effectAllowed = "move";
      }
    });
    row.addEventListener("dragend", () => {
      dragSid = null;
    });
  });

  document.querySelectorAll<HTMLElement>("[data-project-drop]").forEach((target) => {
    target.addEventListener("dragover", (e) => {
      e.preventDefault();
      if (e.dataTransfer) {
        e.dataTransfer.dropEffect = "move";
      }
      dropClasses.forEach((c) => target.classList.add(c));
    });
    target.addEventListener("dragleave", () => {
      dropClasses.forEach((c) => target.classList.remove(c));
    });
    target.addEventListener("drop", (e) => {
      e.preventDefault();
      dropClasses.forEach((c) => target.classList.remove(c));
      const sid = (e.dataTransfer?.getData("text/plain")) || dragSid;
      const pid = target.dataset["projectDrop"];
      const base = resolveBase();
      if (!sid || !base) {
        return;
      }
      fetch(`${base}/sessions/${encodeURIComponent(sid)}/project`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ project_id: pid }),
      })
        .then(() => {
          location.reload();
        })
        .catch(() => {});
    });
  });
}

document.addEventListener("DOMContentLoaded", () => {
  wirePin();
  wireDragToMove();
});
