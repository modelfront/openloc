// The multi-file editor's Complete step: one dialog for every open file the
// user may complete. Pins what each file would leave unapproved, when
// "Approve all and complete" is offered, what the buttons send, and how a
// file that did not complete is reported.
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ScopeFile } from '@/api'
import { ScopeCompleteDialog } from './ScopeCompleteDialog'

const file = (job_id: number, file_name: string): ScopeFile => ({
  job_id, file_id: job_id, file_name, file_format: 'txt', inline_html: true,
  first_row: 0, row_count: 3, step_name: 'Translation', step_assignee: 1, has_previous_step: false,
})

function renderDialog(over: Partial<Parameters<typeof ScopeCompleteDialog>[0]> = {}) {
  const onConfirm = vi.fn()
  render(
    <ScopeCompleteDialog
      open
      onOpenChange={() => {}}
      files={[file(1, 'a.txt'), file(2, 'b.txt')]}
      unapproved={[{ drafts: 0, untranslated: 0 }, { drafts: 2, untranslated: 1 }]}
      pending={false}
      failures={null}
      onConfirm={onConfirm}
      {...over}
    />,
  )
  return onConfirm
}

describe('ScopeCompleteDialog', () => {
  it('lists each file with what it would leave unapproved', () => {
    renderDialog()
    const list = screen.getByTestId('scope-complete-files')
    expect(list).toHaveTextContent('a.txtall approved')
    expect(list).toHaveTextContent('b.txt3 not approved')
    expect(screen.getByTestId('scope-complete-unapproved')).toHaveTextContent('There are 2 drafts and 1 untranslated segment.')
  })

  it('sends the trimmed comment, with approve all only from its button', async () => {
    const onConfirm = renderDialog()
    await userEvent.type(screen.getByLabelText(/Comment for next assignee/), '  Check the terms  ')
    await userEvent.click(screen.getByTestId('complete-approve-all'))
    expect(onConfirm).toHaveBeenLastCalledWith('Check the terms', true)
    await userEvent.click(screen.getByTestId('complete-confirm'))
    expect(onConfirm).toHaveBeenLastCalledWith('Check the terms', false)
  })

  it('offers no approve all when nothing is a draft', () => {
    renderDialog({ unapproved: [{ drafts: 0, untranslated: 0 }, { drafts: 0, untranslated: 2 }] })
    expect(screen.queryByTestId('complete-approve-all')).toBeNull()
  })

  it('names each file that did not complete and why', () => {
    renderDialog({
      failures: [
        { job_id: 2, outcome: 'blocked', blocked: [{ id: 5, reason: 'held' }] },
        { job_id: 1, outcome: 'failed', error: { code: 'forbidden', message: 'only the assignee can complete' } },
      ],
    })
    const failures = screen.getByTestId('scope-complete-failures')
    expect(failures).toHaveTextContent('b.txt: 1 segment could not be approved')
    expect(failures).toHaveTextContent('a.txt: only the assignee can complete')
  })
})
