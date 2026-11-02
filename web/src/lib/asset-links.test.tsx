// The links from a project's settings to QA profiles and MT routers point at a
// row on a list page. These render the real list page at the link the settings
// build, so a key that drifts on either side fails here.
import { describe, it, expect, vi } from 'vitest'
import type { ReactNode } from 'react'
import { render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { AttentionProvider } from '@/hooks/useAttention'
import { MTRoutersSection } from '@/components/mt/MTRoutersSection'
import QAProfiles from '@/pages/QAProfiles'
import { mtRouterPath, qaProfilePath } from './asset-links'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

vi.mock('../api', () => ({
  listMTRouters: () => Promise.resolve([
    { id: 1, name: 'General', rules: [], default_mt_engine_id: 1 },
    { id: 2, name: 'Legal', rules: [], default_mt_engine_id: 1 },
  ]),
  createMTRouter: vi.fn(),
  updateMTRouter: vi.fn(),
  deleteMTRouter: vi.fn(),
  listMTEngines: () => Promise.resolve([]),
  listAIEngines: () => Promise.resolve([]),
  listAIPrompts: () => Promise.resolve([]),
  listAIVendors: () => Promise.resolve([]),
  listLanguages: () => Promise.resolve([]),
  listQAProfiles: () => Promise.resolve([
    { id: 1, name: 'Standard', description: '', created_at: '2026-10-01T00:00:00Z' },
    { id: 2, name: 'Strict', description: '', created_at: '2026-10-01T00:00:00Z' },
  ]),
  getQAProfile: (id: number) => Promise.resolve({ profile: { id, name: id === 2 ? 'Strict' : 'Standard' }, rules: [] }),
  createQAProfile: vi.fn(),
  updateQAProfile: vi.fn(),
  deleteQAProfile: vi.fn(),
  exportQAProfile: vi.fn(),
  importQAProfile: vi.fn(),
  replaceQAProfileRules: vi.fn(),
  saveQARegexRule: vi.fn(),
  upsertQARule: vi.fn(),
  deleteQARule: vi.fn(),
}))

function renderAt(url: string, page: ReactNode) {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter initialEntries={[url]}>
        <AttentionProvider>{page}</AttentionProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('asset links to list rows', () => {
  it("an MT router link rings that router's card on the MT page", async () => {
    renderAt(mtRouterPath(2), <MTRoutersSection canWrite />)
    const card = await screen.findByTestId('mt-router-2')
    await waitFor(() => expect(card.dataset.attention).toBe(''))
    expect(screen.getByTestId('mt-router-1').dataset.attention).toBeUndefined()
  })

  it('a QA profile link rings that profile and opens its rules', async () => {
    renderAt(qaProfilePath(2), <QAProfiles />)
    const card = await screen.findByTestId('qa-profile-2')
    await waitFor(() => expect(card.dataset.attention).toBe(''))
    expect(screen.getByTestId('qa-profile-1').dataset.attention).toBeUndefined()
    // Only the linked profile is expanded: its rules load into the card.
    await within(card).findByTestId('qa-rules-panel')
    expect(within(screen.getByTestId('qa-profile-1')).queryByTestId('qa-rules-panel')).toBeNull()
  })
})
