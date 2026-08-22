/* Background auto-save for the project settings form.

   Two rules shape this file, both of them anti-flicker:

   1. One request at a time. Edits made while a save is open are collapsed
      into a SINGLE follow-up request instead of queueing one per edit, so
      holding a key down cannot stack requests.
   2. The caller decides when "saving" becomes visible. This module reports
      state transitions; SaveStatus.svelte delays showing them, so a save
      that completes quickly never paints a spinner at all. */

export type SaveState = "idle" | "saving" | "saved" | "error";

export type SaveStatus = {
  state: SaveState;
  /** Epoch ms of the last successful save; undefined until one lands. */
  savedAt?: number;
  /** Error text, set only while state is "error". */
  message?: string;
};

export type AutosaveOptions = {
  /** Persists the current form values. Rejecting puts us in "error". */
  save: () => Promise<void>;
  debounceMs?: number;
  onStatus?: (s: SaveStatus) => void;
  /** Start suspended — used while the form is still loading its data, so
      seeding the fields does not immediately save them back. */
  suspended?: boolean;
};

export type Autosave = {
  /** Text input changed: save after the debounce window. */
  schedule: () => void;
  /** Committing interaction (blur, select, toggle): save now. */
  flush: () => void;
  /** Re-run the save that failed. No-op when the last save succeeded. */
  retry: () => void;
  status: () => SaveStatus;
  setSuspended: (v: boolean) => void;
  /** Drop any pending timer. Call on unmount. */
  dispose: () => void;
};

export function createAutosave(opts: AutosaveOptions): Autosave {
  const debounceMs = opts.debounceMs ?? 800;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let inFlight = false;
  // Set when an edit lands (debounced or flushed) and cleared the moment a
  // request picks it up. Also the retry marker: a failed save leaves it set.
  let dirty = false;
  let suspended = opts.suspended === true;
  let status: SaveStatus = { state: "idle" };

  function emit(next: SaveStatus) {
    status = next;
    opts.onStatus?.(next);
  }

  async function run() {
    if (inFlight || !dirty || suspended) return;
    dirty = false;
    inFlight = true;
    emit({ state: "saving", savedAt: status.savedAt });
    try {
      await opts.save();
      emit({ state: "saved", savedAt: Date.now() });
    } catch (e) {
      // Keep the edit marked dirty so retry() has something to send.
      dirty = true;
      emit({
        state: "error",
        savedAt: status.savedAt,
        message: e instanceof Error ? e.message : String(e),
      });
    } finally {
      inFlight = false;
      // Edits that arrived during the request: exactly one follow-up.
      // Skipped after a failure so a broken save does not spin forever —
      // retry() is the way back in.
      if (dirty && status.state !== "error") void run();
    }
  }

  function clearTimer() {
    if (timer !== undefined) {
      clearTimeout(timer);
      timer = undefined;
    }
  }

  return {
    schedule() {
      if (suspended) return;
      dirty = true;
      clearTimer();
      timer = setTimeout(() => {
        timer = undefined;
        void run();
      }, debounceMs);
    },
    /* Flush only sends when something is actually pending: a blur with no
       edit behind it must not fire a request, which is what would make
       tabbing through an untouched form save on every field. */
    flush() {
      if (suspended || (!dirty && timer === undefined)) return;
      dirty = true;
      clearTimer();
      void run();
    },
    retry() {
      if (status.state !== "error") return;
      // Clear the error first so run()'s follow-up guard does not block it.
      emit({ state: "idle", savedAt: status.savedAt });
      dirty = true;
      void run();
    },
    status() {
      return status;
    },
    setSuspended(v: boolean) {
      suspended = v;
      if (v) clearTimer();
    },
    dispose() {
      clearTimer();
    },
  };
}
