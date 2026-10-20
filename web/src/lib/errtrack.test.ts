import { describe, it, expect, beforeEach } from 'vitest'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import {
  shouldReport,
  scrubNotice,
  scrubMessage,
  stripUrl,
  redactTokenPaths,
  redactSensitive,
  sanitizeContext,
  throttleNotice,
  resetThrottle,
  REDACTED,
} from './errtrack'
import { ApiError } from '@/api/request'

describe('shouldReport', () => {
  it.each([
    ['ApiError 401', new ApiError(401, 'unauthorized'), false],
    ['ApiError 403', new ApiError(403, 'forbidden'), false],
    ['ApiError 404', new ApiError(404, 'not found'), false],
    ['ApiError 409 save conflict', new ApiError(409, 'conflict'), false],
    ['ApiError 412 no claude key', new ApiError(412, 'precondition'), false],
    ['ApiError 500 (backend already reported)', new ApiError(500, 'boom'), false],
    ['ApiError 503', new ApiError(503, 'unavailable'), false],
    ['ApiError 0 (XHR network sentinel)', new ApiError(0, 'Network error during re-import'), true],
    ['ApiError 400 contract bug', new ApiError(400, 'bad request'), true],
    ['ApiError 422', new ApiError(422, 'unprocessable'), true],
    ['ApiError 429 rate limit (shown to the user)', new ApiError(429, 'too many requests'), false],
    ['session-expired plain Error', new Error('Session expired'), false],
    ['raw fetch network failure', new TypeError('Failed to fetch'), true],
    ['generic Error', new Error('something broke'), true],
    ['null', null, false],
    ['undefined', undefined, false],
  ])('%s → %s', (_label, err, want) => {
    expect(shouldReport(err)).toBe(want)
  })

  it('skips AbortError (user cancel / navigation)', () => {
    expect(shouldReport(new DOMException('The user aborted a request.', 'AbortError'))).toBe(false)
  })

  it('skips NoClaudeKeyError by name without importing chat.ts', () => {
    const err = new Error('no key')
    err.name = 'NoClaudeKeyError'
    expect(shouldReport(err)).toBe(false)
  })

  it('skips chunk-load errors — lazy-reload owns those', () => {
    expect(shouldReport(new Error('Failed to fetch dynamically imported module: /assets/x.js'))).toBe(false)
    expect(shouldReport(new Error('Loading chunk 42 failed'))).toBe(false)
    // MIME-mismatch wordings (server returned index.html for a .js request)
    expect(shouldReport(new Error('Failed to load module script: …'))).toBe(false)
    expect(shouldReport(new TypeError("'text/html' is not a valid JavaScript MIME type. Expected a JavaScript module script."))).toBe(false)
  })
})

describe('stripUrl', () => {
  it('strips query, fragment and userinfo', () => {
    expect(stripUrl('https://app.example.com/editor?source_text=CUSTOMER+SEGMENT#frag')).toBe(
      'https://app.example.com/editor',
    )
    expect(stripUrl('https://user:pass@host.com/x?q=1')).toBe('https://host.com/x')
  })

  it('falls back to a plain cut on malformed URLs — the query never survives', () => {
    expect(stripUrl('not a url ?source_text=SECRET')).toBe('not a url ')
    expect(stripUrl('%%%?token=abc#x')).toBe('%%%')
  })

  it('passes through empty and clean values', () => {
    expect(stripUrl('')).toBe('')
    expect(stripUrl('https://app.example.com/projects')).toBe('https://app.example.com/projects')
  })

  it('redacts the token segment of invite, reset and share links', () => {
    expect(stripUrl('https://app.example.com/invite/live-token?error=email_mismatch')).toBe(
      `https://app.example.com/invite/${REDACTED}`,
    )
    expect(stripUrl('https://app.example.com/reset-password/live-token#x')).toBe(
      `https://app.example.com/reset-password/${REDACTED}`,
    )
    // A relative URL doesn't parse, so it takes the plain-cut branch.
    expect(stripUrl('/api/shared/charts/live-token/data?filters=1')).toBe(`/api/shared/charts/${REDACTED}/data`)
  })
})

