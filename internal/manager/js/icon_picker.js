// icon_picker.js — connector icon control: emoji-mart picker (vendored
// at vendor_emoji_mart.js + vendor_emoji_mart_data.json, no CDN at
// runtime) plus a custom paste slot for inline <svg> / data:image
// base64 (capped client-side at 32KB; the server re-validates). The
// value lives in the hidden [data-ip-value] input so form scripts
// read/write it like a plain field — they only need to dispatch a
// `change` event after programmatic prefill so the preview catches up.
(function () {
  const pickers = document.querySelectorAll("[data-icon-picker]");
  if (!pickers.length) return;

  const MAX_BYTES = 32 * 1024;
  // Emoji data is 400KB of JSON — fetched once, lazily, on the first
  // open of any picker on the page.
  let dataPromise = null;
  function emojiData() {
    if (!dataPromise) {
      dataPromise = fetch("/modules/manager/js/vendor_emoji_mart_data.json").then((r) => r.json());
    }
    return dataPromise;
  }

  pickers.forEach((root) => {
    const input = root.querySelector("[data-ip-value]");
    const toggle = root.querySelector("[data-ip-toggle]");
    const preview = root.querySelector("[data-ip-preview]");
    const panel = root.querySelector("[data-ip-panel]");
    const mount = root.querySelector("[data-ip-mount]");
    const custom = root.querySelector("[data-ip-custom]");
    const apply = root.querySelector("[data-ip-apply]");
    const errBox = root.querySelector("[data-ip-error]");
    let mounted = false;

    function paintPreview() {
      const v = (input.value || "").trim();
      preview.innerHTML = "";
      if (!v) {
        preview.textContent = "🔌";
        preview.classList.add("opacity-40");
        return;
      }
      preview.classList.remove("opacity-40");
      if (v.startsWith("data:image/") || v.startsWith("<svg")) {
        const img = document.createElement("img");
        img.className = "h-7 w-7 object-contain";
        img.alt = "";
        img.src = v.startsWith("<svg") ? "data:image/svg+xml;base64," + btoa(unescape(encodeURIComponent(v))) : v;
        preview.appendChild(img);
      } else {
        preview.textContent = v;
      }
    }

    function setValue(v) {
      input.value = v;
      paintPreview();
      panel.classList.add("hidden");
    }

    function showError(msg) {
      errBox.textContent = msg;
      errBox.classList.remove("hidden");
    }

    function mountPicker() {
      if (mounted || !window.EmojiMart) return;
      mounted = true;
      emojiData().then((data) => {
        const picker = new EmojiMart.Picker({
          data: data,
          onEmojiSelect: (e) => setValue(e.native),
          theme: document.documentElement.classList.contains("dark") ? "dark" : "light",
          previewPosition: "none",
          skinTonePosition: "none",
          perLine: 9,
          maxFrequentRows: 1,
        });
        mount.appendChild(picker);
      });
    }

    toggle.addEventListener("click", () => {
      errBox.classList.add("hidden");
      custom.value = "";
      panel.classList.toggle("hidden");
      if (!panel.classList.contains("hidden")) mountPicker();
    });
    document.addEventListener("click", (e) => {
      // emoji-mart renders in shadow DOM — composedPath keeps clicks
      // inside the picker from closing the panel.
      if (!e.composedPath().some((n) => n === root)) panel.classList.add("hidden");
    });

    apply.addEventListener("click", () => {
      errBox.classList.add("hidden");
      const v = custom.value.trim();
      if (!v) return;
      if (new Blob([v]).size > MAX_BYTES) {
        showError("Too large — max 32KB.");
        return;
      }
      if (v.startsWith("data:") && !v.startsWith("data:image/")) {
        showError("Only data:image/… payloads are allowed.");
        return;
      }
      if (v.startsWith("<") && !v.startsWith("<svg")) {
        showError("Only inline <svg> markup is allowed.");
        return;
      }
      setValue(v);
    });

    // programmatic prefill (edit pages) dispatches change on the input
    input.addEventListener("change", paintPreview);
    paintPreview();
  });
})();
