import { describe, it, expect } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { Check } from 'lucide-react'
import { Chip, ChipButton, chipVariants, chipTextClass, CHIP_PRESET_COLORS, type ChipAnyColor, type ChipColor } from './chip'

const COLOR_CLASS: Record<ChipColor, string> = {
  grey: 'bg-gray-100',
  blue: 'bg-blue-100',
  yellow: 'bg-amber-100',
  green: 'bg-emerald-100',
  red: 'bg-rose-100',
}

// Every color the component renders — freeform plus the preset-only hues —
// mapped to the Tailwind palette word behind it.
const PALETTE_WORD: Record<ChipAnyColor, string> = {
  grey: 'gray',
  blue: 'blue',
  yellow: 'amber',
  green: 'emerald',
  red: 'rose',
  violet: 'violet',
  indigo: 'indigo',
}

describe('Chip', () => {
  it('renders its children', () => {
    render(<Chip color="blue">In progress</Chip>)
    expect(screen.getByText('In progress')).toBeInTheDocument()
  })

  // The whole point of the component: a color name maps to one palette, in one
  // place. A color silently falling through to grey is the regression that
  // makes every status pill look the same.
  it.each(Object.entries(COLOR_CLASS) as [ChipColor, string][])(
    'applies the %s palette',
    (color, bg) => {
      const { unmount } = render(<Chip color={color} data-testid="c">x</Chip>)
      expect(screen.getByTestId('c').className).toContain(bg)
      unmount()
    },
  )

  // The preset hues are deliberately NOT in ChipColor, so no status map can
  // hold one — only a named chip in chip-presets.tsx reaches them. That is a
  // TYPE invariant, invisible at runtime, so it is pinned by the compiler:
  // `tsc -b` covers test files, and this line fails the build if `Exclude`
  // ever stops excluding. A runtime assertion here could only compare the
  // test's own tables against each other.
  it('keeps the preset-only hues out of the freeform palette (type-level)', () => {
    // @ts-expect-error violet is preset-only and must not satisfy ChipColor
    const reserved: ChipColor = 'violet'
    expect(CHIP_PRESET_COLORS).toContain(reserved)
  })

  it('defaults to grey when no color is given', () => {
    render(<Chip data-testid="c">Created</Chip>)
    expect(screen.getByTestId('c').className).toContain('bg-gray-100')
  })

  it('renders the default size shape', () => {
    render(<Chip data-testid="c">x</Chip>)
    const cls = screen.getByTestId('c').className
    expect(cls).toContain('rounded-md')
    expect(cls).toContain('px-2')
    expect(cls).toContain('text-xs')
  })

  it('renders the large size shape', () => {
    render(<Chip size="large" data-testid="c">x</Chip>)
    const cls = screen.getByTestId('c').className
    expect(cls).toContain('px-2.5')
    expect(cls).toContain('text-sm')
    expect(cls).not.toContain('text-xs')
  })

  // A text annotation beside a chip must use the same palette as the chip, or
  // a palette change in one place leaves the other stranded. Both light and
  // dark shades, both directions, every color including the preset hues.
  it.each(Object.keys(PALETTE_WORD) as ChipAnyColor[])(
    'chipTextClass(%s) uses the same palette word as the chip, light and dark',
    (color) => {
      render(<Chip color={color} data-testid="c">x</Chip>)
      const word = PALETTE_WORD[color]
      const cls = screen.getByTestId('c').className
      expect(cls).toContain(`bg-${word}-100`)
      expect(cls).toContain(`dark:text-${word}-300`)
      expect(chipTextClass(color)).toContain(`text-${word}-700`)
      expect(chipTextClass(color)).toContain(`dark:text-${word}-300`)
    },
  )

  describe('icon', () => {
    it('renders the icon and hides it from screen readers', () => {
      render(
        <Chip color="green" icon={<Check data-testid="icon" />} data-testid="c">
          Approved
        </Chip>,
      )
      // Wrapped, not adjacent — a screen reader must announce "Approved", not
      // the icon's own accessible name.
      expect(screen.getByTestId('icon').closest('[aria-hidden="true"]')).not.toBeNull()
      expect(screen.getByTestId('c').textContent).toBe('Approved')
    })

    it('sizes lucide SVGs from the root so call sites do not', () => {
      render(<Chip icon={<Check />} data-testid="c">x</Chip>)
      // The [&_svg] variant must reach through the aria-hidden wrapper.
      // A `>` child selector here would silently not match, and every icon
      // would render at its 24px default.
      expect(screen.getByTestId('c').className).toContain('[&_svg]:size-3')
    })

    it('renders nothing extra when no icon is passed', () => {
      render(<Chip data-testid="c">x</Chip>)
      expect(screen.getByTestId('c').querySelector('[aria-hidden="true"]')).toBeNull()
    })
  })

  // Chips that need a decoration on top of a color — strikethrough for a stale
  // QE score, pulse for a running job — pass it as className rather than
  // earning a prop each. (Not a dashed edge: Chip's edge is a `ring`, and CSS
  // cannot dash a box-shadow.)
  it('merges an extra className without dropping the color', () => {
    render(
      <Chip color="red" className="animate-pulse line-through" data-testid="c">
        Failed
      </Chip>,
    )
    const cls = screen.getByTestId('c').className
    expect(cls).toContain('animate-pulse')
    expect(cls).toContain('line-through')
    expect(cls).toContain('bg-rose-100')
  })

  it('forwards arbitrary span props', () => {
    render(
      <Chip color="blue" title="tooltip" data-band="soon" data-testid="c">
        Due in 3d
      </Chip>,
    )
    const el = screen.getByTestId('c')
    expect(el).toHaveAttribute('title', 'tooltip')
    expect(el).toHaveAttribute('data-band', 'soon')
    expect(el).toHaveAttribute('data-slot', 'chip')
  })
})

describe('ChipButton', () => {
  it('is a plain button that looks like the chip of its color', () => {
    let clicks = 0
    render(<ChipButton color="red" onClick={() => clicks++}>Error 2</ChipButton>)
    const button = screen.getByRole('button', { name: 'Error 2' })
    // type=button, so a ChipButton inside a form never submits it.
    expect(button).toHaveAttribute('type', 'button')
    for (const cls of chipVariants({ color: 'red' }).split(' ')) expect(button).toHaveClass(cls)
    fireEvent.click(button)
    expect(clicks).toBe(1)
    // Not a toggle unless asked: no aria-pressed at all.
    expect(button).not.toHaveAttribute('aria-pressed')
  })

  it('pressed: a toggle that says whether it is on', () => {
    const { rerender } = render(<ChipButton color="red" pressed={false}>Error</ChipButton>)
    expect(screen.getByRole('button', { name: 'Error' })).toHaveAttribute('aria-pressed', 'false')
    rerender(<ChipButton color="red" pressed>Error</ChipButton>)
    expect(screen.getByRole('button', { name: 'Error', pressed: true })).toHaveClass('ring-2')
  })
})
