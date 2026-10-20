// Anti-tautology: drop `typedOk` from submit's disabled prop → typed test
// fails. Remove the open-effect reset → "reopen resets" test fails.
//
// Click-bubble tests use `userEvent` (matches ConfirmDialog.test.tsx) —
// `fireEvent.click` dispatches a single synthetic event that skips the
// pointerdown/mousedown/focus/mouseup sequence base-ui's content + form
// elements respond to. The bubble bugs only surface under realistic
// input sequences.

import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, act, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import ClearTranslationsDialog from './ClearTranslationsDialog'

afterEach(() => cleanup())

function setup(props: Partial<React.ComponentProps<typeof ClearTranslationsDialog>> = {}) {
  const onOpenChange = vi.fn()
  const onConfirm = vi.fn()
  const utils = render(
    <ClearTranslationsDialog
      open
      onOpenChange={onOpenChange}
      title="Clear translations?"
      description="…"
      onConfirm={onConfirm}
      {...props}
    />,
  )
  return { ...utils, onOpenChange, onConfirm }
}

describe('ClearTranslationsDialog', () => {
  it('defaults scope to non_confirmed', () => {
    setup()
    expect((screen.getByTestId('clear-scope-non-confirmed') as HTMLInputElement).checked).toBe(true)
    expect((screen.getByTestId('clear-scope-all') as HTMLInputElement).checked).toBe(false)
  })

  it('confirms with selected scope and closes', () => {
    const { onConfirm, onOpenChange } = setup()
    fireEvent.click(screen.getByTestId('clear-scope-all'))
    fireEvent.click(screen.getByTestId('clear-submit'))
    expect(onConfirm).toHaveBeenCalledExactlyOnceWith('all')
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('without requireTypedConfirm the submit is enabled immediately', () => {
    setup()
    expect(screen.getByTestId('clear-submit')).not.toBeDisabled()
  })

  it('with requireTypedConfirm the submit is disabled until "CLEAR" is typed', () => {
    const { onConfirm } = setup({ requireTypedConfirm: true })
    const submit = screen.getByTestId('clear-submit')
    expect(submit).toBeDisabled()

    const input = screen.getByTestId('clear-typed-confirm') as HTMLInputElement
    fireEvent.change(input, { target: { value: 'clear' } }) // wrong case
    expect(submit).toBeDisabled()

    fireEvent.change(input, { target: { value: 'CLEAR' } })
    expect(submit).not.toBeDisabled()

    fireEvent.click(submit)
    expect(onConfirm).toHaveBeenCalledExactlyOnceWith('non_confirmed')
  })

  it('renders the exact count that follows the selected scope', () => {
    setup({ counts: { non_confirmed: 5, all: 12 } })
    expect(screen.getByTestId('clear-count-estimate')).toHaveTextContent(/^5 segments will be cleared\.$/)
    fireEvent.click(screen.getByTestId('clear-scope-all'))
    expect(screen.getByTestId('clear-count-estimate')).toHaveTextContent(/^12 segments will be cleared\.$/)
  })

  it('singularises the count at 1', () => {
    setup({ counts: { non_confirmed: 1, all: 1 } })
    expect(screen.getByTestId('clear-count-estimate')).toHaveTextContent(/^1 segment will be cleared\.$/)
  })

  it('omits the count estimate when counts prop is absent', () => {
    setup()
    expect(screen.queryByTestId('clear-count-estimate')).toBeNull()
  })

  it('reopening resets scope to non_confirmed and clears typed text', () => {
    const { rerender } = render(
      <ClearTranslationsDialog
        open
        onOpenChange={() => {}}
        title="t"
        description="d"
        requireTypedConfirm
        onConfirm={() => {}}
      />,
    )
    // First open: user types CLEAR + flips scope to all
    fireEvent.click(screen.getByTestId('clear-scope-all'))
    fireEvent.change(screen.getByTestId('clear-typed-confirm'), { target: { value: 'CLEAR' } })
    expect(screen.getByTestId('clear-submit')).not.toBeDisabled()

    // Close + reopen
    act(() => {
      rerender(
        <ClearTranslationsDialog
          open={false}
          onOpenChange={() => {}}
          title="t"
          description="d"
          requireTypedConfirm
          onConfirm={() => {}}
        />,
      )
    })
    act(() => {
      rerender(
        <ClearTranslationsDialog
          open
          onOpenChange={() => {}}
          title="t"
          description="d"
          requireTypedConfirm
          onConfirm={() => {}}
        />,
      )
    })

    expect((screen.getByTestId('clear-scope-non-confirmed') as HTMLInputElement).checked).toBe(true)
    expect((screen.getByTestId('clear-typed-confirm') as HTMLInputElement).value).toBe('')
    expect(screen.getByTestId('clear-submit')).toBeDisabled()
  })
})

// ────────────────────────────────────────────────────────────────────
// Adversarial bubble tests — bug from 2026-05-25 QA.
// Project page job list wraps each row in <Link>. The Clear dialog
// renders inside JobPipelineActions inside that Link. AlertDialog
// portals to body in the DOM but React synthetic events traverse the
// COMPONENT tree, so a click inside the dialog hits the Link's onClick
// and the user is navigated to the job instead of completing the
// dialog action.
//
// Every interactive surface inside the dialog must stop propagation.
// We test each one — they're all separate code paths in React's event
// delegation and a fix that only covers some surfaces would re-leak.
// ────────────────────────────────────────────────────────────────────

describe('ClearTranslationsDialog — no clicks bubble to a parent component', () => {
  async function setupWithParent(props: Partial<React.ComponentProps<typeof ClearTranslationsDialog>> = {}) {
    const parentClick = vi.fn()
    const onConfirm = vi.fn()
    const onOpenChange = vi.fn()
    render(
      <div onClick={parentClick} data-testid="parent">
        <ClearTranslationsDialog
          open
          onOpenChange={onOpenChange}
          title="Clear?"
          description="d"
          onConfirm={onConfirm}
          {...props}
        />
      </div>,
    )
    const user = userEvent.setup()
    // The dialog renders via portal — give it a tick to mount before
    // interacting.
    await screen.findByTestId('clear-submit')
    return { parentClick, onConfirm, onOpenChange, user }
  }

  it('radio click does NOT bubble to parent onClick', async () => {
    const { parentClick, user } = await setupWithParent()
    await user.click(screen.getByTestId('clear-scope-all'))
    expect(parentClick, 'project-page job row would navigate to the job').not.toHaveBeenCalled()
  })

  it('radio LABEL click does NOT bubble to parent onClick', async () => {
    // <label> wraps each radio — clicking the label propagates to the
    // wrapped input AND fires its own click event in the DOM. Both
    // surfaces need stopPropagation.
    const { parentClick, user } = await setupWithParent()
    await user.click(screen.getByText('Non-confirmed only'))
    expect(parentClick).not.toHaveBeenCalled()
  })

  it('submit click does NOT bubble to parent onClick', async () => {
    const { parentClick, onConfirm, user } = await setupWithParent()
    await user.click(screen.getByTestId('clear-submit'))
    expect(onConfirm, 'sanity: submit still fires the action').toHaveBeenCalledTimes(1)
    expect(parentClick, 'project-page submit would also navigate the row').not.toHaveBeenCalled()
  })

  it('cancel click does NOT bubble to parent onClick', async () => {
    const { parentClick, onConfirm, user } = await setupWithParent()
    await user.click(screen.getByRole('button', { name: /cancel/i }))
    expect(onConfirm).not.toHaveBeenCalled()
    expect(parentClick).not.toHaveBeenCalled()
  })

  it('typed-confirm input click + keystroke do NOT bubble to parent onClick', async () => {
    const { parentClick, user } = await setupWithParent({ requireTypedConfirm: true })
    const input = screen.getByTestId('clear-typed-confirm')
    await user.click(input)
    await user.type(input, 'CLEAR')
    expect(parentClick).not.toHaveBeenCalled()
  })

  it('title + description text clicks do NOT bubble to parent onClick', async () => {
    // Defensive: even non-interactive text inside the dialog must not
    // leak clicks. If a user clicks on the heading by accident the row
    // shouldn't navigate.
    const { parentClick, user } = await setupWithParent()
    await user.click(screen.getByText('Clear?'))
    await user.click(screen.getByText('d'))
    expect(parentClick).not.toHaveBeenCalled()
  })

})
