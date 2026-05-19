// picker.js — inline-chip multi-select widget.
//
// Single input box, chips appear inline as the operator commits each
// token. Two paths into the same chip list:
//
//   Lookup — type a query, debounced fetch hits the lookup endpoint
//     (data-picker-lookup-url + data-picker-source). Dropdown of
//     {id, name} candidates; click a row to commit the chip with the
//     looked-up name.
//
//   Raw / paste — press comma, Enter, or paste a comma- or newline-
//     separated blob. Every non-empty token becomes a chip with
//     id = name = token. Lets the operator paste raw IDs from a log
//     line or bulk-config "C1,C2,C3" → three chips.
//
//   Backspace on an empty input pops the last chip. Familiar from
//   Gmail / Slack recipient pickers.
//
// Value lives on the wrap's hidden <input data-field-key> as JSON
// `[{id, name}, ...]`. Every chip mutation re-serialises and fires
// `input` + `change` so DOM-polling consumers catch the edit.
//
// Init lives in this file (load once via <script src=…> in the
// editor bootstrap). The script auto-wires every .wf-picker on
// DOMContentLoaded and exposes window.wickInitPickers(root) for
// hydrate flows that inject pickers via innerHTML.

(function () {
    'use strict';

    function readValue(wrap) {
        const hidden = wrap.querySelector('input[type="hidden"][data-field-key]');
        if (!hidden) return [];
        try { return JSON.parse(hidden.value || '[]') || []; }
        catch (_) { return []; }
    }

    function writeValue(wrap, arr) {
        const hidden = wrap.querySelector('input[type="hidden"][data-field-key]');
        if (!hidden) return;
        hidden.value = JSON.stringify(arr);
        hidden.dispatchEvent(new Event('input', { bubbles: true }));
        hidden.dispatchEvent(new Event('change', { bubbles: true }));
    }

    function renderChips(wrap) {
        const list = wrap.querySelector('[data-picker-chips]');
        if (!list) return;
        const arr = readValue(wrap);
        list.innerHTML = '';
        arr.forEach((item) => {
            const chip = document.createElement('span');
            chip.className = 'wf-picker-chip';
            chip.dataset.chipId = item.id;
            const label = document.createElement('span');
            label.className = 'wf-picker-chip-label';
            label.textContent = item.name || item.id;
            chip.appendChild(label);
            if (item.name && item.name !== item.id) {
                const sub = document.createElement('span');
                sub.className = 'wf-picker-chip-id';
                sub.textContent = item.id;
                chip.appendChild(sub);
            }
            const rm = document.createElement('button');
            rm.type = 'button';
            rm.className = 'wf-picker-chip-remove';
            rm.setAttribute('aria-label', 'Remove');
            rm.textContent = '×';
            rm.addEventListener('click', () => {
                writeValue(wrap, readValue(wrap).filter((it) => it.id !== item.id));
                renderChips(wrap);
            });
            chip.appendChild(rm);
            list.appendChild(chip);
        });
    }

    function addChips(wrap, items) {
        if (!items || !items.length) return 0;
        const arr = readValue(wrap);
        const seen = new Set(arr.map((it) => it.id));
        let added = 0;
        items.forEach((it) => {
            if (!it || !it.id) return;
            if (seen.has(it.id)) return;
            seen.add(it.id);
            arr.push({ id: it.id, name: it.name || it.id });
            added++;
        });
        if (added === 0) return 0;
        writeValue(wrap, arr);
        renderChips(wrap);
        return added;
    }

    function showStatus(wrap, msg, kind) {
        const results = wrap.querySelector('[data-picker-results]');
        if (!results) return;
        results.innerHTML = '<div class="wf-picker-status wf-picker-status-' + kind + '">' + msg + '</div>';
        results.classList.remove('hidden');
    }

    function showSearchResults(wrap, items) {
        const results = wrap.querySelector('[data-picker-results]');
        const input = wrap.querySelector('[data-picker-input]');
        if (!results) return;
        if (!items || items.length === 0) {
            showStatus(wrap, 'No results — press Enter to add as raw ID', 'empty');
            return;
        }
        results.innerHTML = '';
        items.forEach((it) => {
            const row = document.createElement('button');
            row.type = 'button';
            row.className = 'wf-picker-result-row';
            const nm = document.createElement('span');
            nm.className = 'wf-picker-result-name';
            nm.textContent = it.name || it.id;
            const id = document.createElement('span');
            id.className = 'wf-picker-result-id';
            id.textContent = it.id;
            row.appendChild(nm);
            row.appendChild(id);
            row.addEventListener('click', () => {
                addChips(wrap, [it]);
                if (input) input.value = '';
                results.classList.add('hidden');
            });
            results.appendChild(row);
        });
        results.classList.remove('hidden');
    }

    // commitRaw parses the input buffer into one or more chips with
    // id=name=token. Separators: comma + newline. Empty tokens are
    // dropped. Used on Enter, comma keystroke, and paste with bulk
    // payload.
    function commitRaw(wrap, raw) {
        const tokens = (raw || '').split(/[,\n]+/).map((s) => s.trim()).filter(Boolean);
        if (!tokens.length) return false;
        const added = addChips(wrap, tokens.map((t) => ({ id: t, name: t })));
        return added > 0;
    }

    function wireInput(wrap) {
        const input = wrap.querySelector('[data-picker-input]');
        const results = wrap.querySelector('[data-picker-results]');
        if (!input) return;

        let timer = null;
        let reqSeq = 0;

        function debouncedLookup() {
            clearTimeout(timer);
            timer = setTimeout(() => {
                const lookupURL = wrap.getAttribute('data-picker-lookup-url') || '';
                const source = wrap.getAttribute('data-picker-source') || '';
                if (!lookupURL) {
                    showStatus(wrap, 'Lookup not wired — comma or Enter to add as raw ID', 'empty');
                    return;
                }
                const q = input.value.trim();
                const sep = lookupURL.indexOf('?') >= 0 ? '&' : '?';
                const url = lookupURL + sep + 'source=' + encodeURIComponent(source) + '&q=' + encodeURIComponent(q);
                const seq = ++reqSeq;
                showStatus(wrap, 'Searching…', 'loading');
                fetch(url, { credentials: 'same-origin' })
                    .then((r) => r.ok ? r.json() : [])
                    .then((items) => {
                        if (seq !== reqSeq) return;
                        showSearchResults(wrap, items);
                    })
                    .catch(() => {
                        if (seq !== reqSeq) return;
                        showStatus(wrap, 'Lookup failed — Enter to add as raw ID', 'error');
                    });
            }, 250);
        }

        input.addEventListener('input', (e) => {
            // Comma in value = operator wants raw commit. Strip the
            // trailing comma + commit immediately so paste-then-comma
            // doesn't surface a stray dropdown for ", " queries.
            if (input.value.indexOf(',') >= 0) {
                const buf = input.value;
                input.value = '';
                commitRaw(wrap, buf);
                if (results) results.classList.add('hidden');
                return;
            }
            debouncedLookup();
        });

        input.addEventListener('focus', debouncedLookup);

        input.addEventListener('keydown', (e) => {
            if (e.key === 'Enter') {
                e.preventDefault();
                const buf = input.value.trim();
                if (buf === '') return;
                input.value = '';
                commitRaw(wrap, buf);
                if (results) results.classList.add('hidden');
                return;
            }
            if (e.key === 'Backspace' && input.value === '') {
                // Pop the last chip — Gmail / Slack convention.
                const arr = readValue(wrap);
                if (arr.length === 0) return;
                arr.pop();
                writeValue(wrap, arr);
                renderChips(wrap);
                return;
            }
        });

        input.addEventListener('paste', (e) => {
            const text = (e.clipboardData || window.clipboardData)?.getData('text') || '';
            if (text.indexOf(',') < 0 && text.indexOf('\n') < 0) return;
            // Bulk paste with separators → split + commit, swallow
            // the default insert so the textarea stays clean.
            e.preventDefault();
            commitRaw(wrap, text);
            if (results) results.classList.add('hidden');
        });

        document.addEventListener('click', (e) => {
            if (!wrap.contains(e.target) && results) {
                results.classList.add('hidden');
            }
        });
    }

    function initPicker(wrap) {
        if (!wrap || wrap.dataset.pickerWired === '1') return;
        wrap.dataset.pickerWired = '1';
        wireInput(wrap);
        renderChips(wrap);
    }

    function initAll(root) {
        const scope = root || document;
        scope.querySelectorAll('.wf-picker').forEach(initPicker);
    }

    window.wickInitPickers = initAll;
    // wickRefreshPicker re-renders chips from the hidden input's current
    // value. Call after programmatically setting the hidden input value
    // (e.g. during form hydration) so chips reflect the restored state.
    window.wickRefreshPicker = function (root) {
        const scope = root || document;
        scope.querySelectorAll('.wf-picker').forEach(renderChips);
    };

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', () => initAll(document));
    } else {
        initAll(document);
    }
})();
