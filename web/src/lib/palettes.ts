// Palette engine. A "palette" is a set of 7 anchor colors for one
// mode (light or dark). From those anchors we derive the full set of CSS
// variables the app actually consumes — both the Tailwind `--color-gray-*`
// scale (the app uses `bg-gray-900`, `text-gray-400`, … directly) and the
// shadcn semantic tokens (`--background`, `--card`, `--primary`, …). Applying a
// palette = writing those vars as inline styles on <html>, which override the
// stylesheet defaults for whichever mode is currently active.
//
// Built-ins and user-created palettes share the exact same shape and builder,
// so there is one code path for "how a palette becomes colors on screen".

export type PaletteMode = 'light' | 'dark'

/** The 7 editable roles. Keep in sync with the backend `anchorKeys`. */
export interface PaletteAnchors {
  bg: string       // page background
  surface: string  // cards, sidebar, header
  elevated: string // hover, chips, secondary surfaces
  border: string   // dividers, input borders
  text: string     // primary text
  muted: string    // secondary / muted text
  accent: string   // buttons, links, active states
}

export interface Palette {
  id: string
  name: string
  mode: PaletteMode
  builtin?: boolean
  anchors: PaletteAnchors
}

export const ANCHOR_ROLES: { key: keyof PaletteAnchors; label: string; hint: string }[] = [
  { key: 'bg', label: 'Page background', hint: 'The furthest-back surface' },
  { key: 'surface', label: 'Surface', hint: 'Cards, sidebar, header' },
  { key: 'elevated', label: 'Elevated / hover', hint: 'Hover states, chips' },
  { key: 'border', label: 'Border', hint: 'Dividers and input borders' },
  { key: 'text', label: 'Text', hint: 'Primary text' },
  { key: 'muted', label: 'Muted text', hint: 'Secondary / subdued text' },
  { key: 'accent', label: 'Accent', hint: 'Buttons, links, active states' },
]

// Built-ins, defaults, anchor order and limits are GENERATED from
// internal/appearance/palettes.go — Go is the source of
// truth so the MCP theme tools can read the same anchors server-side. Edit the
// Go file and run `make types`; never edit the generated file.
//
// Re-exported here so every existing importer of this module keeps working.
export {
  BUILTIN_PALETTES,
  DEFAULT_LIGHT_ID,
  DEFAULT_DARK_ID,
  ANCHOR_KEYS,
  ANCHOR_LABELS,
  MAX_CUSTOM_PALETTES,
  MAX_PALETTE_NAME_LEN,
} from './builtin-palettes.generated'

import {
  BUILTIN_PALETTES as BUILTINS,
  DEFAULT_DARK_ID as DARK_ID,
  DEFAULT_LIGHT_ID as LIGHT_ID,
} from './builtin-palettes.generated'

// --- color math (hex only; no deps) ---

function hexToRgb(hex: string): [number, number, number] {
  const h = hex.replace('#', '')
  return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16)]
}

function rgbToHex(r: number, g: number, b: number): string {
  const c = (n: number) => Math.max(0, Math.min(255, Math.round(n))).toString(16).padStart(2, '0')
  return `#${c(r)}${c(g)}${c(b)}`
}

/** Linear interpolation between two hex colors (t in [0,1]). */
function lerpHex(a: string, b: string, t: number): string {
  const [ar, ag, ab] = hexToRgb(a)
  const [br, bg, bb] = hexToRgb(b)
  return rgbToHex(ar + (br - ar) * t, ag + (bg - ag) * t, ab + (bb - ab) * t)
}

/** Relative luminance (WCAG-ish, sRGB) — used to pick readable on-accent text. */
function luminance(hex: string): number {
  const [r, g, b] = hexToRgb(hex).map(v => {
    const s = v / 255
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4)
  })
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

/** Readable text color to sit on top of `bg`. */
function onColor(bg: string): string {
  return luminance(bg) > 0.5 ? '#0a0a0a' : '#ffffff'
}

const GRAY_STEPS = [50, 100, 200, 300, 400, 500, 600, 700, 800, 900, 950]

/** Build a full 50–950 gray ramp by interpolating between the given anchor
 *  stops (each `[step, hex]`). Steps between two anchors are lerped by their
 *  numeric distance; the app's key steps snap exactly to the anchors. */
function buildRamp(stops: [number, string][]): Record<number, string> {
  const sorted = [...stops].sort((a, b) => a[0] - b[0])
  const out: Record<number, string> = {}
  for (const step of GRAY_STEPS) {
    const exact = sorted.find(s => s[0] === step)
    if (exact) { out[step] = exact[1]; continue }
    let lower = sorted[0]
    let upper = sorted[sorted.length - 1]
    for (const s of sorted) if (s[0] <= step) lower = s
    for (let i = sorted.length - 1; i >= 0; i--) if (sorted[i][0] >= step) upper = sorted[i]
    if (lower[0] === upper[0]) { out[step] = lower[1]; continue }
    const t = (step - lower[0]) / (upper[0] - lower[0])
    out[step] = lerpHex(lower[1], upper[1], t)
  }
  return out
}

