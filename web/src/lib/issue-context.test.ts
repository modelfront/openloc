import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, it, expect } from 'vitest'
import { browserName, buildIssueContext, cleanIssuePath, isSupportPage, osName } from './issue-context'

const CHROME_MAC = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36'
const EDGE_WIN = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0'
const SAFARI_IOS = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1'
const FIREFOX_LINUX = 'Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0'

describe('cleanIssuePath', () => {
  it('drops the query string and fragment', () => {
    expect(cleanIssuePath('/projects/7?tab=files#top')).toBe('/projects/7')
  })

  it('replaces the secret of a share link, an invite and a password reset', () => {
    expect(cleanIssuePath('/shared/charts/LB7Kq9x')).toBe('/shared/charts/:token')
    expect(cleanIssuePath('/invite/abc123')).toBe('/invite/:token')
    expect(cleanIssuePath('/reset-password/xyz')).toBe('/reset-password/:token')
  })
})

describe('buildIssueContext', () => {
  it('takes the ids from the path, and the browser, OS, window and version from the environment', () => {
    expect(buildIssueContext('/projects/12/jobs/34/edit', { userAgent: CHROME_MAC, width: 1440, height: 900, appVersion: 'abc123' }))
      .toEqual({ path: '/projects/12/jobs/34/edit', project_id: 12, job_id: 34, browser: 'Chrome 131', os: 'macOS', viewport: '1440x900', app_version: 'abc123' })
  })

  it('leaves out what it cannot tell', () => {
    expect(buildIssueContext('/tasks', { userAgent: 'curl/8.0', width: 0, height: 0 })).toEqual({ path: '/tasks' })
  })
})

describe('browserName and osName', () => {
  it.each([
    [EDGE_WIN, 'Edge 131', 'Windows'],
    [CHROME_MAC, 'Chrome 131', 'macOS'],
    [SAFARI_IOS, 'Safari 17', 'iOS'],
    [FIREFOX_LINUX, 'Firefox 131', 'Linux'],
  ])('reads %#', (ua, browser, os) => {
    expect(browserName(ua)).toBe(browser)
    expect(osName(ua)).toBe(os)
  })
})

// The server's test reads the same cases, so both lists stay one list. The
// router reaches these pages in any case and through escapes too.
const pages: { support: string[]; other: string[] } = JSON.parse(
  readFileSync(join(__dirname, '../../../internal/chat/testdata/support_pages.json'), 'utf8'),
)

describe('isSupportPage', () => {
  it.each(pages.support)('matches %s', path => expect(isSupportPage(path)).toBe(true))
  it.each(pages.other)('does not match %s', path => expect(isSupportPage(path)).toBe(false))
})
