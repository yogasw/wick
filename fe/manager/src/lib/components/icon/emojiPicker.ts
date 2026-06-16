/* Lazy emoji-mart loader. Kept in its own module so IconPicker.svelte can
   pull it in via a dynamic import() — Vite splits emoji-mart + its data set
   into a separate chunk that only loads the first time a picker panel opens,
   never as part of the main bundle.

   emoji-mart + @emoji-mart/data are real npm dependencies (no longer vendored
   under /modules/manager/js). The Picker constructor and the data set are both
   dynamically imported here and memoised across every picker on the page. */

interface EmojiSelectEvent {
  native: string;
}

/* emoji-mart types its Picker as extending a private HTMLElement shim, so the
   constructed instance isn't assignable to the DOM HTMLElement. At runtime it
   is a custom element node, so we type the constructor to return a DOM Node we
   can append + remove. */
type PickerCtor = new (opts: Record<string, unknown>) => HTMLElement;

let libPromise: Promise<{ Picker: PickerCtor; data: unknown }> | null = null;

function loadLib(): Promise<{ Picker: PickerCtor; data: unknown }> {
  if (!libPromise) {
    libPromise = Promise.all([import("emoji-mart"), import("@emoji-mart/data")]).then(
      ([mart, dataMod]) => ({
        Picker: (mart as unknown as { Picker: PickerCtor }).Picker,
        data: (dataMod as { default: unknown }).default,
      }),
    );
  }
  return libPromise;
}

/* mountEmojiPicker loads the lib + data (lazily, once) and mounts a picker
   into `target`, wiring onSelect to the picked emoji's native glyph and the
   theme to the current document state. Returns the created element so the
   caller can remove it on teardown. */
export async function mountEmojiPicker(
  target: HTMLElement,
  onSelect: (native: string) => void,
): Promise<HTMLElement> {
  const { Picker, data } = await loadLib();
  const picker = new Picker({
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
