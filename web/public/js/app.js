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
