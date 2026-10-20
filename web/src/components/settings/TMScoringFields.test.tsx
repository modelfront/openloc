import { describe, it, expect, vi, afterEach } from 'vitest'
import type { ReactNode } from 'react'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { TMScoringFields } from './TMScoringFields'
import { DRAFT_SAVE_DELAY_MS } from '@/hooks/useProjectDraft'

vi.mock('@/components/ui/tooltip', () => ({
  Tooltip: ({ children }: { children?: ReactNode }) => children,
  TooltipTrigger: ({ children }: { children?: ReactNode }) => <span>{children}</span>,
  TooltipContent: ({ children }: { children?: ReactNode }) => children,
  TooltipProvider: ({ children }: { children?: ReactNode }) => children,
}))

const values = { fuzzyThreshold: 0.75, sublanguagePenaltyPct: 1, nearExactPenaltyPct: 2 }

describe('TMScoringFields', () => {
  it('shows the threshold as a percentage and the penalties as stored', () => {
    render(<TMScoringFields values={values} isCreate={false} onCommit={vi.fn()} />)
    expect(screen.getByLabelText('Fuzzy match threshold')).toHaveValue(75)
    expect(screen.getByLabelText('Language-variant mismatch penalty')).toHaveValue(1)
    expect(screen.getByLabelText('Near-exact penalty')).toHaveValue(2)
  })

  it('edit mode commits on blur, as a 0–1 fraction for the threshold', () => {
    const onCommit = vi.fn()
    render(<TMScoringFields values={values} isCreate={false} onCommit={onCommit} />)
    const input = screen.getByLabelText('Fuzzy match threshold')
    fireEvent.change(input, { target: { value: '60' } })
    expect(onCommit).not.toHaveBeenCalled()
    fireEvent.blur(input, { target: { value: '60' } })
    expect(onCommit).toHaveBeenCalledWith({ fuzzy_threshold: 0.6 })
  })

  it('create mode commits per keystroke', () => {
    const onCommit = vi.fn()
    render(<TMScoringFields values={values} isCreate onCommit={onCommit} />)
    fireEvent.change(screen.getByLabelText('Near-exact penalty'), { target: { value: '5' } })
    expect(onCommit).toHaveBeenCalledWith({ near_exact_penalty_pct: 5 })
  })

  it('drops out-of-range values instead of committing them', () => {
    const onCommit = vi.fn()
    render(<TMScoringFields values={values} isCreate={false} onCommit={onCommit} />)
    fireEvent.blur(screen.getByLabelText('Language-variant mismatch penalty'), { target: { value: '150' } })
    fireEvent.blur(screen.getByLabelText('Fuzzy match threshold'), { target: { value: '-1' } })
    expect(onCommit).not.toHaveBeenCalled()
  })

  it('readOnly locks all three inputs and marks the box', () => {
    render(<TMScoringFields values={values} isCreate={false} readOnly onCommit={vi.fn()} />)
    expect(screen.getByTestId('tm-scoring')).toHaveAttribute('data-readonly', 'true')
    for (const label of ['Fuzzy match threshold', 'Language-variant mismatch penalty', 'Near-exact penalty']) {
      expect(screen.getByLabelText(label)).toBeDisabled()
    }
  })

  it('re-syncs the drafts when the stored values change', () => {
    const { rerender } = render(<TMScoringFields values={values} isCreate={false} onCommit={vi.fn()} />)
    rerender(<TMScoringFields values={{ ...values, fuzzyThreshold: 0.5 }} isCreate={false} onCommit={vi.fn()} />)
    expect(screen.getByLabelText('Fuzzy match threshold')).toHaveValue(50)
  })
})

describe('TMScoringFields — emptying a field', () => {
  it('edit mode: the field can be empty while typing; blur without a value snaps back to the stored one and commits nothing', () => {
    const onCommit = vi.fn()
    render(<TMScoringFields values={values} isCreate={false} onCommit={onCommit} />)
    const input = screen.getByLabelText('Sublanguage penalty'.replace('Sublanguage', 'Language-variant mismatch'))
    fireEvent.change(input, { target: { value: '' } })
    expect((input as HTMLInputElement).value).toBe('') // empty, not 0
    fireEvent.blur(input)
    expect(onCommit).not.toHaveBeenCalled()
    expect(input).toHaveValue(1)
  })

  it('create mode: emptying commits nothing and blur restores the value', () => {
    const onCommit = vi.fn()
    render(<TMScoringFields values={values} isCreate onCommit={onCommit} />)
    const input = screen.getByLabelText('Fuzzy match threshold')
    fireEvent.change(input, { target: { value: '' } })
    expect(onCommit).not.toHaveBeenCalled()
    expect((input as HTMLInputElement).value).toBe('')
    fireEvent.blur(input)
    expect(input).toHaveValue(75)
  })
})

describe('TMScoringFields — saving while typing (edit mode)', () => {
  afterEach(() => { vi.useRealTimers() })

  it('saves after a pause while the field keeps focus, and blur adds no second save', () => {
    vi.useFakeTimers()
    const onCommit = vi.fn()
    render(<TMScoringFields values={values} isCreate={false} onCommit={onCommit} />)
    const input = screen.getByLabelText('Language-variant mismatch penalty')
    act(() => input.focus())
    fireEvent.change(input, { target: { value: '4' } })
    act(() => { vi.advanceTimersByTime(DRAFT_SAVE_DELAY_MS) })
    expect(onCommit).toHaveBeenCalledExactlyOnceWith({ sublanguage_penalty_pct: 4 })

    fireEvent.blur(input)
    expect(onCommit).toHaveBeenCalledTimes(1)
  })

  it('a threshold typed as "65" saves 0.65 once, not 0.06 on the way', () => {
    vi.useFakeTimers()
    const onCommit = vi.fn()
    render(<TMScoringFields values={values} isCreate={false} onCommit={onCommit} />)
    const input = screen.getByLabelText('Fuzzy match threshold')
    fireEvent.change(input, { target: { value: '6' } })
    act(() => { vi.advanceTimersByTime(150) })
    fireEvent.change(input, { target: { value: '65' } })
    act(() => { vi.advanceTimersByTime(DRAFT_SAVE_DELAY_MS) })
    expect(onCommit.mock.calls).toEqual([[{ fuzzy_threshold: 0.65 }]])
  })
})
