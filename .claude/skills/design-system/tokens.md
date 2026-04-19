# Design Tokens

Complete token catalog. All values match `tailwind.config.js` and
`web/src/input.css`. This is the single source of truth — if a value
isn't here, it doesn't exist in the system.

---

## 1. Color palette

### 1.1 Green — primary accent, actions

Fixed across all themes. Used for buttons, sidebar accents, active states,
and interactive elements. **Never** for "success" status.

| Token | Hex |
|---|---|
| `green-200` | `#D1ECE5` |
| `green-300` | `#A1D9CB` |
| `green-400` | `#6EC5B2` |
| `green-500` *(base)* | `#27B199` |
| `green-600` | `#288372` |
| `green-700` | `#24584E` |
| `green-800` | `#1B312C` |

### 1.2 Navy — dark surfaces, login, secondary accent

Themed via CSS variables — each theme remaps these for its dark palette.

| Token | Default hex |
|---|---|
| `navy-200` | `#C4CBD9` |
| `navy-300` | `#8A9AB3` |
| `navy-400` | `#506C8F` |
| `navy-500` *(base)* | `#01416C` |
| `navy-600` | `#103352` |
| `navy-700` | `#14263A` |
| `navy-800` | `#121923` |

### 1.3 Black — all text colors

Themed via CSS variables. Title, subtitle, paragraph, and disabled text
all come from this ramp. Never color body text with green or navy.

| Token | Default hex | Role |
|---|---|---|
| `black-600` | `#BFBFBF` | Disabled text, faintest |
| `black-700` | `#A0A0A0` | Disabled text, placeholder |
| `black-800` | `#565656` | Muted text, subtitles |
| `black-900` | `#0A0A0A` | Primary text, titles |

### 1.4 White — backgrounds, borders, surfaces

Themed via CSS variables for light-mode surfaces.

| Token | Default hex | Role |
|---|---|---|
| `white-100` | `#FFFFFF` | Page/card background |
| `white-200` | `#FAFAFA` | Subtle surface, body bg |
| `white-300` | `#ECECEC` | Border, divider |
| `white-400` | `#DFDFDF` | Heavier border, input default |

### 1.5 Status — Positive (success)

| Token | Hex |
|---|---|
| `pos-100` | `#F1F9EF` |
| `pos-200` | `#C9E7BF` |
| `pos-300` | `#A0D491` |
| `pos-400` | `#288C7A` |

### 1.6 Status — Progressive (info)

| Token | Hex |
|---|---|
| `prog-100` | `#EEFAFE` |
| `prog-200` | `#C3EBFA` |
| `prog-300` | `#94DBF6` |
| `prog-400` | `#56CCF2` |

### 1.7 Status — Caution (warning/awareness)

| Token | Hex |
|---|---|
| `cau-100` | `#FFF2D1` |
| `cau-200` | `#FFE5B5` |
| `cau-300` | `#FFC86F` |
| `cau-400` | `#D78E08` |

### 1.8 Status — Negative (destructive)

| Token | Hex |
|---|---|
| `neg-100` | `#FFD7D2` |
| `neg-200` | `#FFC9C4` |
| `neg-300` | `#F7857E` |
| `neg-400` | `#EB5757` |

### 1.9 Error — banners, field validation

| Token | Hex |
|---|---|
| `error-100` | `#FFD7D2` |
| `error-400` | `#EF4C00` |
| `error-800` | `#9D380F` |

### 1.10 Warning — banners

| Token | Hex |
|---|---|
| `warning-100` | `#FFF2D1` |
| `warning-400` | `#D78E08` |
| `warning-800` | `#A0772A` |

### 1.11 Link — hyperlinks

| Token | Hex |
|---|---|
| `link-100` | `#EEFAFE` |
| `link-400` | `#007BFF` |
| `link-800` | `#2553A5` |

### 1.12 Color usage rules

- `green-*` = primary accent. Don't use for success — use `pos-*`.
- `navy-*` = dark surfaces / login. Don't use for info — use `prog-*`.
- Status ramps are role-locked. Don't swap them across intents.
- Text always comes from `black-600` → `black-900`.
- Disabled elements use `black-600`/`black-700` for text and
  `white-300`/`white-400` for borders/surfaces.

---

## 2. Dark / light pairing cheatsheet

Every color class needs a `dark:` counterpart when the element sits on a
surface that changes between themes.

| Light | Dark | Used for |
|---|---|---|
| `bg-white-100` | `dark:bg-navy-700` | Card, input, surface |
| `bg-white-200` | `dark:bg-navy-800` | Page body, subtle bg |
| `text-black-900` | `dark:text-white-100` | Primary text |
| `text-black-800` | `dark:text-black-600` | Muted / subtitle text |
| `border-white-300` | `dark:border-navy-600` | Borders, dividers |
| `border-white-400` | `dark:border-navy-500` | Input borders |
| `placeholder:text-black-700` | `dark:placeholder:text-black-600` | Placeholder text |