// The same cases run against the Go scrubber (internal/errtrack), so the two
// twins can't drift apart.
describe('redaction cases shared with the Go scrubber', () => {
  type Case = { name: string; in: string; want: string }
  const groups: Record<string, Case[]> = JSON.parse(
    readFileSync(join(__dirname, '../../../internal/errtrack/testdata/redaction_cases.json'), 'utf8'),
  )
  const fns: Record<string, (s: string) => string> = {
    redactTokenPaths,
    scrubURL: stripUrl,
    scrubMessage,
  }

  it('has a TS twin for every group', () => {
    expect(Object.keys(groups).sort()).toEqual(Object.keys(fns).sort())
  })

  for (const [group, cases] of Object.entries(groups)) {
    it.each(cases.map((c) => [c.name, c.in, c.want]))(`${group}: %s`, (_name, input, want) => {
      expect(fns[group](input)).toBe(want)
    })
  }
})

describe('redactTokenPaths', () => {
  it.each([
    ['/invite/abc123', `/invite/${REDACTED}`],
    ['https://app.example.com/invite/abc123?error=email_mismatch', `https://app.example.com/invite/${REDACTED}?error=email_mismatch`],
    ['/invite/abc123#top', `/invite/${REDACTED}#top`],
    ['/api/invites/lookup/abc123', `/api/invites/lookup/${REDACTED}`],
    ['/reset-password/abc123', `/reset-password/${REDACTED}`],
    ['/shared/charts/abc123', `/shared/charts/${REDACTED}`],
    ['/api/shared/charts/abc123/data', `/api/shared/charts/${REDACTED}/data`],
    ['/INVITE/abc123', `/INVITE/${REDACTED}`],
    [
      'GET /api/invites/lookup/t1 from https://app.example.com/reset-password/t2 failed',
      `GET /api/invites/lookup/${REDACTED} from https://app.example.com/reset-password/${REDACTED} failed`,
    ],
    ['url="/invite/abc123"', `url="/invite/${REDACTED}"`],
    [`/invite/${REDACTED}`, `/invite/${REDACTED}`],
    // Not token paths: an invite by id, no token segment, a longer segment.
    ['/api/invites/123', '/api/invites/123'],
    ['/invite', '/invite'],
    ['/invite/', '/invite/'],
    ['/api/invites/lookup', '/api/invites/lookup'],
    ['/reinvite/abc123', '/reinvite/abc123'],
    ['/projects/7/jobs/42', '/projects/7/jobs/42'],
    ['', ''],
  ])('%s → %s', (input, want) => {
    expect(redactTokenPaths(input)).toBe(want)
  })
})

describe('scrubMessage', () => {
  it('strips query strings from embedded URLs', () => {
    expect(scrubMessage('GET https://x.com/api/tm/lookup?source_text=SECRET failed')).toBe(
      'GET https://x.com/api/tm/lookup failed',
    )
  })

  it('redacts token path segments in embedded URLs and paths', () => {
    // Not hex and not a JWT, so only the token-path rule can catch it.
    expect(
      scrubMessage('GET /api/invites/lookup/live-token failed on https://app.example.com/invite/live-token?x=1'),
    ).toBe(`GET /api/invites/lookup/${REDACTED} failed on https://app.example.com/invite/${REDACTED}`)
    expect(scrubMessage('POST /reset-password/live-token rejected')).toBe(`POST /reset-password/${REDACTED} rejected`)
  })

  it('redacts JWTs', () => {
    const jwt = 'eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9P' // gitleaks:allow
    expect(scrubMessage(`auth failed for ${jwt}`)).toBe(`auth failed for ${REDACTED}`)
  })

  it('redacts long hex runs', () => {
    expect(scrubMessage('key deadbeefdeadbeefdeadbeefdeadbeef rejected')).toBe(`key ${REDACTED} rejected`)
  })

  it('leaves ordinary messages alone', () => {
    expect(scrubMessage("Cannot read properties of undefined (reading 'map')")).toBe(
      "Cannot read properties of undefined (reading 'map')",
    )
  })
})

