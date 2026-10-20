import { KeyRound, Languages, PowerOff, Sparkles, User, UserCog } from 'lucide-react'

import { cn } from '@/lib/utils'
import { Chip, type ChipAnyColor, type ChipProps } from './chip'

// Preset chips — named labels, not freeform ones.
//
// A freeform `<Chip color="yellow">` says "tint this text yellow" and the call
// site owns the choice. A preset says "this is the AI step marker", and every
// instance of it is identical: same color, same size, same icon, same tooltip.
// That is the point — a thing the product has a NAME for should not be
// re-decided per call site.
//
// Preset chips are the only chips allowed the reserved hues in chip.tsx's
// preset palette (violet, indigo). The trade is deliberate: you may have a
// sixth or seventh color, but only by naming the thing that wears it. A
// literal `color="violet"` in JSX is an ESLint error (web/eslint.config.js);
// the entries below set the hue as table data and pass it as a variable, so
// they never trip it.
//
// Adding a preset:
//   1. Add an entry to a `*_PRESETS` table below (color, label, icon, title).
//   2. Export a component for it, one line, via `presetChip`.
// The /debug gallery shows the step types as the worked example, not every
// preset — add a row there only for a preset whose shape they don't cover.
// A preset on a reserved hue needs its icon and title: hue must never be the
// only signal. One on the neutral grey with self-describing text does not —
// see OverflowChip.
// Do NOT reach for a preset when the label is genuinely per-call-site text —
// a count, a language code, a user-typed name. Those stay freeform Chips.

interface ChipPreset {
  /** Visible text. Omitted when the preset renders icon-only. */
  label: string
  color: ChipAnyColor
  Icon?: typeof User
  /** Tooltip. Carries the meaning the short label cannot. */
  title: string
}

/** Props a preset call site may still vary — never the color or the label. */
export type PresetChipProps = Pick<ChipProps, 'className'> & {
  /** Drop the text and render the icon alone, for tight rows. */
  iconOnly?: boolean
  'data-testid'?: string
}

/**
 * Build a component from a preset entry. Shared so every preset renders
 * through one code path and cannot drift in weight, icon sizing or tooltip
 * handling.
 */
function presetChip(preset: ChipPreset, defaultTestId: string) {
  return function PresetChip({ className, iconOnly, ...props }: PresetChipProps) {
    const { Icon } = preset
    return (
      <Chip
        color={preset.color}
        icon={Icon ? <Icon /> : undefined}
        title={preset.title}
        // font-normal because a preset is a marker beside content, not a
        // heading; Chip's base is font-medium.
        className={`font-normal${className ? ` ${className}` : ''}`}
        data-testid={props['data-testid'] ?? defaultTestId}
      >
        {!iconOnly && preset.label}
      </Chip>
    )
  }
}

// --- Workflow step types ------------------------------------------------
//
// Color answers "who has to act": a human step is ordinary work, a hybrid
// step has AI output waiting on a person, an AI step runs and completes
// itself. Hybrid and AI carry the reserved hues because they are the pair a
// PM scans for when deciding where their attention is needed — and the icons
// keep them apart for anyone who cannot rely on hue. Pre-translation is the
// built-in step of the Pre-translation workflow: machine translation
// fills the file in and the step completes itself.

export const STEP_TYPE_PRESETS = {
  human: {
    label: 'Human',
    color: 'grey',
    Icon: User,
    title: 'A linguist works this step',
  },
  hybrid: {
    label: 'Hybrid',
    color: 'violet',
    Icon: UserCog,
    title: 'AI runs when the step starts; a human reviews and completes it',
  },
  ai: {
    label: 'AI',
    color: 'indigo',
    Icon: Sparkles,
    title: 'AI runs when the step starts and the step completes itself',
  },
  pretranslate: {
    label: 'Pre-translation',
    color: 'blue',
    Icon: Languages,
    title: 'Machine translation fills in the file when the step starts, and the step completes itself',
  },
} as const satisfies Record<string, ChipPreset>

export type StepTypePresetKey = keyof typeof STEP_TYPE_PRESETS

export const HumanStepChip = presetChip(STEP_TYPE_PRESETS.human, 'step-type-human')
export const HybridStepChip = presetChip(STEP_TYPE_PRESETS.hybrid, 'step-type-hybrid')
export const AIStepChip = presetChip(STEP_TYPE_PRESETS.ai, 'step-type-ai')
export const PretranslateStepChip = presetChip(STEP_TYPE_PRESETS.pretranslate, 'step-type-pretranslate')

// --- Engine usability ---------------------------------
//
// An engine that cannot translate, wherever it appears: its own list, and every
// router that routes to it. Two causes, deliberately DIFFERENT colours —
// switched off is a choice somebody made and is undone by a switch, while a
// deleted key is breakage that needs the engine repointed. Telling a user
// "warning" without saying which of those it is leaves them nowhere to go.

export const ENGINE_UNUSABLE_PRESETS = {
  disabled: {
    label: 'Off',
    color: 'yellow',
    Icon: PowerOff,
    title: 'Switched off — it will not translate until you turn it back on',
  },
  no_key: {
    label: 'No key',
    color: 'red',
    Icon: KeyRound,
    title: 'The API key this engine used was deleted — choose another key for it',
  },
} as const satisfies Record<string, ChipPreset>

export const EngineDisabledChip = presetChip(ENGINE_UNUSABLE_PRESETS.disabled, 'engine-unusable-disabled')
export const EngineNoKeyChip = presetChip(ENGINE_UNUSABLE_PRESETS.no_key, 'engine-unusable-no_key')

// The same two states seen from a ROUTER, which needs its own wording: "No key"
// on a router row reads as the router lacking one, when the problem is an engine
// it routes to. Same hues, so the two lists still read as one system.
export const ROUTER_ENGINE_PRESETS = {
  disabled: {
    label: 'Engine off',
    color: 'yellow',
    Icon: PowerOff,
    title: 'This router sends a language to an engine that is switched off',
  },
  no_key: {
    label: 'Engine has no key',
    color: 'red',
    Icon: KeyRound,
    title: 'This router sends a language to an engine whose API key was deleted',
  },
} as const satisfies Record<string, ChipPreset>

export const RouterEngineOffChip = presetChip(ROUTER_ENGINE_PRESETS.disabled, 'router-engine-disabled')
export const RouterEngineNoKeyChip = presetChip(ROUTER_ENGINE_PRESETS.no_key, 'router-engine-no_key')

export const STEP_TYPE_CHIPS: Record<StepTypePresetKey, ReturnType<typeof presetChip>> = {
  human: HumanStepChip,
  hybrid: HybridStepChip,
  ai: AIStepChip,
  pretranslate: PretranslateStepChip,
}

// --- Overflow marker -------------------------------------------------------

/**
 * The "+N more" marker closing a truncated list. Borderless and tight so it
 * reads as a summary rather than another code.
 *
 * Not built through `presetChip` because its label is a count; everything
 * about how it looks still is.
 */
export function OverflowChip({
  count,
  title,
  size,
  className,
}: {
  count: number
  /** Spell out what is hidden, when the caller has it. */
  title?: string
  /** Match the chips around it; a dense cell uses `small`. */
  size?: ChipProps['size']
  className?: string
}) {
  return (
    <Chip size={size} className={cn('px-1.5 ring-0', className)} title={title}>
      +{count}
    </Chip>
  )
}
