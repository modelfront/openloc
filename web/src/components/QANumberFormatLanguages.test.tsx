import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { QANumberFormatLanguages } from './QANumberFormatLanguages'
import { numberFormatKey, type NumberFormatLanguage } from '../lib/qa-number-format'

vi.mock('../api', () => ({
  listLanguages: () => Promise.resolve([{ code: 'es', name: 'Spanish' }, { code: 'fr', name: 'French' }, { code: 'de', name: 'German' }]),
}))
vi.mock('@/components/ui/combobox', async () => (await import('@/test/combobox-mock')).comboboxSelectMock)

const spanish: NumberFormatLanguage = { language: 'es', decimal: '.', group: ',' }

function renderList(languages: NumberFormatLanguage[], onChange = vi.fn().mockResolvedValue(undefined)) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <QANumberFormatLanguages languages={languages} onChange={onChange} saving={false} />
    </QueryClientProvider>,
  )
  return onChange
}

describe('QANumberFormatLanguages', () => {
  it('adds every picked language with the picked format, keeping the others', async () => {
    const user = userEvent.setup()
    const onChange = renderList([spanish])
    const picker = screen.getByTestId('lang')

    expect(screen.getByTestId('qa-number-format-add')).toHaveAttribute('aria-disabled', 'true')
    await user.type(picker, 'fr,de')
    await user.selectOptions(screen.getByTestId('qa-number-format-new-format'), numberFormatKey({ decimal: ',', group: ' ' }))
    expect(screen.getByTestId('qa-number-format-add')).toHaveTextContent('Add 2 languages')
    await user.click(screen.getByTestId('qa-number-format-add'))

    expect(onChange).toHaveBeenCalledWith([spanish, { language: 'fr', decimal: ',', group: ' ' }, { language: 'de', decimal: ',', group: ' ' }])
    await waitFor(() => expect(picker).toHaveValue(''))
  })

  it('keeps the picked languages when the save fails', async () => {
    const user = userEvent.setup()
    const onChange = renderList([], vi.fn().mockRejectedValue(new Error('500')))
    const picker = screen.getByTestId('lang')
    await user.type(picker, 'de')
    await user.click(screen.getByTestId('qa-number-format-add'))
    await waitFor(() => expect(onChange).toHaveBeenCalled())
    // Let the rejected save settle before looking.
    await new Promise(r => setTimeout(r, 0))
    expect(picker).toHaveValue('de')
  })

  it('changes and removes one language, sending the whole list', async () => {
    const user = userEvent.setup()
    const french = { language: 'fr', decimal: ',', group: ' ' }
    const onChange = renderList([spanish, french])
    await user.selectOptions(screen.getByTestId('qa-number-format-es'), numberFormatKey({ decimal: ',', group: '.' }))
    expect(onChange).toHaveBeenLastCalledWith([{ language: 'es', decimal: ',', group: '.' }, french])
    await user.click(screen.getByRole('button', { name: 'Remove ES' }))
    expect(onChange).toHaveBeenLastCalledWith([french])
  })
})