/** All CSS variables a palette sets. Keys are identical for every palette, so
 *  applying a new one cleanly overwrites the previous. */
export function buildTokens(p: Palette): Record<string, string> {
  const a = p.anchors
  // Dark ramps run bg(dark)→text(light) at 950→50; light ramps run the other
  // way, with the app's light surfaces/borders living at the low end.
  const stops: [number, string][] = p.mode === 'dark'
    ? [[950, a.bg], [900, a.surface], [800, a.elevated], [700, a.border], [500, a.muted], [50, a.text]]
    : [[50, a.surface], [100, a.elevated], [200, a.border], [500, a.muted], [900, a.text], [950, a.text]]
  const ramp = buildRamp(stops)

  const tokens: Record<string, string> = {}
  for (const step of GRAY_STEPS) tokens[`--color-gray-${step}`] = ramp[step]

  const onAccent = onColor(a.accent)
  Object.assign(tokens, {
    '--background': a.bg,
    '--foreground': a.text,
    '--card': a.surface,
    '--card-foreground': a.text,
    '--popover': a.surface,
    '--popover-foreground': a.text,
    '--primary': a.accent,
    '--primary-foreground': onAccent,
    '--secondary': a.elevated,
    '--secondary-foreground': a.text,
    '--muted': a.elevated,
    '--muted-foreground': a.muted,
    '--accent': a.elevated,
    '--accent-foreground': a.text,
    '--border': a.border,
    '--input': a.border,
    '--ring': a.accent,
    '--sidebar': a.surface,
    '--sidebar-foreground': a.text,
    '--sidebar-primary': a.accent,
    '--sidebar-primary-foreground': onAccent,
    '--sidebar-accent': a.elevated,
    '--sidebar-accent-foreground': a.text,
    '--sidebar-border': a.border,
    '--sidebar-ring': a.accent,
  })
  return tokens
}

/** Write a palette's tokens onto <html> as inline CSS variables. */
export function applyPalette(p: Palette): void {
  const el = document.documentElement
  const tokens = buildTokens(p)
  for (const [k, v] of Object.entries(tokens)) el.style.setProperty(k, v)
  cacheTokens(p.mode, tokens)
}

/** Resolve a palette id (built-in or custom) for a mode, falling back to the
 *  mode's default when the id is missing or belongs to the other mode. */
export function resolvePalette(id: string, mode: PaletteMode, customs: Palette[]): Palette {
  const all = [...BUILTINS, ...customs]
  const found = all.find(p => p.id === id && p.mode === mode)
  if (found) return found
  return BUILTINS.find(p => p.id === (mode === 'dark' ? DARK_ID : LIGHT_ID))!
}

export const palettesForMode = (mode: PaletteMode, customs: Palette[]): Palette[] =>
  [...BUILTINS, ...customs].filter(p => p.mode === mode)

// --- FOUC-avoiding cache ---
// PaletteProvider mirrors the server response here so a reload can apply the
// right palette synchronously (before React renders), avoiding a color flash.

const CACHE_KEY = 'openloc-appearance'
const THEME_KEY = 'openloc-theme' // next-themes storageKey (see main.tsx)
// The last applied tokens per mode. The inline script in index.html reads
// them, with THEME_KEY, to color the page before any app code has loaded.
export const TOKENS_KEY = 'openloc-palette-tokens'

function cacheTokens(mode: PaletteMode, tokens: Record<string, string>): void {
  try {
    const raw = localStorage.getItem(TOKENS_KEY)
    const all = raw ? JSON.parse(raw) : {}
    localStorage.setItem(TOKENS_KEY, JSON.stringify({ ...all, [mode]: tokens }))
  } catch { /* storage blocked or full */ }
}

export interface AppearanceCache {
  light_palette: string
  dark_palette: string
  custom_palettes: Palette[]
}

export function cacheAppearance(a: AppearanceCache): void {
  try { localStorage.setItem(CACHE_KEY, JSON.stringify(a)) } catch { /* ignore */ }
}

function resolvedMode(): PaletteMode {
  let mode: string | null = null
  try { mode = localStorage.getItem(THEME_KEY) } catch { /* ignore */ }
  if (mode === 'light' || mode === 'dark') return mode
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

/** The appearance PaletteProvider last saved in this browser, if any. */
export function readCachedAppearance(): AppearanceCache | null {
  try {
    const raw = localStorage.getItem(CACHE_KEY)
    return raw ? JSON.parse(raw) : null
  } catch {
    return null
  }
}

/** Apply the cached (or default) palette immediately on boot. Call before the
 *  React tree renders. */
export function applyCachedPalette(): void {
  const cache = readCachedAppearance()
  const mode = resolvedMode()
  const customs = cache?.custom_palettes ?? []
  const id = mode === 'dark'
    ? cache?.dark_palette ?? DARK_ID
    : cache?.light_palette ?? LIGHT_ID
  applyPalette(resolvePalette(id, mode, customs))
}
