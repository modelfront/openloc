import { describe, it, expect, vi, beforeEach } from 'vitest'

const mockRequest = vi.fn()
vi.mock('./request', () => ({
  request: (...a: unknown[]) => mockRequest(...a),
  getToken: () => null,
  BASE: '/api',
}))

import { listTMEntries } from './tms'

const BASE = { searchSource: true, searchTarget: true, caseSensitive: false, useRegex: false, exactMatch: false }
const query = () => new URLSearchParams(String(mockRequest.mock.calls[0][0]).split('?')[1])

beforeEach(() => {
  mockRequest.mockReset()
  mockRequest.mockResolvedValue({ data: [], cursor: 0, has_more: false, total: 0 })
})

describe('listTMEntries search options', () => {
  it('sends nothing extra for the defaults', async () => {
    await listTMEntries(1, { search: 'hello', searchOptions: BASE })
    expect(Object.fromEntries(query())).toEqual({ q: 'hello' })
  })

  it('sends one scope when only one field is searched', async () => {
    await listTMEntries(1, { search: 'hello', searchOptions: { ...BASE, searchSource: false } })
    expect(query().get('scope')).toBe('target')
  })

  it('sends the match flags', async () => {
    await listTMEntries(1, { search: 'a.b', searchOptions: { ...BASE, searchTarget: false, exactMatch: true, caseSensitive: true, useRegex: true } })
    expect(Object.fromEntries(query())).toEqual({
      q: 'a.b', scope: 'source', exact: 'true', case_sensitive: 'true', regex: 'true',
    })
  })

  it('ignores the options when there is no search text', async () => {
    await listTMEntries(1, { searchOptions: { ...BASE, useRegex: true } })
    expect(mockRequest.mock.calls[0][0]).toBe('/tms/1/entries')
  })
})
