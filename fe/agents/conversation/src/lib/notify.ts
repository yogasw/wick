export interface NotifyDeps {
  hasFocus: () => boolean;
  beep: () => void;
  showNotification: (title: string, body: string) => void;
}

let audioCtx: AudioContext | null = null;

function realBeep(): void {
  try {
    const AC = (window as typeof window & { webkitAudioContext?: typeof AudioContext }).AudioContext
      || (window as typeof window & { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
    if (!AC) return;
    if (!audioCtx) audioCtx = new AC();
    if (audioCtx.state === "suspended") audioCtx.resume();
    const now = audioCtx.currentTime;
    [880, 1175].forEach((freq, i) => {
      const osc = audioCtx!.createOscillator();
      const gain = audioCtx!.createGain();
      osc.type = "sine";
      osc.frequency.value = freq;
      const t = now + i * 0.16;
      gain.gain.setValueAtTime(0.0001, t);
      gain.gain.exponentialRampToValueAtTime(0.18, t + 0.02);
      gain.gain.exponentialRampToValueAtTime(0.0001, t + 0.14);
      osc.connect(gain);
      gain.connect(audioCtx!.destination);
      osc.start(t);
      osc.stop(t + 0.15);
    });
  } catch (_) { /* audio blocked — notification still fires */ }
}

function realShowNotification(title: string, body: string): void {
  if (typeof Notification === "undefined" || Notification.permission !== "granted") return;
  try {
    const n = new Notification(title, { body, tag: "wick-ask", renotify: true } as NotificationOptions);
    n.onclick = () => { window.focus(); n.close(); };
  } catch (_) { /* construction can throw on some mobile browsers */ }
}

function realHasFocus(): boolean {
  return document.hasFocus() && document.visibilityState === "visible";
}

export function notify(title: string, body?: string, deps?: Partial<NotifyDeps>): void {
  const hasFocus = deps?.hasFocus ?? realHasFocus;
  const beep = deps?.beep ?? realBeep;
  const showNotification = deps?.showNotification ?? realShowNotification;

  if (hasFocus()) return;

  const resolvedTitle = title || "Agent needs your input";
  const resolvedBody = body ?? "";

  beep();
  showNotification(resolvedTitle, resolvedBody);
}

if (typeof document !== "undefined") {
  const once = () => {
    document.removeEventListener("click", once);
    document.removeEventListener("keydown", once);
    if (typeof Notification !== "undefined" && Notification.permission === "default") {
      try { Notification.requestPermission(); } catch (_) { /* older API */ }
    }
    try {
      const AC = (window as typeof window & { webkitAudioContext?: typeof AudioContext }).AudioContext
        || (window as typeof window & { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
      if (AC) {
        if (!audioCtx) audioCtx = new AC();
        if (audioCtx.state === "suspended") audioCtx.resume();
      }
    } catch (_) { /* audio unavailable */ }
  };
  document.addEventListener("click", once);
  document.addEventListener("keydown", once);
}