describe('redactSensitive', () => {
  it('redacts credential-shaped keys recursively, keeps the rest', () => {
    const obj = {
      user_id: '7',
      authorization: 'Bearer abc',
      nested: { api_key: 'xyz', safe: 'keep-me', Cookie: 'session=1' },
    }
    redactSensitive(obj)
    expect(obj.user_id).toBe('7')
    expect(obj.authorization).toBe(REDACTED)
    expect(obj.nested.api_key).toBe(REDACTED)
    expect(obj.nested.Cookie).toBe(REDACTED)
    expect(obj.nested.safe).toBe('keep-me')
  })
})

describe('sanitizeContext', () => {
  it('never mutates the caller-supplied object, even nested', () => {
    const shared = { job: { id: 7, api_key: 'live-value' } }
    const out = sanitizeContext(shared)
    expect(shared.job.api_key).toBe('live-value')
    expect((out.job as Record<string, unknown>).api_key).toBe(REDACTED)
    expect((out.job as Record<string, unknown>).id).toBe(7)
  })

  it('tolerates circular references instead of throwing', () => {
    const ctx: Record<string, unknown> = { name: 'a' }
    ctx.self = ctx
    const out = sanitizeContext(ctx)
    expect(out.name).toBe('a')
    expect(out.self).toBe('[Truncated]')
  })

  it('truncates beyond the depth limit', () => {
    const deep = { a: { b: { c: { d: { e: 'too deep' } } } } }
    const out = sanitizeContext(deep) as { a: { b: { c: { d: unknown } } } }
    expect(out.a.b.c.d).toBe('[Truncated]')
  })
})

describe('scrubNotice', () => {
  it('scrubs url, message and all key-value blocks — and never drops', () => {
    const notice = {
      url: 'https://app.example.com/editor?source_text=SECRET',
      message: 'fetch https://x.com/lookup?source_text=LEAK failed',
      context: { token: 'abc', queryKey: '["projects"]' },
      params: { password: 'hunter2' },
      headers: { Authorization: 'Bearer x' },
      cgiData: { HTTP_COOKIE: 'a=b' },
    }
    expect(scrubNotice(notice)).toBe(true)
    expect(notice.url).toBe('https://app.example.com/editor')
    expect(notice.message).toBe('fetch https://x.com/lookup failed')
    expect(notice.context.token).toBe(REDACTED)
    expect(notice.context.queryKey).toBe('["projects"]')
    expect(notice.params.password).toBe(REDACTED)
    expect(notice.headers.Authorization).toBe(REDACTED)
    expect(notice.cgiData.HTTP_COOKIE).toBe(REDACTED)
  })

  it('returns true on empty/missing notices too', () => {
    expect(scrubNotice(undefined)).toBe(true)
    expect(scrubNotice({})).toBe(true)
  })
})

describe('throttleNotice', () => {
  beforeEach(resetThrottle)

  it('allows 5 per minute per key, drops the 6th', () => {
    const t0 = 1_000_000
    for (let i = 0; i < 5; i++) {
      expect(throttleNotice({ name: 'Error', message: 'boom' }, t0 + i)).toBe(true)
    }
    expect(throttleNotice({ name: 'Error', message: 'boom' }, t0 + 5)).toBe(false)
  })

  it('keys are independent — one flood does not starve other errors', () => {
    const t0 = 1_000_000
    for (let i = 0; i < 6; i++) throttleNotice({ name: 'Error', message: 'flood' }, t0 + i)
    expect(throttleNotice({ name: 'Error', message: 'different' }, t0 + 6)).toBe(true)
  })

  it('refills over time', () => {
    const t0 = 1_000_000
    for (let i = 0; i < 5; i++) throttleNotice({ name: 'E', message: 'x' }, t0)
    expect(throttleNotice({ name: 'E', message: 'x' }, t0 + 1)).toBe(false)
    // A full window later the bucket is full again.
    expect(throttleNotice({ name: 'E', message: 'x' }, t0 + 60_001)).toBe(true)
  })
})
