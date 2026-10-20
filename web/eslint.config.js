import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

// Tailwind palettes that carry a colour, and the light-tint steps a chip's
// background/ring uses. Deliberately excludes theme tokens (`bg-primary/10`,
// `text-muted-foreground`) — those are the right way to tint something.
const TW_PALETTE =
  'gray|slate|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose'
const TINT_STEP = '50|100|200'
// A colour class anywhere in the string, variant prefixes (`dark:`, `hover:`)
// included.
const ANY_COLOR_CLASS = `(bg|text|ring|border)-(${TW_PALETTE})-(${TINT_STEP})`
// The chip silhouette: inline box + rounded + chip-scale padding + an
// UNPREFIXED tint background. All four together, in any order. Requiring
// `inline-*` is what keeps banners, form inputs and key hints out: they are
// block-level. Requiring the background unprefixed keeps hover-only tints out.
// Text size is deliberately NOT required — a pill often inherits `text-xs`
// from its row, so a size token in the literal would let those through.
//
// Known gap: a bare `<span>` with no `inline-*` class is invisible to this
// rule (it can only be told from a banner by its size). Widening the rule to
// catch it would flag every tinted block-level element.
//
// Token ends are matched with `(?![\w-])`, not `\b`: `\b` needs a word
// character on one side, so it never fires after the `]` of `text-[10px]`.
// The negative lookahead also lets an opacity modifier through
// (`bg-amber-100/60`) while still rejecting `px-10` and `bg-gray-1000`.
// No literal `/` anywhere in these patterns — esquery reads the selector's
// `/…/` as a regex literal and an unescaped slash ends it early, which breaks
// the whole config (every rule, not just this one).
const END = '(?![\\w-])'
const PILL_SHAPE =
  `^(?=.*\\binline-(flex|block)${END})(?=.*\\brounded)(?=.*\\bpx-(0\\.5|1|1\\.5|2|2\\.5)${END})` +
  `(?=.*(^|\\s)bg-(${TW_PALETTE})-(${TINT_STEP})${END})`

// Where every message below sends the reader for the shared components.
const SEE_GALLERY = 'See the component gallery (/debug) and CONTRIBUTING.md (Frontend).'

const CHIP_MESSAGE =
  'Colored labels use <Chip color="grey|blue|yellow|green|red"> from @/components/ui/chip — do not hand-write the color classes. ' +
  'Need a new color or size? Extend the variant map in chip.tsx. For an interactive chip (a button that looks like one), ' +
  'apply chipVariants({ color }) instead of retyping the classes. ' + SEE_GALLERY

const BADGE_MESSAGE =
  '<Badge> is the THEME-TOKEN primitive (bg-primary / bg-secondary / bg-destructive), for counts and inline actions. A palette-colored label is a <Chip> (@/components/ui/chip). ' +
  SEE_GALLERY

// Hues reserved for NAMED preset chips (components/ui/chip-presets.tsx). Kept
// out of `ChipColor` by the type, and out of JSX by this rule: the two doors a
// call site could otherwise walk through. Mirrors CHIP_PRESET_COLORS in
// chip.tsx — add here when you add one there.
const PRESET_ONLY_COLORS = 'violet|indigo'

const PRESET_COLOR_MESSAGE =
  'violet and indigo are reserved for named preset chips. Do not pass them at a call site — add an entry to ' +
  'components/ui/chip-presets.tsx and export a component for it, so every instance of that thing looks the same. ' +
  'Freeform chips use grey|blue|yellow|green|red. ' + SEE_GALLERY