Green tokens don't need `dark:` pairing — they're fixed across themes.
Status tokens (`pos-*`, `neg-*`, etc.) are also fixed.

---

## 3. Spacing scale

8-pixel grid. All spacing uses these values:

```
4px   8px   12px   16px   20px   24px   32px   40px   48px   56px   64px   72px   80px
```

Tailwind mapping:
- `p-1` (4px), `p-2` (8px), `p-3` (12px), `p-4` (16px), `p-5` (20px),
  `p-6` (24px), `p-8` (32px), `p-10` (40px), `p-12` (48px)
- Prefer multiples of 8. Use 4/12/20 only when 8 is too coarse.
- Never use arbitrary values like `p-[13px]`.

---

## 4. Typography

### Font family

`Inter` is the only typeface. Mapped to `font-sans` in Tailwind config:

```js
fontFamily: {
  sans: ['Inter', 'system-ui', '-apple-system', 'BlinkMacSystemFont',
         '"Segoe UI"', 'sans-serif'],
  mono: ['Menlo', 'Consolas', 'Monaco', 'monospace'],
}
```

### Font weights (only these three)

| Weight | Tailwind class | Use for |
|---|---|---|
| 400 | `font-normal` | Body text, descriptions |
| 500 | `font-medium` | Labels, nav items, subtle emphasis |
| 600 | `font-semibold` | Headings, titles, strong emphasis |

Do not use `font-bold` (700), `font-light` (300), or italic.

### Type hierarchy

| Role | Classes |
|---|---|
| Page heading | `text-[1.75rem] font-semibold leading-tight tracking-tight` |
| Section heading | `text-lg font-semibold` or `text-xl font-semibold` |
| Body text | `text-sm font-normal` or `text-base font-normal` |
| Small / helper text | `text-xs font-normal` |
| Label | `text-sm font-medium` |

Text color always from the Black ramp (`text-black-900 dark:text-white-100`
for primary, `text-black-800 dark:text-black-600` for muted).

---

## 5. Iconography

- **Container size:** multiple of 8 — `16px` (`h-4 w-4`), `20px`
  (`h-5 w-5`), `24px` (`h-6 w-6`), `32px` (`h-8 w-8`).
- **Stroke width:** `2px` (or `stroke-width="2"`).
- **Padding:** `1–2px` between glyph and container edge.
- **Style:** `fill="none" stroke="currentColor" stroke-linecap="round"
  stroke-linejoin="round"`.

```html
<svg class="h-5 w-5" viewBox="0 0 24 24" fill="none"
     stroke="currentColor" stroke-width="2"
     stroke-linecap="round" stroke-linejoin="round">
  <!-- paths -->
</svg>
```

---

## 6. Shadows

Four tiers defined in `tailwind.config.js`:

| Tailwind | Tier | Use for |
|---|---|---|
| `shadow-sm` | Small | Subtle lift — navbar, small buttons |
| `shadow-md` | Medium | Cards, panels, navigation elements |
| `shadow-lg` | Large | Modals, banners, main panels |
| `shadow-xl` | Extra Large | Hero sections, large popups |

```js
boxShadow: {
  sm:  '0 1px 3px 0 rgba(0,0,0,0.06)',
  md:  '0 4px 12px -2px rgba(0,0,0,0.08), 0 2px 4px -1px rgba(0,0,0,0.05)',
  lg:  '0 10px 24px -4px rgba(0,0,0,0.10), 0 4px 8px -2px rgba(0,0,0,0.06)',
  xl:  '0 20px 40px -8px rgba(0,0,0,0.12), 0 8px 16px -4px rgba(0,0,0,0.08)',
}
```

---

## 7. Border radius

| Tailwind | Value |
|---|---|
| `rounded-sm` | `4px` |
| `rounded` | `8px` |
| `rounded-lg` | `12px` |
| `rounded-xl` | `16px` |
| `rounded-full` | `9999px` |

---

## 8. Container and layout

| Token | Value |
|---|---|
| `max-w-container` | `1120px` |
| Dark mode | `class` strategy (`dark:` prefix) |
| Content scan | `.templ`, `*_templ.go`, `web/public/js/app.js` |

---

## 9. Available themes

12 themes defined in `web/src/input.css`. Each remaps `navy-*`, `black-*`,
`white-*` CSS variables. Green stays fixed.

| Theme | Type |
|---|---|
| Light (default) | light |
| Dark | dark |
| Solarized Light / Dark | light / dark |
| Dracula | dark |
| Nord | dark |
| Gruvbox Dark | dark |
| GitHub Light / Dark | light / dark |
| Material Light / Dark | light / dark |
| Monokai | dark |
