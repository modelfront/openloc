// The Chip ESLint rules, tested against the real eslint.config.js.
//
// Two things need pinning, and a rule with no test rots into either:
//   - it stops catching hand-rolled chips (the whole point), or
//   - it starts firing on banners, form inputs and hover tints, and someone
//     adds a blanket eslint-disable that then hides the real cases.
//
// The "must fire" cases are hand-rolled chip shapes; the "must not fire" cases
// are shapes that legitimately are not chips.

import { describe, it, expect, beforeAll } from 'vitest'
import { ESLint } from 'eslint'
import path from 'node:path'
import fs from 'node:fs'
import { CHIP_PRESET_COLORS, chipVariants } from './chip'

const RULE = 'no-restricted-syntax'
const webRoot = path.resolve(__dirname, '../../..')

let eslint: ESLint

// Constructing an ESLint is cheap; the first lintText is not — it loads the
// flat config and the TypeScript parser, several seconds on a loaded machine.
// Paid here rather than by whichever case happens to run first, which would
// blow that one case's 5s timeout while its 30 siblings run in milliseconds.
beforeAll(async () => {
  eslint = new ESLint({ cwd: webRoot })
  await eslint.lintText('export const Warm = () => null\n', {
    filePath: path.join(webRoot, 'src/__chip_rule_warmup.tsx'),
  })
}, 60_000)

// Which of the three rules fired, judged by its message. Asserting the kind
// (not just "something fired") is what makes each fixture prove the rule it
// is named for — a fixture aimed at the Badge rule that only trips the pill
// rule would otherwise pass silently.
type RuleKind = 'badge' | 'pill' | 'preset'

function ruleKind(message: string): RuleKind | null {
  if (message.startsWith('<Badge>')) return 'badge'
  if (message.startsWith('violet and indigo')) return 'preset'
  if (message.startsWith('Colored labels use')) return 'pill'
  // Some OTHER no-restricted-syntax rule shares this rule id — the raw-<button>
  // guard fires on fixtures that legitimately use a
  // <button>, and the two CAT save-helper guards exist too. Returning null keeps
  // them out of the chip verdict, so a must-not-fire fixture is not failed by an
  // unrelated rule.
  return null
}

async function chipViolations(source: string): Promise<RuleKind[]> {
  // A path under src/ so the config's `**/*.{ts,tsx}` block applies. Not
  // `*.test.tsx` — that block turns no-restricted-syntax off.
  const results = await eslint.lintText(`export const X = () => (<>${source}</>)\n`, {
    filePath: path.join(webRoot, 'src/__chip_rule_fixture.tsx'),
  })
  return results
    .flatMap(r => r.messages)
    .filter(m => m.ruleId === RULE)
    .map(m => ruleKind(m.message))
    .filter((k): k is RuleKind => k !== null)
}

