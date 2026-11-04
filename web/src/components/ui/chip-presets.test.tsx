// Preset chips: the promise is that a named thing looks identical everywhere.
// These tests pin the two halves of that — the preset table's assignments, and
// that each exported component actually renders its own entry.
import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  AIStepChip,
  HumanStepChip,
  HybridStepChip,
  MemberDisabledChip,
  OverflowChip,
  STEP_TYPE_CHIPS,
  STEP_TYPE_PRESETS,
  type StepTypePresetKey,
} from './chip-presets'
import { CHIP_PRESET_COLORS } from './chip'
import { TooltipProvider } from './tooltip'

afterEach(cleanup)

describe('step type presets', () => {
  // The assignments themselves. A preset silently changing color is the exact
  // drift the preset mechanism exists to prevent, so the values are pinned.
  it.each([
    ['human', 'grey'],
    ['hybrid', 'violet'],
    ['ai', 'indigo'],
  ] as const)('%s is %s', (key, color) => {
    expect(STEP_TYPE_PRESETS[key].color).toBe(color)
  })

  it('gives every step type a distinct color, so the three never collide', () => {
    const colors = Object.values(STEP_TYPE_PRESETS).map(p => p.color)
    expect(new Set(colors).size).toBe(colors.length)
  })

  // Hue alone must never be the only signal — an icon and a tooltip carry the
  // meaning for anyone who cannot rely on color.
  it('gives every step type an icon and a tooltip', () => {
    for (const preset of Object.values(STEP_TYPE_PRESETS)) {
      expect(preset.Icon).toBeTruthy()
      expect(preset.title.length).toBeGreaterThan(0)
    }
  })

  it('spends the reserved hues on the two step types a PM scans for', () => {
    expect(CHIP_PRESET_COLORS).toContain(STEP_TYPE_PRESETS.hybrid.color)
    expect(CHIP_PRESET_COLORS).toContain(STEP_TYPE_PRESETS.ai.color)
  })
})

describe('preset chip components', () => {
  const CASES: [string, StepTypePresetKey, typeof HumanStepChip, string][] = [
    ['HumanStepChip', 'human', HumanStepChip, 'bg-gray-100'],
    ['HybridStepChip', 'hybrid', HybridStepChip, 'bg-violet-100'],
    ['AIStepChip', 'ai', AIStepChip, 'bg-indigo-100'],
  ]

  it.each(CASES)('%s renders its label, tooltip and color', (_name, key, Component, bg) => {
    render(<Component />)
    const el = screen.getByTestId(`step-type-${key}`)
    expect(el.textContent).toBe(STEP_TYPE_PRESETS[key].label)
    expect(el).toHaveAttribute('title', STEP_TYPE_PRESETS[key].title)
    expect(el.className).toContain(bg)
  })

  it.each(CASES)('%s drops the label but keeps the icon when iconOnly', (_name, key, Component) => {
    render(<Component iconOnly />)
    const el = screen.getByTestId(`step-type-${key}`)
    expect(el.textContent).toBe('')
    // The icon stays, hidden from screen readers — the tooltip carries meaning.
    expect(el.querySelector('[aria-hidden="true"] svg')).not.toBeNull()
  })

  // A preset is a marker beside content; Chip's base is font-medium, so each
  // preset must undo it or the whole row reads as emphasis. The negative half
  // is the real assertion: `font-normal` being present proves nothing if
  // `font-medium` ships alongside it, which is what a broken class merge (or a
  // flipped `cn` argument order in chip.tsx) would produce.
  it.each(CASES)('%s renders font-normal, not the base font-medium', (_name, key, Component) => {
    render(<Component />)
    const cls = screen.getByTestId(`step-type-${key}`).className
    expect(cls).toContain('font-normal')
    expect(cls).not.toContain('font-medium')
  })

  // The dispatch table the step-type call sites index. Asserting the components
  // are merely truthy proves nothing — the map is typed
  // `Record<StepTypePresetKey, …>`, so a missing key is a compile error. What
  // is NOT compile-checked is the key→component WIRING: `hybrid: HumanStepChip`
  // typechecks fine, and every other test here imports the components directly,
  // so only rendering through the map catches it.
  it.each([
    ['human', 'bg-gray-100'],
    ['hybrid', 'bg-violet-100'],
    ['ai', 'bg-indigo-100'],
  ] as const)('STEP_TYPE_CHIPS.%s renders the %s preset', (key, bg) => {
    const Component = STEP_TYPE_CHIPS[key]
    const { unmount } = render(<Component />)
    const el = screen.getByTestId(`step-type-${key}`)
    expect(el.className).toContain(bg)
    expect(el.textContent).toBe(STEP_TYPE_PRESETS[key].label)
    unmount()
  })

  it('renders the Disabled member chip at the slim size, beside a row\'s other chips', () => {
    render(<MemberDisabledChip />)
    const el = screen.getByTestId('member-disabled')
    // The small size's padding and text, not the default's.
    expect(el).toHaveClass('px-1', 'py-0', 'text-[10px]')
    expect(el).not.toHaveClass('px-2', 'py-0.5', 'text-xs')
    expect(el).toHaveTextContent('Disabled')
  })
})

describe('OverflowChip', () => {
  it('renders the count with a leading plus', () => {
    render(<OverflowChip count={3} />)
    expect(screen.getByText('+3')).toBeInTheDocument()
  })

  // Borderless and tight is the whole point: it sits against the codes it
  // summarises instead of competing with them. Three lists render it and all
  // three must match, which is why it is a preset and not three call sites.
  it('is borderless and tight, whatever the caller adds', () => {
    render(<OverflowChip count={7} className="ml-1" />)
    const el = screen.getByText('+7')
    expect(el.className).toContain('ring-0')
    // Chip's base is `ring-1`; `ring-0` only wins if the merge collapses it.
    expect(el.className).not.toContain('ring-1')
    expect(el.className).toContain('px-1.5')
    expect(el.className).toContain('ml-1')
  })

  it('shows a tooltip on hover, and takes keyboard focus, only when the caller supplies one', async () => {
    const user = userEvent.setup()
    // The app's provider opens at once by default; Base UI alone waits 600 ms.
    const { unmount } = render(<TooltipProvider><OverflowChip count={2} tooltip="es, it" /></TooltipProvider>)
    const chip = screen.getByText('+2')
    expect(chip).toHaveAttribute('tabindex', '0')
    expect(chip).not.toHaveAttribute('title')
    await user.hover(chip)
    await waitFor(() => {
      expect(document.querySelector('[data-slot="tooltip-content"]')).toHaveTextContent('es, it')
    })
    unmount()
    render(<OverflowChip count={2} />)
    expect(screen.getByText('+2')).not.toHaveAttribute('tabindex')
  })
})
