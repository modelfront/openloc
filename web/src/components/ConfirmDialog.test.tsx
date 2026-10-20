import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import ConfirmDialog from './ConfirmDialog'

afterEach(() => cleanup())

describe('ConfirmDialog', () => {
  function setup(overrides: Partial<React.ComponentProps<typeof ConfirmDialog>> = {}) {
    const onOpenChange = vi.fn()
    const onConfirm = vi.fn()
    render(
      <ConfirmDialog
        open
        onOpenChange={onOpenChange}
        title="Are you sure?"
        description="This will overwrite your edits."
        actionLabel="Reload and discard"
        onConfirm={onConfirm}
        {...overrides}
      />,
    )
    return { onOpenChange, onConfirm }
  }

  it('renders title + description + action + cancel when open', async () => {
    setup()
    expect(await screen.findByText('Are you sure?')).toBeInTheDocument()
    expect(screen.getByText('This will overwrite your edits.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Reload and discard/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Cancel/i })).toBeInTheDocument()
  })

  it('clicking the action button calls onConfirm AND closes the dialog', async () => {
    const { onConfirm, onOpenChange } = setup()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /Reload and discard/i }))
    expect(onConfirm).toHaveBeenCalledTimes(1)
    // Base UI calls onOpenChange(open, eventDetails) — match on first arg only.
    expect(onOpenChange.mock.calls.some(c => c[0] === false)).toBe(true)
  })

  it('clicking Cancel closes the dialog WITHOUT firing onConfirm', async () => {
    const { onConfirm, onOpenChange } = setup()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /Cancel/i }))
    expect(onConfirm).not.toHaveBeenCalled()
    // Base UI calls onOpenChange(open, eventDetails) — match on first arg only.
    expect(onOpenChange.mock.calls.some(c => c[0] === false)).toBe(true)
  })

  // Pins that a backdrop click cancels and never fires onConfirm.
  // Base UI's AlertDialog does not dismiss on outside click by default, so
  // without the backdrop handler this click is a no-op.
  it('clicking the backdrop closes the dialog (cancel semantics) without firing onConfirm', async () => {
    const { onConfirm, onOpenChange } = setup()

    // Wait for the dialog to render
    await screen.findByText('Are you sure?')

    // Find the backdrop. Base UI renders the overlay with data-slot="alert-dialog-overlay".
    const overlay = document.querySelector('[data-slot="alert-dialog-overlay"]') as HTMLElement
    expect(overlay, 'precondition: backdrop must exist').toBeTruthy()

    // Click the backdrop (the user's "click outside" gesture).
    const user = userEvent.setup()
    await user.click(overlay)

    // This MUST close the dialog (onOpenChange(false)) and
    // MUST NOT fire onConfirm. If onConfirm fires, an outside click would
    // trigger a destructive action — strictly worse than the original bug.
    expect(onConfirm,
      'outside click must NEVER trigger the destructive action').not.toHaveBeenCalled()
    expect(onOpenChange.mock.calls.some(c => c[0] === false),
      'clicking the backdrop must close the dialog (cancel-on-outside)').toBe(true)
  })
})