describe('Chip lint rules — must fire', () => {
  const CASES: [string, RuleKind, string][] = [
    [
      'a Badge tinted with a color triple (the CAT grid repetition chip)',
      'badge',
      '<Badge variant="outline" className="bg-indigo-100 dark:bg-indigo-950 text-indigo-700 border-indigo-200">→R</Badge>',
    ],
    [
      // The colors here are string Literals inside the template's
      // expression, so this exercises the Badge rule's Literal selector.
      'a Badge tinted via a ternary inside a template literal (the CAT editor running badge)',
      'badge',
      '<Badge variant="outline" className={`animate-pulse ${cond ? "bg-indigo-50 text-indigo-600" : "bg-blue-50 text-blue-600"}`}>x</Badge>',
    ],
    [
      // The color lives in the template's quasi (raw text), which only the
      // TemplateElement selector can see.
      'a Badge tinted in the raw text of a template literal',
      'badge',
      '<Badge variant="outline" className={`bg-indigo-100 text-indigo-700 ${extra}`}>x</Badge>',
    ],
    [
      'a hand-rolled pill span (the org role badge)',
      'pill',
      '<span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-amber-100 text-amber-800 dark:bg-amber-900">owner</span>',
    ],
    [
      'a hand-rolled pill with rounded-full (the API token status chip)',
      'pill',
      '<span className="inline-flex text-xs px-2 py-0.5 rounded-full bg-green-100 dark:bg-green-900/30 text-green-700">Active</span>',
    ],
    [
      'a hand-rolled pill with the color first (the connector category chip)',
      'pill',
      '<span className="inline-flex rounded-md bg-gray-100 px-1.5 py-0.5 text-xs text-gray-600 dark:bg-gray-800">CMS</span>',
    ],
    [
      // `\b` cannot follow the `]` of a bracket size, so a regex written with
      // word boundaries lets every text-[10px] pill through.
      'a hand-rolled pill sized with a bracket value (the comment indicator)',
      'pill',
      '<span className="inline-flex items-center gap-0.5 rounded border px-1 py-px text-[10px] bg-blue-100 text-blue-700">3</span>',
    ],
    [
      'a hand-rolled pill whose background has an opacity modifier',
      'pill',
      '<span className="inline-flex rounded px-2 py-0.5 text-xs bg-amber-100/60 text-amber-800">warn</span>',
    ],
    [
      // Same four tokens in a template literal's raw text. The pill rule needs
      // a TemplateElement form as well as a Literal one to reach this.
      'a hand-rolled pill written as a template literal',
      'pill',
      '<span className={`inline-flex items-center rounded px-2 py-0.5 text-xs bg-amber-100 text-amber-800 ${extra}`}>warn</span>',
    ],
    [
      // The "+N" overflow markers inherit text-xs from their row, so the rule
      // must not require a size token — requiring one lets every marker through.
      'a hand-rolled pill that inherits its text size from the parent (the +N overflow marker)',
      'pill',
      '<span className="ml-1 inline-flex items-center px-1 py-0.5 rounded bg-gray-100 dark:bg-gray-800">+3</span>',
    ],
    // The preset hues are reserved for named chips in chip-presets.tsx. The
    // type keeps them out of every ChipColor map; this rule is the other door
    // — a literal at a call site.
    //
    // Derived from CHIP_PRESET_COLORS, not hardcoded: eslint.config.js
    // hand-mirrors that list as PRESET_ONLY_COLORS (a flat .js config cannot
    // import from src/), so adding a hue there and forgetting the config would
    // otherwise leave it usable freeform with the guards suite still green.
    ...CHIP_PRESET_COLORS.map(color => [
      `the reserved hue ${color} used freeform on a Chip`,
      'preset' as const,
      `<Chip color="${color}">x</Chip>`,
    ] as [string, RuleKind, string]),
    [
      // The selector matches a Literal DESCENDING from the attribute, not just
      // an attribute-value literal, so the braced form is caught too. Pinned
      // because narrowing it to `JSXAttribute > Literal` would still pass every
      // other case here.
      'a reserved hue in a braced expression rather than a bare attribute value',
      'preset',
      `<Chip color={'violet'}>x</Chip>`,
    ],
  ]

  it.each(CASES)('flags %s', async (_label, kind, source) => {
    expect(await chipViolations(source)).toContain(kind)
  })
})

// The eslint rules above only see ONE string literal at a time, so a chip
// whose shape and color live in separate literals — `cn('inline-flex rounded
// px-2', tint)`, or a color held in a named constant or a map — slips past
// them. This scan is the net under that hole for the case that matters most:
// the reserved hues must exist in chip.tsx and nowhere else, or the "only a
// named preset can reach violet/indigo" claim in the ADR is not true.
//
// Scoped to the chip shades (100/200). Solid -500 bars and -50 tinted cards
// are not chips and are none of this rule's business.

/** Every non-test source file under web/src, recursively. */
function sourceFiles(dir: string): string[] {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap(e => {
    const full = path.join(dir, e.name)
    if (e.isDirectory()) return sourceFiles(full)
    if (!/\.tsx?$/.test(e.name) || /\.test\.tsx?$/.test(e.name)) return []
    return [full]
  })
}

/** Source files whose contents match, as web-relative paths, sorted. */
function filesMatching(test: (src: string) => boolean): string[] {
  return sourceFiles(path.join(webRoot, 'src'))
    .filter(f => test(fs.readFileSync(f, 'utf8')))
    .map(f => path.relative(webRoot, f))
    .sort()
}

