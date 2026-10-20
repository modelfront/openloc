import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, act, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import ConcordanceModal from './ConcordanceModal'
import type { ConcordanceResponse } from '../api'
import { chipVariants } from '@/components/ui/chip'
import { cn } from '@/lib/utils'

vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api')>()),
  concordanceSearch: vi.fn(),
}))

const { concordanceSearch } = await import('../api')
const mockSearch = vi.mocked(concordanceSearch)

function tmHit(source: string, target: string, targetRich?: string): ConcordanceResponse {
  return {
    tm_matches: [{
      id: 1,
      source_language: 'en',
      target_language: 'de',
      source_text: source,
      target_text: target,
      target_rich_text: targetRich,
      tm_name: 'Default TM',
    }],
    term_matches: [],
  } as unknown as ConcordanceResponse
}

// Arbitrary — the API is mocked, so the id only has to be non-null. Named so the
// call-args assertion below reads as "the project we passed in", not as magic 7.
const PROJECT_ID = 42
const SOURCE_LANG = 'en'
const TARGET_LANG = 'de'

function renderModal(initialQuery: string, overrides: Partial<{
  projectId: number | null
  sourceLanguage: string
  targetLanguage: string
  onInsert: (text: string) => void
  plainText: boolean
}> = {}) {
  return render(
    <ConcordanceModal
      open
      onClose={() => {}}
      // `??` won't do: null is a meaningful override (missing project context).
      projectId={overrides.projectId !== undefined ? overrides.projectId : PROJECT_ID}
      sourceLanguage={overrides.sourceLanguage ?? SOURCE_LANG}
      targetLanguage={overrides.targetLanguage ?? TARGET_LANG}
      onInsert={overrides.onInsert ?? (() => {})}
      plainText={overrides.plainText}
      initialQuery={initialQuery}
    />,
  )
}

