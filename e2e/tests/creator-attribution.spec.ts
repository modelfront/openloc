// E2E: creator attribution on the three asset lists (projects, TMs, term
// bases) and the project page header — the name of whoever created a row,
// plus the "Created by" filter.
//
// What this pins:
//   - The create handlers stamp created_by from the caller's JWT.
//   - The list endpoints resolve that id to a display name (created_by_name,
//     a LEFT JOIN on users) and the FE renders it via <CreatorLabel>.
//   - The `created_by` filter narrows each list: server-side for projects
//     (ListProjectsFiltered), client-side for TMs / term bases.
//
// Anti-tautology: drop `CreatedBy` from the CreateTMCollection call in
// internal/translationmemory/handler_collection.go → the TM row renders "—"
// instead of the seed owner's name → the TM assertions go RED. Same for
// term bases via internal/terminology/handler.go.
//
// Filtering is driven through the URL param (the filter state is deep-linkable
// by design) plus one popover interaction test for the chip itself — that keeps
// the narrowing assertions independent of popover click flakiness.

import { test, expect } from '@playwright/test'
import { login } from './helpers'
import { SEED_OWNER_NAME, SEED_VENDOR_EMAIL, SEED_VENDOR_NAME } from '../seed-identity'

const TS = Date.now()
const PROJECT_NAME = `CreatorAttrib ${TS}`
const TM_NAME = `CreatorAttrib TM ${TS}`
const TB_NAME = `CreatorAttrib TB ${TS}`

// Seed owner (dev auto-login identity) — the creator of everything below.
const OWNER_NAME = SEED_OWNER_NAME
// Seeded linguist in the same org; creates nothing, so filtering by them must
// empty every list.
const OTHER_EMAIL = SEED_VENDOR_EMAIL

test.describe('creator attribution', () => {
  let projectId: number
  let tmId: number
  let tbId: number
  let meId: number
  let otherId: number

  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage()
    await login(page)

    const p = await page.request.post('/api/projects', {
      data: { name: PROJECT_NAME, source_language: 'en', target_languages: ['fr'] },
    })
    expect(p.ok()).toBe(true)
    projectId = (await p.json()).data.id

    const tm = await page.request.post('/api/tms', {
      data: { name: TM_NAME, source_language: 'en', target_languages: ['fr'] },
    })
    expect(tm.ok()).toBe(true)
    tmId = (await tm.json()).data.id

    const tb = await page.request.post('/api/terminology', {
      data: { name: TB_NAME, source_language: 'en' },
    })
    expect(tb.ok()).toBe(true)
    tbId = (await tb.json()).data.id

    const me = await page.request.get('/api/auth/me')
    expect(me.ok()).toBe(true)
    meId = (await me.json()).data.id

    const members = await page.request.get('/api/members')
    expect(members.ok()).toBe(true)
    const other = (await members.json()).data.find((m: { email: string }) => m.email === OTHER_EMAIL)
    expect(other, `seeded member ${OTHER_EMAIL} missing — run make seed`).toBeTruthy()
    otherId = other.id

    await page.close()
  })

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage()
    await login(page)
    await page.request.delete(`/api/projects/${projectId}`)
    await page.request.delete(`/api/tms/${tmId}`)
    await page.request.delete(`/api/terminology/${tbId}`)
    await page.close()
  })

  test('projects list shows the creator and filters by them', async ({ page }) => {
    await login(page)

    const row = page.getByTestId(`project-row-${projectId}`)
    await expect(row).toBeVisible()
    // The label and the name are separate flex children, so textContent has no
    // whitespace between them ("Created by:Olivia Owner").
    await expect(row).toContainText(new RegExp(`Created by:\\s*${OWNER_NAME}`))

    // Filtering by the creator keeps the row (server-side `created_by`).
    await page.goto(`/projects?created_by=${meId}`)
    await expect(page.getByTestId(`project-row-${projectId}`)).toBeVisible()

    // Filtering by somebody who created nothing drops it.
    await page.goto(`/projects?created_by=${otherId}`)
    await expect(page.getByTestId('project-filter-bar')).toBeVisible()
    await expect(page.getByTestId(`project-row-${projectId}`)).toHaveCount(0)
  })

  test('project page shows the creator above the tabs, on every tab', async ({ page }) => {
    await login(page)

    for (const path of ['', '/files', '/settings', '/history']) {
      await page.goto(`/projects/${projectId}${path}`)
      await expect(page.getByTestId('project-panel')).toBeVisible()
      await expect(page.getByTestId('project-created-by')).toHaveText(new RegExp(`^Created by:\\s*${OWNER_NAME}$`))
    }
  })

  test('TM list shows the creator and filters by them', async ({ page }) => {
    await login(page)
    await page.goto('/memories')

    const row = page.getByTestId(`tm-row-${tmId}`)
    await expect(row).toBeVisible()
    await expect(row).toContainText(OWNER_NAME)

    await page.goto(`/memories?created_by=${meId}`)
    await expect(page.getByTestId(`tm-row-${tmId}`)).toBeVisible()

    await page.goto(`/memories?created_by=${otherId}`)
    await expect(page.getByTestId('tm-filter-bar')).toBeVisible()
    await expect(page.getByTestId(`tm-row-${tmId}`)).toHaveCount(0)
  })

  test('term base list shows the creator and filters by them', async ({ page }) => {
    await login(page)
    await page.goto('/terminology')

    const row = page.getByTestId(`term-base-row-${tbId}`)
    await expect(row).toBeVisible()
    await expect(row).toContainText(OWNER_NAME)

    await page.goto(`/terminology?created_by=${meId}`)
    await expect(page.getByTestId(`term-base-row-${tbId}`)).toBeVisible()

    await page.goto(`/terminology?created_by=${otherId}`)
    await expect(page.getByTestId('term-base-filter-bar')).toBeVisible()
    await expect(page.getByTestId(`term-base-row-${tbId}`)).toHaveCount(0)
  })

  // The "Created by" chip offers only members who created a row:
  // a colleague who created nothing, or a leftover
  // j*-member test account, would be an option that can only return an
  // empty list.
  test('the "Created by" chip offers only actual creators, not every member', async ({ page }) => {
    await login(page)

    // The seeded linguist is a member of this org and has created no project.
    const members = await page.request.get('/api/members')
    expect(members.ok()).toBe(true)
    const memberEmails: string[] = (await members.json()).data.map((m: { email: string }) => m.email)
    expect(memberEmails, 'seed must include the vendor member').toContain(OTHER_EMAIL)

    const creators = await page.request.get('/api/projects/creators')
    expect(creators.ok()).toBe(true)
    const creatorIds: number[] = (await creators.json()).data.map((c: { id: number }) => c.id)
    expect(creatorIds, 'the test project makes its author a creator').toContain(meId)
    expect(creatorIds, 'a member who created nothing is not a creator').not.toContain(otherId)

    // And the popover reflects that: the non-creator member is absent.
    await page.getByTestId('filter-created-by').click()
    await expect(page.getByRole('button', { name: 'Me', exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: SEED_VENDOR_NAME, exact: true })).toHaveCount(0)
  })

  test('the "Created by" chip writes the filter into the URL', async ({ page }) => {
    await login(page)

    await page.getByTestId('filter-created-by').click()
    const meOption = page.getByRole('button', { name: 'Me', exact: true })
    await expect(meOption).toBeVisible()
    await meOption.click()

    await expect(page).toHaveURL(new RegExp(`created_by=${meId}\\b`))
    await expect(page.getByTestId('filter-created-by')).toContainText('Me')
    await expect(page.getByTestId(`project-row-${projectId}`)).toBeVisible()
  })
})
