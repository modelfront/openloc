// E2E: project name search on the projects list (server-side `q` filter).
//
// Pins the frontend wiring: typing in the search box debounces into the `q`
// URL param, refetches, and the list shows only matching projects.
//
// Anti-tautology: delete the two `q` lines from parseProjectFilters
// (web/src/lib/project-filters.ts) → the param never reaches the query key →
// both projects stay visible after searching → this test goes RED.

import { test, expect } from '@playwright/test'
import { login } from './helpers'

const TS = Date.now()
const NAME_MATCH = `SearchZebra ${TS}`
const NAME_OTHER = `SearchGiraffe ${TS}`

test.describe('project name search', () => {
  let idMatch: number | null = null
  let idOther: number | null = null

  test.beforeEach(async ({ page }) => {
    await login(page)
    const a = await page.request.post('/api/projects', {
      data: { name: NAME_MATCH, source_language: 'en', target_languages: ['fr'] },
    })
    expect(a.ok()).toBe(true)
    idMatch = (await a.json()).data.id
    const b = await page.request.post('/api/projects', {
      data: { name: NAME_OTHER, source_language: 'en', target_languages: ['fr'] },
    })
    expect(b.ok()).toBe(true)
    idOther = (await b.json()).data.id
  })

  test.afterEach(async ({ page }) => {
    if (idMatch != null) await page.request.delete(`/api/projects/${idMatch}`)
    if (idOther != null) await page.request.delete(`/api/projects/${idOther}`)
    idMatch = idOther = null
  })

  test('typing a name filters the list to matching projects only', async ({ page }) => {
    await page.goto('/')

    // Both freshly-created projects are on the first page (id DESC) before search.
    await expect(page.getByTestId(`project-row-${idMatch}`)).toBeVisible()
    await expect(page.getByTestId(`project-row-${idOther}`)).toBeVisible()

    // Type a substring unique to the "match" project and wait for the query
    // that actually carries `q` (not the initial no-q load).
    const search = page.getByTestId('project-search')
    const resp = page.waitForResponse(
      r =>
        r.url().includes('/api/projects?') &&
        r.url().includes('q=SearchZebra') &&
        r.request().method() === 'GET',
    )
    await search.fill('SearchZebra')
    await resp

    await expect(page.getByTestId(`project-row-${idMatch}`)).toBeVisible()
    await expect(page.getByTestId(`project-row-${idOther}`)).toHaveCount(0)

    // The search input must keep focus after results load. Without
    // `placeholderData: keepPreviousData` in ProjectList, the new query key
    // flips `isLoading` true, unmounts the page into <LoadingPlaceholder/> and
    // back, and focus is lost (activeElement becomes <body>). Remove that
    // option → this assertion goes RED.
    await expect(search).toBeFocused()

    // Clearing the box drops `q` and restores the other project.
    await search.fill('')
    await expect(page.getByTestId(`project-row-${idOther}`)).toBeVisible()
    await expect(page).not.toHaveURL(/q=/)
  })
})
