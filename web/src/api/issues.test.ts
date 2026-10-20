import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, it, expect, vi, afterEach } from 'vitest'
import { createIssue } from './issues'

// The form field names the server reads the assistant's check from, shared
// with internal/chat/confirm_integration_test.go.
const names: { result: string; note: string; token: string } = JSON.parse(
  readFileSync(join(__dirname, '../../../internal/issues/testdata/report_form_check.json'), 'utf8'),
)

afterEach(() => vi.unstubAllGlobals())

describe('createIssue', () => {
  // A renamed field would drop every real check without a sound.
  it('sends the assistant’s check under the names the server reads', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ data: { id: 1 } }), {
      status: 201, headers: { 'Content-Type': 'application/json' },
    }))
    vi.stubGlobal('fetch', fetchMock)

    await createIssue({
      kind: 'bug', severity: 'major', area: 'editor', title: 'Editor stops at 100',
      description: 'The editor lists 100 of 120 segments.', context: { path: '/projects/1' },
      verification: { result: 'verified', note: 'Job 9 has 120 segments.', token: 'id.123.mac' },
    })

    const body = (fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1].body as FormData
    expect(body.get(names.result)).toBe('verified')
    expect(body.get(names.note)).toBe('Job 9 has 120 segments.')
    expect(body.get(names.token)).toBe('id.123.mac')
  })
})
