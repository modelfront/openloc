// The CAT editor's Complete step dialog: what it hands to onConfirm is what
// the advance request sends as the hand-off note for the next step.
import { describe, it, expect, vi, afterEach } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

import CompleteStepDialog, { type CompleteStepDialogProps } from './CompleteStepDialog'

afterEach(cleanup)

function renderDialog(extra: Partial<CompleteStepDialogProps> = {}) {
  const props: CompleteStepDialogProps = {
    open: true,
    onOpenChange: vi.fn(),
    stepName: 'Translation',
    isFinalStep: false,
    approvedCount: 3,
    unapproved: { drafts: 0, untranslated: 0 },
    canApproveAll: true,
    approveBlocked: null,
    pending: false,
    onConfirm: vi.fn(),
    onApproveAll: vi.fn(),
    ...extra,
  }
  render(<CompleteStepDialog {...props} />)
  return props
}

const commentBox = () => screen.queryByLabelText(/comment for next assignee/i)

describe('CompleteStepDialog', () => {
  it('completes with the comment written for the next assignee', async () => {
    const props = renderDialog()
    await userEvent.type(commentBox()!, 'Please check terminology')
    await userEvent.click(screen.getByRole('button', { name: 'Complete step' }))
    expect(props.onConfirm).toHaveBeenCalledWith('Please check terminology')
  })

  it('completes with an empty comment when none is written', async () => {
    const props = renderDialog()
    await userEvent.click(screen.getByRole('button', { name: 'Complete step' }))
    expect(props.onConfirm).toHaveBeenCalledWith('')
  })

  it('the final step has no next assignee, so it offers no comment box', async () => {
    const props = renderDialog({ isFinalStep: true })
    expect(commentBox()).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Complete final step' }))
    expect(props.onConfirm).toHaveBeenCalledWith('')
  })

  it('counts the drafts and untranslated segments left', () => {
    renderDialog({ unapproved: { drafts: 2, untranslated: 1 } })
    expect(screen.getByText('There are 2 drafts and 1 untranslated segment.')).toBeInTheDocument()
  })

  it('Approve all and complete carries the comment for the next assignee', async () => {
    const props = renderDialog({ unapproved: { drafts: 2, untranslated: 0 } })
    await userEvent.type(commentBox()!, 'Check section 3')
    await userEvent.click(screen.getByRole('button', { name: 'Approve all and complete' }))
    expect(props.onApproveAll).toHaveBeenCalledWith('Check section 3')
    expect(props.onConfirm).not.toHaveBeenCalled()
  })

  it('offers Approve all only with drafts left and edits allowed', () => {
    renderDialog({ unapproved: { drafts: 0, untranslated: 2 } })
    expect(screen.queryByRole('button', { name: 'Approve all and complete' })).toBeNull()
    cleanup()
    renderDialog({ unapproved: { drafts: 2, untranslated: 0 }, canApproveAll: false })
    expect(screen.queryByRole('button', { name: 'Approve all and complete' })).toBeNull()
  })

  it('lists the segments Approve all could not approve', () => {
    renderDialog({ unapproved: { drafts: 1, untranslated: 0 }, approveBlocked: { toFix: [3, 8], held: [] } })
    const alert = screen.getByTestId('complete-approve-blocked')
    expect(alert).toHaveTextContent('The step was not completed.')
    expect(alert).toHaveTextContent('Segments 3, 8 could not be approved because of a QA error or a change made meanwhile. Fix them, then try again.')
    expect(alert).not.toHaveTextContent('someone else')
  })

  it('says which segments someone else is editing, apart from the ones to fix', () => {
    renderDialog({ unapproved: { drafts: 3, untranslated: 0 }, approveBlocked: { toFix: [2], held: [4] } })
    const alert = screen.getByTestId('complete-approve-blocked')
    expect(alert).toHaveTextContent('The step was not completed.')
    expect(alert).toHaveTextContent('Segment 2 could not be approved because of a QA error or a change made meanwhile. Fix it, then try again.')
    expect(alert).toHaveTextContent('Segment 4 is being edited by someone else. Try again when it is free.')
  })

  it('names only the held segments when nothing else blocked', () => {
    renderDialog({ unapproved: { drafts: 2, untranslated: 0 }, approveBlocked: { toFix: [], held: [4, 6] } })
    const alert = screen.getByTestId('complete-approve-blocked')
    expect(alert).toHaveTextContent('The step was not completed. Segments 4, 6 are being edited by someone else. Try again when they are free.')
    expect(alert).not.toHaveTextContent('QA error')
  })

  it('has no Cancel button: the ✕ closes it', () => {
    renderDialog({ unapproved: { drafts: 1, untranslated: 0 } })
    expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull()
  })

  // A failed request keeps the dialog open with the comment; Escape while the
  // request runs must not close it first.
  it('ignores Escape while completing', async () => {
    const props = renderDialog({ pending: true })
    commentBox()!.focus()
    await userEvent.keyboard('{Escape}')
    expect(props.onOpenChange).not.toHaveBeenCalledWith(false, expect.anything())
    expect(props.onOpenChange).not.toHaveBeenCalledWith(false)
  })

  it('closes on Escape when idle', async () => {
    const props = renderDialog()
    commentBox()!.focus()
    await userEvent.keyboard('{Escape}')
    expect(props.onOpenChange).toHaveBeenCalledWith(false, expect.anything())
  })
})
