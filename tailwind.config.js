/** @type {import('tailwindcss').Config} */
module.exports = {
  darkMode: 'class',

  // Scan .templ source files and generated Go files for class names.
  content: [
    './internal/**/*.templ',
    './internal/**/*_templ.go',
    './internal/**/*.js',
    './web/public/js/app.js',
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
