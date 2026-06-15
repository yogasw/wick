/* Lazy emoji-mart loader. Kept in its own module so IconPicker.svelte can
   pull it in via a dynamic import() — Vite splits it (plus the runtime
   fetch of the ~400KB vendored data) into a separate chunk that only loads
   the first time a picker panel opens, never as part of the main bundle.

   The emoji-mart library is vendored (not an npm dep): it ships as a global
   `window.EmojiMart` from /modules/manager/js/vendor_emoji_mart.js, and its
   data is fetched from the sibling JSON. Both are loaded once, lazily, and
   memoised across every picker on the page. */

const VENDOR_SCRIPT = "/modules/manager/js/vendor_emoji_mart.js";
const VENDOR_DATA = "/modules/manager/js/vendor_emoji_mart_data.json";

interface EmojiSelectEvent {
  native: string;
}

interface EmojiMartGlobal {
  Picker: new (opts: Record<string, unknown>) => HTMLElement;
}

let scriptPromise: Promise<EmojiMartGlobal> | null = null;
let dataPromise: Promise<unknown> | null = null;

function loadScript(): Promise<EmojiMartGlobal> {
  if (scriptPromise) return scriptPromise;
  scriptPromise = new Promise<EmojiMartGlobal>((resolve, reject) => {
    const existing = (window as unknown as { EmojiMart?: EmojiMartGlobal }).EmojiMart;
    if (existing) {
      resolve(existing);
      return;
    }
    const el = document.createElement("script");
    el.src = VENDOR_SCRIPT;
    el.async = true;
    el.onload = () => {
      const mart = (window as unknown as { EmojiMart?: EmojiMartGlobal }).EmojiMart;
      if (mart) {
        resolve(mart);
      } else {
        reject(new Error("emoji-mart vendor script loaded without exposing EmojiMart"));
      }
    };
    el.onerror = () => reject(new Error("failed to load emoji-mart vendor script"));
    document.head.appendChild(el);
  });
  return scriptPromise;
}

function loadData(): Promise<unknown> {
  if (!dataPromise) {
    dataPromise = fetch(VENDOR_DATA).then((r) => r.json());
  }
  return dataPromise;
}

/* mountEmojiPicker loads the lib + data (lazily, once) and mounts a picker
   into `target`, wiring onSelect to the picked emoji's native glyph and the
   theme to the current document state. Returns the created element so the
   caller can remove it on teardown. */
export async function mountEmojiPicker(
  target: HTMLElement,
  onSelect: (native: string) => void,
): Promise<HTMLElement> {
  const [mart, data] = await Promise.all([loadScript(), loadData()]);
  const picker = new mart.Picker({
    data,
    onEmojiSelect: (e: EmojiSelectEvent) => onSelect(e.native),
    theme: document.documentElement.classList.contains("dark") ? "dark" : "light",
    previewPosition: "none",
    skinTonePosition: "none",
    perLine: 9,
    maxFrequentRows: 1,
  });
  target.appendChild(picker);
  return picker;
}
