// Global app-wide helpers (loaded on every page via ui.Layout).

// Theme picker dropdown: open/close on caret click, close on outside
// click or Escape. The actual theme switch happens server-side via
// form POST to /theme — this only toggles menu visibility.
document.addEventListener('click', function (e) {
  var toggle = e.target.closest('[data-theme-toggle]');
  var picker = e.target.closest('[data-theme-picker]');
  document.querySelectorAll('[data-theme-menu]').forEach(function (menu) {
    var pickerEl = menu.closest('[data-theme-picker]');
    if (toggle && pickerEl === picker) {
      menu.classList.toggle('hidden');
    } else if (pickerEl !== picker) {
      menu.classList.add('hidden');
    }
  });
});

document.addEventListener('keydown', function (e) {
  if (e.key !== 'Escape') return;
  document.querySelectorAll('[data-theme-menu]').forEach(function (menu) {
    menu.classList.add('hidden');
  });
});

// Server-rendered timestamps in the viewer's own timezone.
//
// Pages are rendered in Go, so a formatted time comes out in the
// timezone of the host wick runs on — read by someone in another zone
// it is silently wrong, and nothing on screen says which zone it is.
// ui.LocalTime emits <time datetime="<RFC3339>" data-local="<format>">
// with the server's rendering as the text; this rewrites that text
// using the browser's clock, the way chat message times already work.
//
// Formatting is done by hand rather than via toLocaleString so the
// output matches the Go layouts in internal/pkg/ui/localtime.templ
// exactly — only the zone changes, never the shape. Change one, change
// the other.
(function () {
  var MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

  function pad(n) {
    return n < 10 ? '0' + n : String(n);
  }

  function render(d, format) {
    var date = MONTHS[d.getMonth()] + ' ' + d.getDate() + ', ' + d.getFullYear();
    var clock = pad(d.getHours()) + ':' + pad(d.getMinutes());
    if (format === 'date') return date;
    if (format === 'iso') return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) + ' ' + clock;
    return date + ' ' + clock;
  }

  function localizeTimes(root) {
    var scope = root || document;
    scope.querySelectorAll('time[data-local]:not([data-localized])').forEach(function (el) {
      var iso = el.getAttribute('datetime');
      if (!iso) return;
      var d = new Date(iso);
      // An unparseable datetime leaves the server's text alone — a
      // wrong zone still beats "Invalid Date".
      if (isNaN(d.getTime())) return;
      el.textContent = render(d, el.getAttribute('data-local'));
      // Full local string names the zone, for anyone checking.
      el.title = d.toString();
      el.setAttribute('data-localized', '');
    });
  }

  // Exposed so a page that injects markup after load can re-run it.
  window.wickLocalizeTimes = localizeTimes;

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () { localizeTimes(); });
  } else {
    localizeTimes();
  }
})();
