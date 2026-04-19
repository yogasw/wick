/** @type {import('tailwindcss').Config} */
module.exports = {
  darkMode: 'class',

  // Scan .templ source files and generated Go files for class names.
  content: [
    './internal/**/*.templ',
    './internal/**/*_templ.go',
    './web/public/js/app.js',
  ],

  theme: {
    // ── Project design system tokens.
    // Source: .claude/skills/design-system/tokens.md
    colors: {
      transparent: 'transparent',
      current: 'currentColor',

      // Brand Green — buttons, sidebar, primary actions
      green: {
        200: '#D1ECE5',
        300: '#A1D9CB',
        400: '#6EC5B2',
        500: '#27B199', // primary brand product color
        600: '#288372',
        700: '#24584E',
        800: '#1B312C',
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

  plugins: [],
}
