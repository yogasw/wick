---
name: design-system
description: Use when creating or editing any UI component, styling, button, input, form, card, modal, or layout. Enforces the project design system — Inter font, 8-pixel grid, green/navy palette, named color tokens, dark/light theming, responsive layout, and icon/spacing rules.
allowed-tools: Read, Grep, Glob, Edit, Write
paths:
  - "**/*.templ"
  - "**/*_templ.go"
  - "**/*.go"
  - "**/*.js"
  - "**/*.css"
  - "**/tailwind.config.*"
---

# Design System

Enforces the project's visual language whenever Claude writes or edits UI
code — templ components, Tailwind classes, CSS tokens, theme files, or
`tailwind.config.*`. Every rule below is derived from the living token set
in `tailwind.config.js` and `web/src/input.css`.

## When this skill activates

- Creating, editing, or refactoring a UI component (button, input, card,
  modal, dialog, tab, list, badge, notification, nav, layout, etc.)
- Writing or updating CSS, Tailwind classes, design tokens, theme
  variables, or `tailwind.config.*`
- Choosing colors, font sizes, spacing, padding, margins, border-radius,
  shadows, or icon sizes
- Building a page layout, grid, container, or responsive breakpoint

Skip for purely backend / non-visual work.

## Core rules

1. **Inter font only.** All text uses `Inter` via `font-sans`. No other
   typeface in the UI.

2. **8-pixel grid.** Spacing, padding, margin, width, and height are
   multiples of 8 (`8, 16, 24, 32, 40, 48, 56, 64, 72, 80`). The
   sub-8 exceptions `4px`, `12px`, and `20px` are allowed when 8-grid
   is too coarse.

3. **Named tokens only.** Every color maps to a Tailwind token defined in
   `tailwind.config.js` (see `tokens.md`). Raw hex is allowed only
   inside the token definition itself.

4. **Accent vs. status separation.** `green-*` is the primary accent
   color — never reuse it for "success." `navy-*` is for dark surfaces
   and login — never reuse it for "info." Status intent uses the
   dedicated ramps: `pos-*`, `prog-*`, `cau-*`, `neg-*`, `error-*`,
   `warning-*`, `link-*`.

5. **Text from Black ramp.** All body/title/paragraph text uses
   `black-600` through `black-900`. Never color body text with
   green or navy.

6. **Dark + light mode required.** Every color class must have a `dark:`
   counterpart. Use the themed CSS-variable tokens (`navy-*`, `black-*`,
   `white-*`) so themes work automatically. See the "Theming" section
   below.

7. **Mobile-first and responsive.** Build for narrow viewports first,
   then layer `sm:` / `md:` / `lg:` overrides. Test mentally at ≤375px
   before declaring done.

8. **Icons.** Container is a multiple of 8 (`16, 18, 24, 32, 40, 48`px).
   Stroke width `2px`. Padding `1–2px` between glyph and container edge.

9. **Font weights: 400 / 500 / 600 only.** `font-normal` (400),
   `font-medium` (500), `font-semibold` (600). No bold (700), no light
   (300), no italic — unless the user explicitly asks.

10. **No invented values.** If a token doesn't exist, reuse the closest
    project value or ask the user. Never add arbitrary Tailwind values
    like `p-[13px]` or inline hex.

## Token quick-reference

Full catalog in `tokens.md`. Most-used values:

| Purpose | Token | Hex |
|---|---|---|
| Primary accent (buttons, sidebar) | `green-500` | `#27B199` |
| Secondary accent (login, dark surfaces) | `navy-500` | `#01416C` |
| Body text default | `black-900` | `#0A0A0A` |
| Body text muted / subtitle | `black-800` | `#565656` |
| Disabled text | `black-700` / `black-600` | `#A0A0A0` / `#BFBFBF` |
| Page background | `white-100` | `#FFFFFF` |
| Surface / subtle bg | `white-200` | `#FAFAFA` |
| Border / divider | `white-300` / `white-400` | `#ECECEC` / `#DFDFDF` |
| Success | `pos-400` | `#288C7A` |
| Info | `prog-400` | `#56CCF2` |
| Warning | `cau-400` / `warning-400` | `#D78E08` |
| Destructive / error | `neg-400` / `error-400` | `#EB5757` / `#EF4C00` |
| Hyperlink | `link-400` | `#007BFF` |

**Spacing (px):** `4, 8, 12, 16, 20, 24, 32, 40, 48, 56, 64, 72, 80`
**Font:** `Inter, sans-serif` — weights `400`, `500`, `600`
**Shadows:** `shadow-sm` → `shadow-md` → `shadow-lg` → `shadow-xl`
**Radius:** `rounded-sm` (4px) → `rounded` (8px) → `rounded-lg` (12px)
→ `rounded-xl` (16px) → `rounded-full`

## Theming

The project ships 12 themes. Colors that change per theme (`navy-*`,
`black-*`, `white-*`) are backed by CSS variables in `web/src/input.css`.
Green is fixed across all themes for visual consistency.

**What this means for you:**

- Always pair light and dark variants: `bg-white-100 dark:bg-navy-700`,
  `text-black-900 dark:text-white-100`, `border-white-300 dark:border-navy-600`.
- Never hardcode `rgb(...)` or `#hex` for themed colors — use the
  Tailwind tokens which resolve to CSS vars at runtime.
- Green tokens (`green-200` through `green-800`) are hardcoded, not
  themed — they look the same in every theme. This is intentional.

## Layout conventions

- **Page container:** `<main class="mx-auto w-full max-w-container px-6 py-8">`
- **Always use `@ui.Layout(title)`** — never write raw `<html>`.
- **Always use `@ui.Navbar(user)`** — never build a custom nav.
- **Container max-width:** `1120px` (Tailwind `max-w-container`).

## Supporting files

Read on demand — don't load all at once:

- **`tokens.md`** — Complete color palette (all hex values), spacing
  scale, typography rules, icon sizing, shadow tiers. Load when picking
  colors, setting spacing, or editing `tailwind.config.*`.
- **`components.md`** — Patterns for buttons, inputs, cards, tabs, modals,
  badges, etc. with correct and incorrect examples in templ/Tailwind.
  Load when building or editing a specific component.

## Regenerate after every UI edit

- Edited a `.templ` → `templ generate`
- Added/changed Tailwind classes → `./bin/tailwindcss* -i web/src/input.css -o web/public/css/app.css --minify`
- Both → do both, then `go build ./...`
- Shortcut: `make generate` runs all three.

## Checklist (mental check before finishing)

- [ ] All text uses `font-sans` (mapped to Inter)
- [ ] Every color is a named token, no raw hex
- [ ] Green used only for primary accent, not "success"
- [ ] Status colors from `pos / prog / cau / neg / error / warning / link`
- [ ] Body text uses Black ramp, not green/navy
- [ ] All spacing on the 8-pixel scale
- [ ] Every color class has a `dark:` counterpart
- [ ] Layout is usable at ≤375px viewport
- [ ] Icons sized 16/18/24/32/40/48px, 2px stroke
- [ ] No arbitrary values, no invented tokens
