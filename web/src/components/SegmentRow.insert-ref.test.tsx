// The editor's shortcuts (Mod+Enter, Mod+Shift+S, clear, inserts) act on
// whatever the open row put in insertRef. When the row stops being the open
// one, or leaves the screen, its handlers must leave with it: otherwise a
// shortcut pressed while the next row's text is still loading would confirm
// the row the user just left.

import { describe, it, expect, vi, afterEach } from 'vitest'
import { cleanup, waitFor } from '@testing-library/react'

vi.mock('../lib/tiptap-spellcheck')
vi.mock('../lib/tiptap-whitespace')
vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return { ...actual, lookupTerms: vi.fn().mockResolvedValue([]), runTranslationQA: vi.fn().mockResolvedValue([]) }
})

import { renderSegmentRow, rowTranslation } from '../test/segment-row'

afterEach(cleanup)

describe('SegmentRow — shortcut handlers follow the open row', () => {
  it('drops its handlers when it is no longer the open row', async () => {
    const r = renderSegmentRow({ translation: rowTranslation() })
    await waitFor(() => expect(r.insertRef.current.confirm).toBeTypeOf('function'))

    r.rerender({ detached: true, isActive: false })

    expect(r.insertRef.current.confirm).toBeUndefined()
    expect(r.insertRef.current.copySource).toBeUndefined()
    expect(r.insertRef.current.replaceAll).toBeUndefined()
  })

  it('drops its handlers when it unmounts', async () => {
    const r = renderSegmentRow({ translation: rowTranslation() })
    await waitFor(() => expect(r.insertRef.current.confirm).toBeTypeOf('function'))

    r.unmount()

    expect(r.insertRef.current.confirm).toBeUndefined()
  })
})
