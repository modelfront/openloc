import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { XLSXInspection } from '@/api'
import { XLSXLayoutDialog } from './XLSXLayoutDialog'

const inspection: XLSXInspection = {
  bilingual: false,
  sheets: [
    { name: 'Products', columns: [{ letter: 'A', title: 'ID' }, { letter: 'B', title: 'English' }, { letter: 'C', title: 'Note' }] },
    { name: 'Notes', columns: [{ letter: 'A', title: 'Thanks' }] },
  ],
  suggested_layout: {
    sheets: [
      { name: 'Products', header_row: true, columns: ['B'] },
      { name: 'Notes', header_row: false, columns: ['A'] },
    ],
  },
}

function renderDialog() {
  const onSubmit = vi.fn()
  render(
    <XLSXLayoutDialog
      open
      onCancel={vi.fn()}
      fileName="catalog.xlsx"
      inspection={inspection}
      initial={inspection.suggested_layout!}
      submitLabel="Upload"
      onSubmit={onSubmit}
    />,
  )
  return { onSubmit }
}

describe('XLSXLayoutDialog', () => {
  it('starts with every sheet collapsed, its choice as a summary', () => {
    renderDialog()
    expect(screen.getByTestId('xlsx-sheet-Products-toggle')).toHaveTextContent('Column B')
    expect(screen.getByTestId('xlsx-sheet-Products-toggle')).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByTestId('xlsx-sheet-Products-columns')).toBeNull()
    // How the columns work is a tooltip by the title, closed until asked for.
    expect(screen.getByLabelText('About Excel columns')).toBeInTheDocument()
    expect(screen.queryByText(/Choose the columns to translate on each sheet/)).toBeNull()
    // No preview table.
    expect(screen.queryByRole('table')).toBeNull()
  })

  it('labels each column with its row 1 text', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.click(screen.getByTestId('xlsx-sheet-Products-toggle'))
    await user.click(screen.getByTestId('xlsx-sheet-Products-columns'))
    expect(screen.getByTestId('xlsx-sheet-Products-columns-option-A')).toHaveTextContent('A (ID)')
    expect(screen.getByTestId('xlsx-sheet-Products-columns-option-C')).toHaveTextContent('C (Note)')
  })

  it('submits several columns of one sheet and the whole of another', async () => {
    const user = userEvent.setup()
    const { onSubmit } = renderDialog()

    await user.click(screen.getByTestId('xlsx-sheet-Products-toggle'))
    await user.click(screen.getByTestId('xlsx-sheet-Products-columns'))
    await user.click(screen.getByTestId('xlsx-sheet-Products-columns-option-C'))
    await user.keyboard('{Escape}')
    expect(screen.getByTestId('xlsx-sheet-Products-toggle')).toHaveTextContent('Columns B, C')

    // No column left: the whole sheet.
    await user.click(screen.getByTestId('xlsx-sheet-Notes-toggle'))
    await user.click(screen.getByTestId('xlsx-sheet-Notes-columns'))
    await user.click(screen.getByTestId('xlsx-sheet-Notes-columns-option-A'))
    await user.keyboard('{Escape}')
    expect(screen.getByTestId('xlsx-sheet-Notes-toggle')).toHaveTextContent('Whole sheet')

    await user.click(screen.getByTestId('xlsx-layout-submit'))
    expect(onSubmit).toHaveBeenCalledWith({
      sheets: [
        { name: 'Products', header_row: true, columns: ['B', 'C'] },
        { name: 'Notes', header_row: false },
      ],
    })
  })

  it('selects every column at once, and clears them back to the whole sheet', async () => {
    const user = userEvent.setup()
    const { onSubmit } = renderDialog()

    await user.click(screen.getByTestId('xlsx-sheet-Products-toggle'))
    const link = screen.getByTestId('xlsx-sheet-Products-select-all')
    expect(link).toHaveTextContent('Select all')
    await user.click(link)
    expect(screen.getByTestId('xlsx-sheet-Products-toggle')).toHaveTextContent('All columns')
    expect(link).toHaveTextContent('Clear')

    await user.click(screen.getByTestId('xlsx-layout-submit'))
    expect(onSubmit.mock.calls[0][0].sheets[0]).toEqual({ name: 'Products', header_row: true, columns: ['A', 'B', 'C'] })

    await user.click(link)
    expect(screen.getByTestId('xlsx-sheet-Products-toggle')).toHaveTextContent('Whole sheet')
    expect(link).toHaveTextContent('Select all')
  })

  it('offers no Select all for a sheet with one column', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.click(screen.getByTestId('xlsx-sheet-Notes-toggle'))
    expect(screen.getByTestId('xlsx-sheet-Notes-columns')).toBeInTheDocument()
    expect(screen.queryByTestId('xlsx-sheet-Notes-select-all')).toBeNull()
  })

  it('leaves a sheet out, and holds the submit while no sheet is left', async () => {
    const user = userEvent.setup()
    const { onSubmit } = renderDialog()

    await user.click(screen.getByTestId('xlsx-sheet-Notes-include'))
    expect(screen.getByTestId('xlsx-sheet-Notes-toggle')).toHaveTextContent('Not translated')
    await user.click(screen.getByTestId('xlsx-sheet-Products-include'))
    expect(screen.getByTestId('xlsx-layout-problem')).toHaveTextContent('Choose at least one sheet to translate.')
    await user.click(screen.getByTestId('xlsx-layout-submit'))
    expect(onSubmit).not.toHaveBeenCalled()

    await user.click(screen.getByTestId('xlsx-sheet-Products-include'))
    await user.click(screen.getByTestId('xlsx-layout-submit'))
    expect(onSubmit).toHaveBeenCalledWith({ sheets: [{ name: 'Products', header_row: true, columns: ['B'] }] })
  })
})
