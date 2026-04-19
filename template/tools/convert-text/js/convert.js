// Auto-convert text on input/change. Mirrors internal/convert-text/service.go.
(function () {
  function upper(s) { return s.toUpperCase(); }
  function lower(s) { return s.toLowerCase(); }
  function titleCase(s) {
    return s.split(/\s+/).filter(Boolean).map(function (w) {
      return w.charAt(0).toUpperCase() + w.slice(1).toLowerCase();
    }).join(' ');
  }
  function sentenceCase(s) {
    var l = s.toLowerCase();
    return l.charAt(0).toUpperCase() + l.slice(1);
  }
  function alternating(s) {
    var out = '', up = true;
    for (var i = 0; i < s.length; i++) {
      var c = s[i];
      if (/[a-z]/i.test(c)) {
        out += up ? c.toUpperCase() : c.toLowerCase();
        up = !up;
      } else {
        out += c;
      }
    }
    return out;
  }

  function linesToEscaped(s) {
    return s.replace(/\r\n/g, '\n').replace(/\r/g, '\n').replace(/\n/g, '\\n');
  }
  function escapedToLines(s) {
    return s.replace(/\\n/g, '\n');
  }

  function convert(text, type) {
    switch (type) {
      case 'uppercase': return upper(text);
      case 'lowercase': return lower(text);
      case 'titlecase': return titleCase(text);
      case 'sentence': return sentenceCase(text);
      case 'alternating': return alternating(text);
      case 'lines-to-escaped': return linesToEscaped(text);
      case 'escaped-to-lines': return escapedToLines(text);
      default: return text;
    }
  }

  function ensureResultNodes() {
    var host = document.getElementById('ct-result');
    if (!host) return null;
    return {
      wrap: host,
      pre: host.querySelector('pre'),
      copyBtn: host.querySelector('[data-copy]'),
    };
  }

  function run() {
    var text = document.getElementById('text');
    var type = document.getElementById('type');
    var r = ensureResultNodes();
    if (!text || !type || !r) return;
    var out = convert(text.value, type.value);
    r.pre.textContent = out;
    r.wrap.classList.toggle('hidden', text.value.length === 0);
  }

  function wireCopy() {
    var r = ensureResultNodes();
    if (!r || !r.copyBtn) return;
    r.copyBtn.addEventListener('click', function () {
      navigator.clipboard.writeText(r.pre.textContent).then(function () {
        var orig = r.copyBtn.textContent;
        r.copyBtn.textContent = 'Copied!';
        setTimeout(function () { r.copyBtn.textContent = orig; }, 1500);
      });
    });
  }

  function wireTypeList() {
    var type = document.getElementById('type');
    var list = document.getElementById('ct-type-list');
    if (!type || !list) return;
    var buttons = list.querySelectorAll('[data-type-option]');
    var activeOn = ['border-green-500', 'bg-green-200/40', 'dark:bg-green-800/30'];
    var activeOff = ['border-transparent', 'hover:border-white-400', 'dark:hover:border-navy-600', 'hover:bg-white-200', 'dark:hover:bg-navy-800'];

    function setActive(val) {
      type.value = val;
      buttons.forEach(function (b) {
        var on = b.getAttribute('data-value') === val;
        activeOn.forEach(function (c) { b.classList.toggle(c, on); });
        activeOff.forEach(function (c) { b.classList.toggle(c, !on); });
      });
    }

    buttons.forEach(function (b) {
      b.addEventListener('click', function () {
        setActive(b.getAttribute('data-value'));
        run();
      });
    });

    if (!type.value && buttons.length > 0) {
      setActive(buttons[0].getAttribute('data-value'));
    }
  }

  function wireSearch() {
    var input = document.getElementById('ct-search');
    var list = document.getElementById('ct-type-list');
    var empty = document.getElementById('ct-empty');
    if (!input || !list) return;
    var items = list.querySelectorAll('li');
    input.addEventListener('input', function () {
      var q = input.value.trim().toLowerCase();
      var visible = 0;
      items.forEach(function (li) {
        var btn = li.querySelector('[data-type-option]');
        var hay = (btn && btn.getAttribute('data-search') || '').toLowerCase();
        var match = q === '' || hay.indexOf(q) !== -1;
        li.classList.toggle('hidden', !match);
        if (match) visible++;
      });
      if (empty) empty.classList.toggle('hidden', visible !== 0);
    });
  }

  document.addEventListener('DOMContentLoaded', function () {
    var text = document.getElementById('text');
    var form = document.getElementById('ct-form');
    if (text) text.addEventListener('input', run);
    if (form) form.addEventListener('submit', function (e) { e.preventDefault(); run(); });
    wireTypeList();
    wireSearch();
    wireCopy();
    run();
  });
})();