describe('reserved hues stay inside chip.tsx', () => {
  // Scan for the Tailwind palette words the presets actually RENDER, not their
  // prop names. The two coincide for violet/indigo, but the freeform five prove
  // they need not (yellow→amber, green→emerald, red→rose, grey→gray), so a
  // preset hue named in the design vocabulary would otherwise be scanned for a
  // class that exists nowhere — passing while pinning nothing. Reading them out
  // of chipVariants keeps the scan tied to the implementation.
  const PRESET_PALETTES = CHIP_PRESET_COLORS.map(color => {
    const palette = /bg-([a-z]+)-100/.exec(chipVariants({ color }))?.[1]
    if (!palette) throw new Error(`chip.tsx renders no bg-<palette>-100 for the "${color}" variant`)
    return palette
  })

  const RESERVED = new RegExp(
    `(?:bg|text|ring|border)-(?:${PRESET_PALETTES.join('|')})-(?:100|200)`,
  )

  it('finds them in chip.tsx only', () => {
    expect(filesMatching(src => RESERVED.test(src))).toEqual([
      'src/components/ui/chip.tsx',
    ])
  })
})

// --- Nobody re-invents the Chip -------------------------------------------
//
// The eslint rules above stop a colored label being hand-written AT A CALL
// SITE. They cannot see a component that declares its own `cva` or colour→
// classes map and is then used in forty places — the same duplication, one
// level up. So the two
// shapes a look-alike takes are pinned here by their definition.
//
// Both allowlists are exhaustive today. A new entry means someone grew a second
// colour vocabulary; the fix is `<Chip color=…>` or `chipVariants({ color })`.

const PALETTE =
  '(?:gray|slate|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald' +
  '|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)'

describe('nobody re-invents the Chip', () => {
  // Shape 1 — a component declaring its own `cva` with a colour-variant group.
  // These six are the legitimate variant components; a seventh needs a reason.
  it('defines cva() only in the known variant components', () => {
    expect(filesMatching(src => /\bcva\(/.test(src))).toEqual([
      'src/components/layout/PageContainer.tsx',
      'src/components/ui/badge.tsx',
      'src/components/ui/button.tsx',
      'src/components/ui/chip.tsx',
      'src/components/ui/input-group.tsx',
      'src/components/ui/tabs.tsx',
    ])
  })

  // Shape 2 — a status→classes table: three or more consecutive entries whose
  // values carry a palette background, whether the value IS the class string
  // or an object holding it. That is a second palette, wherever it lives. The
  // list is chip.tsx alone and must never grow.
  //
  // The shade is capped at the chip range. Solid `-400`/`-500` fills
  // (ProcessingTimeDialog's stage bars) and brand colours (ProviderIcon) are
  // not chips, and a rule that swept them up would have to allowlist them.
  const colourMapEntry = new RegExp(`^\\s*[\\w'"]+\\s*:.*\\bbg-${PALETTE}-(?:50|100|200)\\b`)
  const hasColourMap = (src: string) => {
    let run = 0
    for (const line of src.split('\n')) {
      run = colourMapEntry.test(line) ? run + 1 : 0
      if (run >= 3) return true
    }
    return false
  }

  it('defines a colour map only in chip.tsx', () => {
    expect(filesMatching(hasColourMap)).toEqual([
      'src/components/ui/chip.tsx',
    ])
  })

  // The allowlist above is only as good as the detector under it. A colour map
  // can hide its classes one level down, in an object per entry — `ProviderIcon`'s
  // `{ mark, className }` is the shape — and a detector that only read quoted
  // values would miss every one of them.
  it('detects a colour map whose entries nest the class inside an object', () => {
    expect(hasColourMap([
      "const TONES = {",
      "  done: { label: 'Done', className: 'bg-emerald-100 text-emerald-700' },",
      "  busy: { label: 'Busy', className: 'bg-amber-100 text-amber-700' },",
      "  dead: { label: 'Dead', className: 'bg-rose-100 text-rose-700' },",
      "}",
    ].join('\n'))).toBe(true)
  })

  it('leaves solid fills alone — a stage bar is not a chip', () => {
    expect(hasColourMap([
      "const GROUP_META = {",
      "  ingest: { label: 'Ingest', bar: 'bg-slate-400', dot: 'bg-slate-400' },",
      "  tm:     { label: 'TM',     bar: 'bg-emerald-500', dot: 'bg-emerald-500' },",
      "  mt:     { label: 'MT',     bar: 'bg-orange-500', dot: 'bg-orange-500' },",
      "}",
    ].join('\n'))).toBe(false)
  })

  // Shape 3 — a whole chip assembled inside one `cn()` call, the silhouette in
  // one argument and the tint in another. This is the hole the ESLint pill rule
  // cannot reach: it reads a single literal, and here no single literal carries
  // all four tokens. Scoped to ONE cn() call rather than the whole file, so an
  // unrelated tinted bubble elsewhere in the same file is not mistaken for a
  // chip — that precision is what lets this list be empty.
  const cnCall = /\bcn\(([\s\S]{0,700}?)\)\s*}/g
  const SILHOUETTE = new RegExp(
    '(?:inline-flex|inline-block)[^\'"`]*rounded[^\'"`]*px-[\\d.]+' +
    '|px-[\\d.]+[^\'"`]*rounded[^\'"`]*(?:inline-flex|inline-block)' +
    '|(?:inline-flex|inline-block)[^\'"`]*px-[\\d.]+[^\'"`]*rounded',
  )
  // Unprefixed only: `hover:bg-gray-100` is a hover state, not a chip fill.
  const TINT = new RegExp(`(?<![\\w:-])bg-${PALETTE}-(?:50|100|200)(?![\\w-])`)
  const buildsAChip = (src: string) => {
    for (const m of src.matchAll(cnCall)) {
      if (SILHOUETTE.test(m[1]) && TINT.test(m[1])) return true
    }
    return false
  }

  it('builds no chip by hand inside a cn() call', () => {
    expect(filesMatching(buildsAChip)).toEqual([])
  })

  it('detects a chip whose silhouette and tint sit in different cn() arguments', () => {
    expect(buildsAChip([
      "  <span className={cn(",
      "    'inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px]',",
      "    ok ? 'border-gray-200 bg-gray-50 text-gray-600' : 'border-red-200 bg-red-50 text-red-700',",
      "  )}>",
    ].join('\n'))).toBe(true)
  })

  it('leaves a hover tint alone — a menu row is not a chip', () => {
    expect(buildsAChip([
      "  <button className={cn(",
      "    'inline-flex h-7 items-center gap-1 rounded-md px-1.5 text-xs',",
      "    active ? 'bg-accent' : 'hover:bg-gray-100 dark:hover:bg-gray-800',",
      "  )}>",
    ].join('\n'))).toBe(false)
  })
})