describe('ConcordanceModal', () => {
  // Block body on purpose: `() => mockSearch.mockReset()` returns the mock, and
  // vitest treats a returned value as an async hook and waits on it.
  beforeEach(() => { mockSearch.mockReset() })

  // A work modal the user reads the grid behind, so it opts into dragging.
  // The drag itself is covered in ui/dialog.test.tsx.
  it('can be moved by its header', async () => {
    renderModal('')
    const header = (await screen.findByText('Concordance search')).closest('[data-slot="dialog-header"]')
    expect(header).toHaveAttribute('data-draggable', 'true')
  })

  it('auto-searches the phrase it was opened with', async () => {
    mockSearch.mockResolvedValue(tmHit('Reset your password.', 'Passwort zurücksetzen.'))
    renderModal('password')

    expect(await screen.findByText('Passwort zurücksetzen.')).toBeInTheDocument()
    expect(mockSearch).toHaveBeenCalledWith(PROJECT_ID, SOURCE_LANG, TARGET_LANG, 'password')
  })

  it('does not fire a search when opened without a selection', async () => {
    renderModal('')
    await waitFor(() => expect(screen.getByPlaceholderText(/search tm/i)).toHaveValue(''))
    expect(mockSearch).not.toHaveBeenCalled()
    // No "No matches found" either — an empty box shouldn't look like a failed search.
    expect(screen.queryByText(/no matches found/i)).not.toBeInTheDocument()
  })

  // The linguist-role bug: the CAT editor couldn't resolve the project, so the
  // modal opened prefilled and did absolutely nothing — no request, no message.
  // Missing context must be visible, never silent.
  it('says so when project context is missing instead of silently doing nothing', async () => {
    renderModal('password', { projectId: null })

    expect(await screen.findByRole('alert')).toHaveTextContent(/missing its project context/i)
    expect(mockSearch).not.toHaveBeenCalled()
    expect(screen.queryByText(/no matches found/i)).not.toBeInTheDocument()
  })

  it('treats a missing language pair the same way', async () => {
    renderModal('password', { sourceLanguage: '' })

    expect(await screen.findByRole('alert')).toHaveTextContent(/missing its project context/i)
    expect(mockSearch).not.toHaveBeenCalled()
  })

  // A failed request and an empty TM are different answers — don't render the
  // second when the first happened.
  it('reports a failed request rather than showing "no matches"', async () => {
    // Deferred rejection, not mockRejectedValue / an async throw: those settle
    // the promise before the component's `await` attaches, and vitest counts the
    // gap as an unhandled rejection — failing the test even though the component
    // handles the error correctly. Rejecting after render puts the consumer first.
    let rejectSearch: (e: Error) => void = () => {}
    mockSearch.mockImplementation(() => new Promise<never>((_, rej) => { rejectSearch = rej }))
    renderModal('password')
    rejectSearch(new Error('network down'))

    expect(await screen.findByRole('alert')).toHaveTextContent(/search failed/i)
    expect(screen.queryByText(/no matches found/i)).not.toBeInTheDocument()
  })

  // The auto-search on open races whatever the user types next. If the slow
  // first response were allowed to land last, the linguist would watch their
  // results get replaced by the phrase they'd already moved on from.
  it('discards a superseded response that resolves after a newer search', async () => {
    const user = userEvent.setup()
    let releaseAutoSearch: (v: ConcordanceResponse) => void = () => {}
    mockSearch
      .mockImplementationOnce(() => new Promise<ConcordanceResponse>(res => { releaseAutoSearch = res }))
      .mockResolvedValueOnce(tmHit('Enable Two-Factor Authentication.', 'Zwei-Faktor-Authentifizierung aktivieren.'))

    renderModal('password')

    // Second, manual search resolves first. Enter is the path that reaches this
    // — the Search button is disabled while a request is in flight, so only the
    // keyboard can start a second search before the first has answered.
    const input = screen.getByPlaceholderText(/search tm/i)
    await user.clear(input)
    await user.type(input, 'authentication{Enter}')
    expect(await screen.findByText('Zwei-Faktor-Authentifizierung aktivieren.')).toBeInTheDocument()

    // Now the stale auto-search finally answers. `act` is load-bearing: it
    // flushes the promise's continuation and the resulting render. A bare
    // `waitFor(() => expect(stale).not.toBeInTheDocument())` passes on its first
    // synchronous check — the stale text was never rendered — so the assertion
    // would hold even with the seq guards removed.
    await act(async () => {
      releaseAutoSearch(tmHit('Reset your password.', 'Passwort zurücksetzen.'))
    })

    expect(screen.queryByText('Passwort zurücksetzen.'), 'stale response must not paint').not.toBeInTheDocument()
    expect(screen.getByText('Zwei-Faktor-Authentifizierung aktivieren.')).toBeInTheDocument()
  })

  // Reopening while a search is in flight bumps searchSeq, which orphans that
  // request's seq-guarded `finally` — nothing else would ever clear `loading`.
  it('does not leave the Search button stuck on "Searching…" after a reopen', async () => {
    mockSearch.mockImplementation(() => new Promise<ConcordanceResponse>(() => {}))
    const { rerender } = renderModal('password')

    // In flight: button is disabled and reads "Searching…".
    await waitFor(() => expect(screen.getByRole('button', { name: /searching/i })).toBeDisabled())

    // Reopen with no selection — the reset runs, and runSearch('') early-returns.
    rerender(
      <ConcordanceModal
        open
        onClose={() => {}}
        projectId={PROJECT_ID}
        sourceLanguage={SOURCE_LANG}
        targetLanguage={TARGET_LANG}
        onInsert={() => {}}
        initialQuery=""
      />,
    )

    const search = await screen.findByRole('button', { name: /^search$/i })
    expect(search, 'a typed query must be searchable again').toBeDisabled() // empty query
    await userEvent.setup().type(screen.getByPlaceholderText(/search tm/i), 'password')
    expect(screen.getByRole('button', { name: /^search$/i })).toBeEnabled()
  })

  // runSearch's identity changes with projectId/language. If the reset effect
  // depended on it, a prop change mid-session would wipe what the user typed.
  it('keeps the typed query when a language prop changes while open', async () => {
    mockSearch.mockResolvedValue(tmHit('Reset your password.', 'Passwort zurücksetzen.'))
    const { rerender } = renderModal('password')
    await screen.findByText('Passwort zurücksetzen.')

    const input = screen.getByPlaceholderText(/search tm/i)
    await userEvent.setup().clear(input)
    await userEvent.setup().type(input, 'authentication')

    rerender(
      <ConcordanceModal
        open
        onClose={() => {}}
        projectId={PROJECT_ID}
        sourceLanguage={SOURCE_LANG}
        targetLanguage="fr"
        onInsert={() => {}}
        initialQuery="password"
      />,
    )

    expect(input, 'the language change must not reset the query').toHaveValue('authentication')
  })

  it('shows a term status as the shared chip in its status colour', async () => {
    mockSearch.mockResolvedValue({
      tm_matches: [],
      term_matches: [{ id: 1, source_term: 'password', target_term: 'Kennwort', status: 'forbidden', notes: '', term_base_name: 'Glossary' }],
    })
    renderModal('password')
    await userEvent.click(await screen.findByRole('tab', { name: /^Terminology/ }))

    const label = await screen.findByText('Forbidden')
    expect(label).toHaveAttribute('data-slot', 'chip')
    expect(label.className).toBe(cn(chipVariants({ color: 'red', size: 'small' })))
  })

  // A plain-text file stores no tags, so a match's XLIFF markup would land as
  // literal text and be exported.
  describe('inserting a tagged match', () => {
    const rich = '<g id="1" ctype="bold">Passwort</g> zurücksetzen.'
    for (const [plainText, want] of [[false, rich], [true, 'Passwort zurücksetzen.']] as const) {
      it(`inserts ${plainText ? 'the plain target in a plain-text file' : 'the rich target'}`, async () => {
        mockSearch.mockResolvedValue(tmHit('Reset your password.', 'Passwort zurücksetzen.', rich))
        const onInsert = vi.fn()
        renderModal('Reset your password.', { onInsert, plainText })
        fireEvent.click(await screen.findByText('Source'))
        expect(onInsert).toHaveBeenCalledWith(want)
      })
    }
  })
})
