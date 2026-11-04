import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/api', () => ({
  listLanguages: () => Promise.resolve([
    { code: 'de', name: 'German' }, { code: 'fr', name: 'French' }, { code: 'ja', name: 'Japanese' },
  ]),
}))

import { LanguageFilterChip } from './LanguageFilterChip'

async function openChip(props: { value: string | null; codes?: string[] }) {
  const qc = new QueryClient()
  render(
    <QueryClientProvider client={qc}>
      <LanguageFilterChip label="Target" testId="chip" onChange={() => {}} {...props} />
    </QueryClientProvider>,
  )
  await userEvent.setup().click(screen.getByTestId('chip'))
  const options = await screen.findAllByRole('button', { name: /^(All|.+ — .+)$/ })
  return options.map(o => o.textContent)
}

describe('LanguageFilterChip', () => {
  it('offers every language without codes', async () => {
    const labels = await openChip({ value: null })
    expect(labels[0]).toBe('All')
    expect(labels.filter(l => /German|French|Japanese/.test(l ?? ''))).toHaveLength(3)
  })

  it('offers only the given codes', async () => {
    const labels = await openChip({ value: null, codes: ['ja', 'de'] })
    expect(labels.some(l => l?.includes('French'))).toBe(false)
    expect(labels.filter(l => /German|Japanese/.test(l ?? ''))).toHaveLength(2)
  })

  it('finds a language by name or code in the search box', async () => {
    await openChip({ value: null })
    const user = userEvent.setup()
    const search = screen.getByTestId('chip-search')
    await user.type(search, 'fren')
    expect(screen.getByRole('button', { name: /French/ })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /German/ })).not.toBeInTheDocument()
    await user.clear(search)
    // "de" is in no language name: only the code can match it.
    await user.type(search, 'de')
    expect(screen.getByRole('button', { name: /German/ })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /French|Japanese/ })).not.toBeInTheDocument()
    expect(screen.queryByText('No matches')).not.toBeInTheDocument()
  })

  it('keeps the current pick listed even with no hit left', async () => {
    const labels = await openChip({ value: 'fr', codes: ['de'] })
    expect(labels.some(l => l?.includes('French'))).toBe(true)
    expect(labels.some(l => l?.includes('Japanese'))).toBe(false)
  })
})
