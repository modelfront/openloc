// The server reads `include_locked` on both spell check writes and reads; only
// the "All segments (includes locked)" scope may send it. The dialog and editor
// tests mock these functions, so the request bodies are pinned here.
import { describe, it, expect, vi, beforeEach } from 'vitest'

const mockRequest = vi.fn()
vi.mock('./request', () => ({
  request: (...a: unknown[]) => mockRequest(...a),
  getToken: () => null,
  BASE: '/api',
}))

import { checkJobSpelling, batchDraftTranslations } from './spellcheck'

const body = () => JSON.parse(String(mockRequest.mock.calls[0][1].body))

beforeEach(() => {
  mockRequest.mockReset()
})

describe('checkJobSpelling', () => {
  beforeEach(() => mockRequest.mockResolvedValue({ data: { results: [], failed: 0 } }))

  it('sends only the ids by default', async () => {
    await checkJobSpelling(7, [1, 2])
    expect(mockRequest.mock.calls[0][0]).toBe('/jobs/7/spellcheck')
    expect(body()).toEqual({ translation_ids: [1, 2] })
  })

  it('asks for locked segments only when told to', async () => {
    await checkJobSpelling(7, [1, 2], undefined, { includeLocked: true })
    expect(body()).toEqual({ translation_ids: [1, 2], include_locked: true })
  })
})

describe('batchDraftTranslations', () => {
  const fixes = [{ translation_id: 1, human_text: 'Adresse' }]
  beforeEach(() => mockRequest.mockResolvedValue({ data: { applied: [], conflicts: [], locked: [], rejected: [] } }))

  it('sends only the fixes by default', async () => {
    await batchDraftTranslations(3, fixes)
    expect(mockRequest.mock.calls[0][0]).toBe('/files/3/translations/batch-draft')
    expect(body()).toEqual({ fixes })
  })

  it('writes locked segments only when told to', async () => {
    await batchDraftTranslations(3, fixes, { includeLocked: true })
    expect(body()).toEqual({ fixes, include_locked: true })
  })
})