export default defineConfig([
  globalIgnores(['dist']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    rules: {
      // A hot-reload hint, not a code-quality rule: a file that exports a
      // component and a helper still works, it only reloads in full.
      'react-refresh/only-export-components': 'off',
      // A React Compiler notice: this app doesn't run the compiler, and
      // useReactTable has no compliant form.
      'react-hooks/incompatible-library': 'off',
      // `{ node, ...props }` drops a prop before spreading the rest.
      '@typescript-eslint/no-unused-vars': ['error', { ignoreRestSiblings: true }],
      // Every change of the CAT editor's active-segment index MUST
      // route through `switchActiveSegment(newIdx, opts)` so the previously-
      // active row's draft is flushed first. Direct `setActiveSegmentIdx(...)`
      // calls bypass the chokepoint and lose the in-progress edit on row click /
      // Tab / F&R navigate / Jump dialog.
      // The single legitimate call site is inside `switchActiveSegment` itself,
      // which carries an `eslint-disable-next-line` exemption.
      'no-restricted-syntax': [
        'error',
        {
          selector: "CallExpression[callee.name='setActiveSegmentIdx']",
          message: 'Call switchActiveSegment(newIdx, opts) instead. Direct setActiveSegmentIdx calls bypass the deactivate-flush chokepoint and lose user edits.',
        },
        {
          // Phase 17.2: every post-save sync goes through `applySegmentSaveSuccess`,
          // which advances baselineUpdatedAtRef + patches cache + syncs originalText
          // + clears dirtyRef in one place. Direct `advanceTranslationInCache(...)`
          // calls miss the other three invariants. The only legitimate call site
          // is inside `applySegmentSaveSuccess` itself; other writers must use the
          // helper or explicitly disable this rule with a comment + reason.
          selector: "CallExpression[callee.name='advanceTranslationInCache']",
          message: 'Use applySegmentSaveSuccess(...) for post-save sync (Phase 17.2). Direct advanceTranslationInCache calls miss baselineUpdatedAtRef advance, originalText sync, and dirtyRef clear — see web/src/lib/segment-save.ts.',
        },
        {
          // Every clickable action must use the shared <Button> (or its
          // customizations like <DeleteButton>) so variants, sizes, focus rings,
          // loading and disabled states stay consistent.
          // Genuinely non-action controls (toggles/segmented controls, tabs, chips,
          // whole-row/card targets, drag handles, popover/combobox triggers,
          // launchers) may keep a raw <button> with an eslint-disable-next-line +
          // reason on the line above.
          selector: "JSXOpeningElement[name.name='button']",
          message: 'Use <Button> / <DeleteButton> (components/ui) instead of a raw <button> for actions. ' + SEE_GALLERY + ' For a genuine non-action control (toggle, tab, chip, row target, trigger, launcher), keep the raw <button> and add an eslint-disable-next-line no-restricted-syntax with a reason.',
        },
        {
          // Every pick-from-a-list field is the shared <Combobox> / <MultiCombobox>
          // (components/ui/combobox.tsx) so the field shape, search, pinned actions
          // and chips stay consistent.
          selector: "JSXOpeningElement[name.name='select']",
          message: 'Use <Combobox> / <MultiCombobox> (components/ui/combobox.tsx) instead of a native <select>. ' + SEE_GALLERY,
        },
        {
          // Same rule as <button>, for text fields: the shared <Input>/<Textarea>
          // own the box, focus ring, disabled and aria-invalid states, the clear
          // X and click-to-focus — a hand-written border string drifts from all
          // of them. `<textarea>` has no exceptions beyond
          // the primitive itself.
          selector: "JSXOpeningElement[name.name='textarea']",
          message: 'Use <Textarea> (components/ui/textarea.tsx) instead of a raw <textarea>. ' + SEE_GALLERY,
        },
        {
          // Types no component models stay raw: checkbox, radio, file and
          // color. A date or time goes through DeadlinePicker. Everything else —
          // including an <input> with no type, which defaults to text — goes
          // through <Input>.
          selector:
            "JSXOpeningElement[name.name='input']:not(:has(JSXAttribute[name.name='type'][value.value=/^(checkbox|radio|file|color)$/]))",
          message: 'Use <Input> (components/ui/input.tsx) instead of a raw <input>. ' + SEE_GALLERY + ' Only checkbox, radio, file and color stay raw — a date or time uses <DeadlinePicker> / <DateTimePicker>; for anything else that genuinely cannot use <Input>, add an eslint-disable-next-line no-restricted-syntax with a reason.',
        },
        // --- Chip: one component for every colored label ------------------
        // Three shapes to catch. First, a <Badge> tinted at the call site.
        {
          selector: `JSXElement[openingElement.name.name='Badge'] JSXAttribute[name.name='className'] Literal[value=/${ANY_COLOR_CLASS}/]`,
          message: BADGE_MESSAGE,
        },
        {
          selector: `JSXElement[openingElement.name.name='Badge'] JSXAttribute[name.name='className'] TemplateElement[value.raw=/${ANY_COLOR_CLASS}/]`,
          message: BADGE_MESSAGE,
        },
        // Second, a pill hand-rolled out of Tailwind on a plain element — as a
        // string literal, and as a template literal's raw text (the same pair
        // the Badge rule above needs). Still a strong net, not a proof: the
        // four tokens must land in ONE literal, so a className that splits
        // them across interpolated fragments slips through either way.
        {
          selector: `JSXAttribute[name.name='className'] Literal[value=/${PILL_SHAPE}/]`,
          message: CHIP_MESSAGE,
        },
        {
          selector: `JSXAttribute[name.name='className'] TemplateElement[value.raw=/${PILL_SHAPE}/]`,
          message: CHIP_MESSAGE,
        },
        // Third, a preset-only hue used freeform. The type keeps these out of
        // every ChipColor map; this keeps them out of JSX.
        {
          selector: `JSXAttribute[name.name='color'] Literal[value=/^(${PRESET_ONLY_COLORS})$/]`,
          message: PRESET_COLOR_MESSAGE,
        },
      ],
    },
  },
  {
    // A picker is built once, in components/ui/combobox.tsx. Reaching for cmdk
    // or ui/command anywhere else is a hand-rolled picker. The docs command palette is the
    // one genuine non-picker use and carries an eslint-disable + reason.
    files: ['src/**/*.{ts,tsx}'],
    ignores: ['src/components/ui/**'],
    rules: {
      'no-restricted-imports': [
        'error',
        {
          paths: [
            { name: 'cmdk', message: 'Use <Combobox> / <MultiCombobox> (components/ui/combobox.tsx); cmdk is only wired up there.' },
            { name: '@/components/ui/command', message: 'Use <Combobox> / <MultiCombobox> (components/ui/combobox.tsx) instead of composing a picker from ui/command.' },
          ],
        },
      ],
    },
  },
  {
    // Test files exercise the helpers directly — they're the unit-test
    // surface for advanceTranslationInCache + applySegmentSaveSuccess
    // themselves. The lint rules above are for production callers; test doubles
    // may render a native <select> in place of the picker.
    files: ['**/*.test.{ts,tsx}', 'src/test/**'],
    rules: {
      'no-restricted-syntax': 'off',
    },
  },
])