describe('Chip lint rules — must NOT fire', () => {
  const CASES: [string, string][] = [
    [
      'a Badge with no palette classes (counts, language codes, inline actions)',
      '<Badge variant="secondary" className="text-sm font-medium">EN</Badge>',
    ],
    [
      'a Badge tinted with theme tokens rather than a palette',
      '<Badge variant="secondary" className="bg-primary/10 text-primary">3</Badge>',
    ],
    [
      'a block-level error banner',
      '<div className="mt-1 rounded bg-red-50 px-2 py-1 text-xs text-red-700 dark:bg-red-950/40">boom</div>',
    ],
    [
      'a small form input',
      '<input className="w-full px-1 py-0.5 border border-gray-200 dark:border-gray-700 rounded text-xs dark:bg-gray-900" />',
    ],
    [
      'a hover-only tint on a button',
      '<button className="inline-flex text-xs px-2 py-1 rounded hover:bg-gray-100 dark:hover:bg-gray-800">x</button>',
    ],
    [
      'a chip built from theme tokens rather than a palette',
      '<span className="inline-flex items-center rounded bg-primary/10 px-1.5 py-0.5 text-xs text-primary">es</span>',
    ],
    [
      // Tinted, inline, rounded — but wide padding: a tinted card/toolbar
      // section, not a pill.
      'an inline tinted panel with non-chip padding',
      '<span className="inline-flex items-center rounded-lg bg-gray-100 px-4 py-2 text-xs dark:bg-gray-800">Settings</span>',
    ],
    [
      // Only the reserved hues are restricted; the freeform five must stay
      // usable at any call site or the whole component is unusable.
      'a freeform color on a Chip',
      '<Chip color="blue">Approved</Chip>',
    ],
    [
      // The rule keys on the attribute NAME `color`, with no element
      // constraint — deliberately, so a preset hue cannot slip in on any
      // component. So this proves only that a hue-shaped word on a DIFFERENT
      // prop is ignored; `<Icon color="violet" />` would (correctly) fire.
      'a preset-hue-shaped word on a prop other than color',
      '<Icon stroke="violet" />',
    ],
    [
      // The value is anchored `^…$`, so a class string that merely contains a
      // hue name is not a bare hue.
      'a color prop holding a class string, not a bare hue',
      '<StatCard color="text-violet-600 dark:text-violet-400" />',
    ],
  ]

  it.each(CASES)('allows %s', async (_label, source) => {
    expect(await chipViolations(source)).toHaveLength(0)
  })
})
