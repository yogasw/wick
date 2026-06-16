/** @type {import('tailwindcss').Config} */
module.exports = {
  darkMode: 'class',

  // Only emit hover: styles on devices that actually support hover, so
  // tapping on touch screens doesn't leave buttons stuck in :hover.
  future: {
    hoverOnlyWhenSupported: true,
  },

  // Scan .templ source files and generated Go files for class names.
  // FE/Svelte files are scanned too so classes used only by the Svelte
  // workflow editor (`right-3`, `translate-x-3.5`, dark:bg-slate-* etc.)
  // make it into the emitted CSS — without this the editor falls back
  // to default browser styles and overlay buttons land at left:0.
  content: [
    './internal/**/*.templ',
    './internal/**/*_templ.go',
    './internal/**/*.js',
    './web/public/js/app.js',
    // Narrow to FE source dirs — broader `./fe/**/*.{svelte,ts}`
    // walks every workspace's node_modules and triples scan time.
    './fe/agents/*/src/**/*.{svelte,ts}',
    './fe/agents/*/index.html',
    './fe/common/*/src/**/*.{svelte,ts}',
    './fe/manager/src/**/*.{svelte,ts}',
    './fe/manager/index.html',
  ],

  theme: {
    extend: {},
    // ── Project design system tokens.
    // Source: .claude/skills/design-system/tokens.md
    // NOTE: top-level `colors` replaces ALL Tailwind defaults. Custom tokens
    // live here intentionally so the palette is fully controlled. Classes that
    // use colors not listed here (red, amber, blue, …) must be added below or
    // they will be purged. Add missing Tailwind defaults as needed rather than
    // moving custom tokens into extend.colors (which would re-introduce the
    // full 800-class default palette and bloat the CSS).
    colors: {
      transparent: 'transparent',
      current: 'currentColor',

      // ── Tailwind defaults kept for compatibility ──────────────────────────
      // red: used by Block button, error states, workflow inspector
      // exec / mock-data accents
      red: {
        50:  '#fef2f2',
        300: '#fca5a5',
        400: '#f87171',
        500: '#ef4444',
        600: '#dc2626',
        700: '#b91c1c',
        800: '#991b1b',
        900: '#7f1d1d',
      },
      // amber: used by gate-disabled banner, countdown pulse
      amber: {
        50:  '#fffbeb',
        300: '#fcd34d',
        500: '#f59e0b',
        700: '#b45309',
        800: '#92400e',
        900: '#78350f',
      },
      // blue: used by workflow Publish button + draft-approved badge
      blue: {
        400: '#60a5fa',
        500: '#3b82f6',
        600: '#2563eb',
        700: '#1d4ed8',
      },

      // ── Workflow v2 editor palette (Svelte FE) ────────────────────────────
      // Mirrors the legacy editor.css node-head colours so the Svelte
      // editor renders the same TRIGGER / CLASSIFY / AGENT / DATATABLE
      // pills. Restricted to the shades actually used to keep the
      // emitted CSS lean.
      rose:    { 100: '#ffe4e6', 300: '#fda4af', 500: '#f43f5e', 600: '#e11d48', 700: '#be123c' },
      emerald: { 100: '#d1fae5', 300: '#6ee7b7', 400: '#34d399', 500: '#10b981', 600: '#059669' },
      slate:   { 100: '#f1f5f9', 200: '#e2e8f0', 300: '#cbd5e1', 400: '#94a3b8', 500: '#64748b', 600: '#475569', 700: '#334155', 800: '#1e293b', 900: '#0f172a' },
      sky:     { 100: '#e0f2fe', 300: '#7dd3fc', 500: '#0ea5e9', 700: '#0369a1', 800: '#075985' },
      fuchsia: { 100: '#fae8ff', 700: '#a21caf', 800: '#86198f' },
      indigo:  { 100: '#e0e7ff', 500: '#6366f1', 700: '#4338ca' },
      violet:  { 100: '#ede9fe', 500: '#8b5cf6', 700: '#6d28d9', 800: '#5b21b6' },
      pink:    { 100: '#fce7f3', 500: '#ec4899', 700: '#be185d', 800: '#9d174d' },
      lime:    { 100: '#ecfccb', 500: '#84cc16', 600: '#65a30d', 800: '#3f6212' },
      cyan:    { 100: '#cffafe', 300: '#67e8f9', 500: '#06b6d4', 700: '#0e7490', 800: '#155e75' },
      teal:    { 100: '#ccfbf1', 500: '#14b8a6', 600: '#0d9488', 800: '#115e59' },
      yellow:  { 100: '#fef9c3', 200: '#fef08a', 300: '#fde047', 500: '#eab308', 700: '#a16207', 800: '#854d0e' },
      orange:  { 300: '#fdba74', 400: '#fb923c', 500: '#f97316' },

      // Brand Green — buttons, sidebar, primary actions
      green: {
        50:  '#f0fdf9',
        200: '#D1ECE5',
        300: '#A1D9CB',
        400: '#6EC5B2',
        500: '#27B199', // primary brand product color
        600: '#288372',
        700: '#24584E',
        800: '#1B312C',
        900: '#0f2420',
      },

      // Brand Navy — login pages, marketing. Themed: values come from
      // CSS variables in web/src/input.css so each theme can re-skin
      // the dark-mode surfaces without touching class names.
      navy: {
        200: 'rgb(var(--color-navy-200) / <alpha-value>)',
        300: 'rgb(var(--color-navy-300) / <alpha-value>)',
        400: 'rgb(var(--color-navy-400) / <alpha-value>)',
        500: 'rgb(var(--color-navy-500) / <alpha-value>)',
        600: 'rgb(var(--color-navy-600) / <alpha-value>)',
        700: 'rgb(var(--color-navy-700) / <alpha-value>)',
        800: 'rgb(var(--color-navy-800) / <alpha-value>)',
      },

      // Black — text tokens. Themed.
      black: {
        600: 'rgb(var(--color-black-600) / <alpha-value>)',
        700: 'rgb(var(--color-black-700) / <alpha-value>)',
        800: 'rgb(var(--color-black-800) / <alpha-value>)',
        900: 'rgb(var(--color-black-900) / <alpha-value>)',
      },

      // White / Grey — light-mode backgrounds. Themed.
      white: {
        100: 'rgb(var(--color-white-100) / <alpha-value>)',
        200: 'rgb(var(--color-white-200) / <alpha-value>)',
        300: 'rgb(var(--color-white-300) / <alpha-value>)',
        400: 'rgb(var(--color-white-400) / <alpha-value>)',
      },

      // Status — Positive (success)
      pos: {
        100: '#F1F9EF',
        200: '#C9E7BF',
        300: '#A0D491',
        400: '#288C7A',
      },

      // Status — Progressive (informational)
      prog: {
        100: '#EEFAFE',
        200: '#C3EBFA',
        300: '#94DBF6',
        400: '#56CCF2',
      },

      // Status — Cautions (awareness)
      cau: {
        100: '#FFF2D1',
        200: '#FFE5B5',
        300: '#FFC86F',
        400: '#D78E08',
      },

      // Status — Negative
      neg: {
        100: '#FFD7D2',
        200: '#FFC9C4',
        300: '#F7857E',
        400: '#EB5757',
      },

      // Error — error banner and field
      error: {
        100: '#FFD7D2',
        400: '#EF4C00',
        800: '#9D380F',
      },

      // Warning — warning banner
      warning: {
        100: '#FFF2D1',
        400: '#D78E08',
        800: '#A0772A',
      },

      // Link — hyperlinks
      link: {
        100: '#EEFAFE',
        400: '#007BFF',
        800: '#2553A5',
      },
    },

    extend: {
      // Inter — the only typeface
      fontFamily: {
        sans: ['Inter', 'system-ui', '-apple-system', 'BlinkMacSystemFont', '"Segoe UI"', 'sans-serif'],
        mono: ['Menlo', 'Consolas', 'Monaco', 'monospace'],
      },

      // Custom max-width for the page container (1120px)
      maxWidth: {
        container: '1120px',
      },

      // Shadow tiers — small to extra large
      boxShadow: {
        sm:  '0 1px 3px 0 rgba(0,0,0,0.06)',                                                      // Small Object Shadow
        md:  '0 4px 12px -2px rgba(0,0,0,0.08), 0 2px 4px -1px rgba(0,0,0,0.05)',               // Medium Object Shadow
        lg:  '0 10px 24px -4px rgba(0,0,0,0.10), 0 4px 8px -2px rgba(0,0,0,0.06)',              // Large Object Shadow
        xl:  '0 20px 40px -8px rgba(0,0,0,0.12), 0 8px 16px -4px rgba(0,0,0,0.08)',             // Extra Large Object Shadow
      },

      // Border radius — 8-pixel grid; exact values not in templates so using 8-grid
      borderRadius: {
        sm:   '4px',
        DEFAULT: '8px',
        lg:   '12px',
        xl:   '16px',
        full: '9999px',
      },
    },
  },

  plugins: [
    function({ addComponents }) {
      addComponents({
        '.toggle-track': {
          position: 'relative',
          display: 'inline-block',
          width: '36px',
          height: '20px',
          borderRadius: '9999px',
          backgroundColor: '#A0A0A0',
          transition: 'background-color 200ms',
          cursor: 'pointer',
          flexShrink: '0',
        },
        '.toggle-track.is-on': {
          backgroundColor: '#27B199',
        },
        '.toggle-knob': {
          position: 'absolute',
          top: '2px',
          left: '2px',
          width: '16px',
          height: '16px',
          borderRadius: '9999px',
          backgroundColor: '#ffffff',
          boxShadow: '0 1px 3px 0 rgba(0,0,0,0.2)',
          transition: 'transform 200ms',
        },
        '.toggle-track.is-on .toggle-knob': {
          transform: 'translateX(16px)',
        },
      });
    },
  ],
}
