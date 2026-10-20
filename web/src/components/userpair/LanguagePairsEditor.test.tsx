import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

vi.mock('../../api', async () => {
  const actual = await vi.importActual<typeof import('../../api')>('../../api')
  return { ...actual, listLanguages: vi.fn(), listUserLanguagePairs: vi.fn(), replaceUserLanguagePairs: vi.fn() }
})

import { listLanguages, listUserLanguagePairs, replaceUserLanguagePairs } from '../../api'
import LanguagePairsEditor from './LanguagePairsEditor'

function renderEditor() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <LanguagePairsEditor userId={7} userName="Dana" open={true} onOpenChange={() => {}} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.mocked(listLanguages).mockResolvedValue([
    { code: 'en', name: 'English' }, { code: 'de', name: 'German' }, { code: 'fr', name: 'French' },
  ] as Awaited<ReturnType<typeof listLanguages>>)
  vi.mocked(listUserLanguagePairs).mockReset()
  vi.mocked(replaceUserLanguagePairs).mockReset().mockResolvedValue([])
})

describe('LanguagePairsEditor load failure', () => {
  it('a failed load shows the error with a Retry and offers no Save', async () => {
    vi.mocked(listUserLanguagePairs).mockRejectedValue(new Error('server unavailable'))
    renderEditor()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load the language pairs: server unavailable")
    expect(within(alert).getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    expect(screen.queryByTestId('pair-row-0')).not.toBeInTheDocument()
    expect(screen.queryByTestId('save-pairs-button')).not.toBeInTheDocument()
    expect(replaceUserLanguagePairs).not.toHaveBeenCalled()
  })

  it('Retry that succeeds shows the saved pairs and saves them unchanged', async () => {
    vi.mocked(listUserLanguagePairs)
      .mockRejectedValueOnce(new Error('server unavailable'))
      .mockResolvedValue([
        { source_lang: 'en', target_lang: 'de', row_group: 0 },
        { source_lang: 'en', target_lang: 'fr', row_group: 0 },
      ])
    renderEditor()

    fireEvent.click(await screen.findByRole('button', { name: 'Retry' }))

    expect(await screen.findByText('2 pairs:')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    fireEvent.click(screen.getByTestId('save-pairs-button'))
    await waitFor(() => expect(replaceUserLanguagePairs).toHaveBeenCalledTimes(1))
    expect(replaceUserLanguagePairs).toHaveBeenCalledWith(7, [
      { source_lang: 'en', target_lang: 'de', row_group: 0 },
      { source_lang: 'en', target_lang: 'fr', row_group: 0 },
    ])
  })

  it('Save sends nothing while the saved pairs are still loading', async () => {
    vi.mocked(listUserLanguagePairs).mockReturnValue(new Promise(() => {}))
    renderEditor()

    await screen.findByText('Loading…')
    fireEvent.click(screen.getByTestId('save-pairs-button'))

    await new Promise(r => setTimeout(r, 0))
    expect(replaceUserLanguagePairs).not.toHaveBeenCalled()
  })
})
