// Project metadata journey: pick a customer label value on /projects/new,
// publish, change the status from the detail page, see the new status in the
// list, then filter the list by status and keep the filter across a reload.

import { test, expect } from '@playwright/test'
import {
  authHeaders,
  login,
  pickComboboxOption,
  pickComboboxValue,
  selectTargetLanguages,
  setDeadline,
  startFromScratch,
  submitProjectWizard,
  systemLabelsViaAPI,
  addLabelValueViaAPI,
  projectLabelsViaAPI,
} from './helpers'

const TS = Date.now()
const CUSTOMER_NAME = `J25 Customer ${TS}`
const PROJECT_NAME = `J25 Project ${TS}`

test.describe('Project metadata flow', () => {
  let headers: Record<string, string> = {}

  test.beforeEach(async ({ page }) => {
    await login(page)
    headers = await authHeaders(page)
  })

  // Cleanup registry, worker-local by construction: each parallel worker loads
  // this module separately, so a test only ever cleans up what it created
  // itself. A hook that swept /api/projects by name instead would delete a
  // sibling test's project mid-run — the tests in this file run concurrently.
  // The hook deletes with the headers read after login and the `request`
  // fixture, best-effort: it reads nothing from the page.
  const createdProjects: number[] = []

  test.afterEach(async ({ request }) => {
    while (createdProjects.length) {
      await request.delete(`/api/projects/${createdProjects.pop()}`, { headers }).catch(() => {})
    }
    // Label values are not cleaned up: one a project used cannot be retired,
    // and addLabelValueViaAPI resolves an existing value on a repeat run.
  })

  test('pick a customer label + project → edit metadata → list cross-page invalidates → filter persists', async ({ page }) => {
    const { customer: customerLabelId } = await systemLabelsViaAPI(page)
    await addLabelValueViaAPI(page, customerLabelId, CUSTOMER_NAME)

    // ── 1. Pick a customer label value on /projects/new ──────────────────
    // Navigate via SPA link so the admin-only route guard sees the role.
    await page.getByRole('link', { name: 'New project' }).click()
    await expect(page).toHaveURL(/\/projects\/new/)
    await startFromScratch(page)

    // Fill name + target language (en is default source per the schema).
    await page.getByPlaceholder('Project name').fill(PROJECT_NAME)

    // Open Target languages combobox + pick French. Use the data-field hook
    // (stable across CSS reshuffles).
    await page.locator('[data-field="target_languages"] button').click()
    await page.getByRole('option', { name: /French/i }).first().click()
    // Close the popover via Escape (outside-clicks get intercepted by the
    // popover backdrop in cmdk).
    await page.keyboard.press('Escape')

    // Customer is a built-in label: its values are defined
    // on the Labels page, and the form picks one.
    await pickComboboxOption(page, page.getByTestId(`project-label-value-${customerLabelId}`), CUSTOMER_NAME)

    // Submit. The wizard has been autosaving a draft; "Create project"
    // publishes it and redirects to /projects/{id}.
    const { response: createResp } = await submitProjectWizard(page)
    expect(createResp.status()).toBe(200)
    const createBody = await createResp.json()
    const projectID = createBody.data.id
    createdProjects.push(projectID)
    expect(createBody.data.status).toBe('in_progress')
    expect(await projectLabelsViaAPI(page, projectID)).toMatchObject({ Customer: CUSTOMER_NAME })
    await expect(page).toHaveURL(new RegExp(`/projects/${projectID}`))

    // ── 2. From the detail page, change status to "on_hold" ────────────
    await expect(page.getByTestId('project-metadata-strip')).toBeVisible()
    // The header pill mirrors the list-row pill, so opening a project shows
    // its lifecycle state at a glance. Managers see a select in the strip, so
    // this pill is the page's only one.
    await expect(page.getByTestId('project-status-badge-in_progress')).toBeVisible()
    // Draft is the wizard's autosave state: a project leaves it through publish
    // and never goes back, so the dropdown must not offer
    // it. The three live statuses are the positive control — without them this
    // would also pass if the popover simply never opened.
    const statusPopover = page.locator('[data-slot="popover-content"]')
    await expect(statusPopover).toHaveCount(0)
    await page.getByTestId('project-status-select').click()
    await expect(statusPopover).toHaveCount(1)
    for (const s of ['in_progress', 'completed', 'on_hold']) {
      await expect(statusPopover.getByTestId(`project-status-select-option-${s}`)).toBeVisible()
    }
    await expect(
      statusPopover.getByTestId('project-status-select-option-draft'),
      'a created project cannot be sent back to draft',
    ).toHaveCount(0)
    await page.keyboard.press('Escape')
    await expect(statusPopover).toHaveCount(0)

    const patchPromise = page.waitForResponse(r => r.url().endsWith(`/api/projects/${projectID}`) && r.request().method() === 'PATCH')
    await pickComboboxValue(page, 'project-status-select', 'on_hold')
    const patchResp = await patchPromise
    expect(patchResp.status()).toBe(200)
    // …and it tracks edits made from the metadata strip.
    await expect(page.getByTestId('project-status-badge-on_hold')).toBeVisible()

    // ── 3. Cross-page state-after-edit guard ────────────────────────────
    // Pins that the list shows the new status ('on_hold'), not the original
    // ('in_progress'), after navigating back. The list refetches on mount
    // (staleTime 0), so this does not by itself prove the explicit
    // invalidation in ProjectDetail's updateMetaMut.
    // The breadcrumb also links to Projects; target the sidebar navigation.
    await page.getByRole('complementary').getByRole('link', { name: 'Projects', exact: true }).click()
    await expect(page).toHaveURL(/\/$|\/projects$/)
    const row = page.locator(`[data-testid="project-row-${projectID}"]`)
    await expect(row).toBeVisible()
    await expect(row.getByTestId('project-status-badge-on_hold')).toBeVisible()

    // ── 4. Apply a filter chip → URL updates → list filters ──────────────
    // Status filter to "Draft" → our project (on_hold) drops out of the list.
    await page.getByTestId('filter-status').click()
    await page.getByRole('button', { name: 'Draft', exact: true }).click()
    await expect(page).toHaveURL(/status=draft/)
    await expect(row).toBeHidden()

    // ── 5. Hard reload → URL-backed filter persists ──────────────────────
    await page.reload()
    await expect(page).toHaveURL(/status=draft/)
    await expect(page.getByTestId('filter-status')).toContainText('Draft')

    // Clear filters → row reappears.
    await page.getByTestId('filter-clear-all').click()
    await expect(page).toHaveURL(/^http:[^?]*\/?$/)
    await expect(row).toBeVisible()
  })

  // The deadline picker holds edits while its popover is open and PATCHes once
  // on close, in UTC. Picking the day and then typing the time are two edits —
  // the time is not the picker's 17:00 default, so typing it really changes the
  // value — and a picker that saved on every change would send two PATCHes here.
  //
  // Anti-tautology: making DeadlinePicker commit on every onChange (instead of
  // on close) makes this test fail with more than one PATCH.
  test('deadline edit PATCHes once, in UTC, when the picker closes', async ({ page }) => {
    // Create a fresh project via API so we don't depend on seed.
    const created = await page.request.post('/api/projects', {
      data: {
        name: PROJECT_NAME + ' (deadline buffer)',
        source_language: 'en',
        target_languages: ['fr'],
      },
    })
    expect(created.ok()).toBe(true)
    const projectId = (await created.json()).data.id
    createdProjects.push(projectId)

    await page.goto(`/projects/${projectId}/files`)
    const trigger = page.getByTestId('project-deadline-edit')
    await expect(trigger).toBeVisible()

    const patches: Array<{ url: string; body: any }> = []
    page.on('request', (req) => {
      if (req.method() === 'PATCH' && req.url().includes(`/api/projects/${projectId}`)) {
        patches.push({ url: req.url(), body: req.postDataJSON?.() })
      }
    })

    const patchPromise = page.waitForResponse(
      (r) => r.url().includes(`/api/projects/${projectId}`) && r.request().method() === 'PATCH',
    )
    await setDeadline(page, trigger, '2026-09-15T10:30')
    await patchPromise
    expect(patches.length).toBe(1)
    expect(patches[0].body.deadline).toBe(new Date('2026-09-15T10:30').toISOString())
    await expect(trigger).toHaveAttribute('data-value', new Date('2026-09-15T10:30').toISOString())
  })

  // Domain is editable on an existing project. Domain is a built-in label, so it is a pick: one write, and it survives a reload.
  test('picking a domain on an existing project saves once and persists', async ({ page }) => {
    const { domain: domainLabelId } = await systemLabelsViaAPI(page)
    await addLabelValueViaAPI(page, domainLabelId, 'Patents')

    const created = await page.request.post('/api/projects', {
      data: {
        name: PROJECT_NAME + ' (domain edit)',
        source_language: 'en',
        target_languages: ['fr'],
      },
    })
    expect(created.ok()).toBe(true)
    const projectId = (await created.json()).data.id
    createdProjects.push(projectId)

    await page.goto(`/projects/${projectId}/files`)
    const field = page.getByTestId(`project-label-value-${domainLabelId}`)
    await expect(field).toBeVisible()
    await expect(field).toContainText('No domain') // unanswered

    const writes: string[] = []
    page.on('request', (req) => {
      if (req.method() === 'PUT' && req.url().includes(`/api/projects/${projectId}/labels`)) {
        writes.push(req.url())
      }
    })

    const saved = page.waitForResponse(
      (r) => r.url().includes(`/api/projects/${projectId}/labels`) && r.request().method() === 'PUT',
    )
    await pickComboboxOption(page, field, 'Patents')
    expect((await saved).status()).toBe(200)
    // One pick, one write — no save-per-keystroke equivalent.
    expect(writes.length).toBe(1)

    // Persist across reload (proves the write stored it + the page reads it).
    await page.reload()
    await expect(page.getByTestId(`project-label-value-${domainLabelId}`)).toContainText('Patents')
    expect(await projectLabelsViaAPI(page, projectId)).toMatchObject({ Domain: 'Patents' })
  })

  test('a long project name truncates, keeps the header status pill on screen, and shows the full name on hover', async ({ page }) => {
    // Unbreakable: no spaces, so nothing can wrap it — the title has to
    // truncate or it pushes the rest of the breadcrumb off the viewport.
    const longName = `${PROJECT_NAME.replace(/ /g, '')}${'x'.repeat(180)}`
    const create = (name: string) =>
      page.request.post('/api/projects', {
        data: { name, source_language: 'en', target_languages: ['fr'], status: 'on_hold' },
      })
    // Inter loads with font-display: swap, so text first renders in the
    // fallback font, a few px narrower. Both widths are read in Inter.
    const fontsLoaded = () => page.evaluate(async () => {
      await document.fonts.load('14px "Inter Variable"')
      await document.fonts.ready
    })
    const created = await create(longName)
    expect(created.ok()).toBe(true)
    const projectId = (await created.json()).data.id
    createdProjects.push(projectId)
    // Reference page for the ancestor-crumb width comparison below.
    const short = await create(`${PROJECT_NAME} baseline`)
    expect(short.ok()).toBe(true)
    const shortId = (await short.json()).data.id
    createdProjects.push(shortId)

    await page.goto(`/projects/${projectId}/files`)
    const viewport = page.viewportSize()!

    // The status pill lives on its own row below the breadcrumb, so the name
    // can't displace it — this is the feature, not the overflow guard.
    await expect(page.getByTestId('project-status-badge-on_hold')).toBeVisible()

    // The overflow guard. Two independent failure modes:
    //   - name doesn't truncate  → the crumb paints past the nav's box
    //   - ancestors can shrink   → "Projects /" collapses into the name
    const nav = page.getByRole('navigation', { name: 'Breadcrumb' })
    const overflow = await nav.evaluate((el) => el.scrollWidth - el.clientWidth)
    expect(overflow, 'breadcrumb must not scroll horizontally').toBeLessThanOrEqual(1)

    // …and the rename affordance, which sits after the name, has to survive
    // on screen. A truncating name that still pushes the pencil out is a
    // half-fix, and nav.scrollWidth alone wouldn't catch it.
    const pencil = (await page.getByTestId('rename-project').boundingBox())!
    expect(pencil.x + pencil.width, 'rename pencil must stay in the viewport')
      .toBeLessThanOrEqual(viewport.width)

    // The ancestor crumb must not pay for the name's length. Compared against
    // the same link on a short-name page rather than a fixed bound: the
    // squeeze is only ~10-20px, which any loose threshold would sleep through.
    // Measure the <li>, not the <a> inside it: the link keeps its own width
    // and overflows when the crumb is squeezed, so only the item's box moves.
    await fontsLoaded()
    const ancestor = (await nav.locator('li').first().boundingBox())!

    // Hovering the clipped title is the way back to the full name…
    const trigger = nav.locator('[data-slot="tooltip-trigger"]')
    await trigger.hover()
    await expect(page.locator('[data-slot="tooltip-content"]')).toHaveText(longName)

    // …and so is tabbing to it, for anyone not driving a mouse.
    await page.mouse.move(0, 400)
    await expect(page.locator('[data-slot="tooltip-content"]')).toHaveCount(0)
    await trigger.focus()
    await expect(page.locator('[data-slot="tooltip-content"]')).toHaveText(longName)

    // Baseline for the ancestor width, measured last so the navigation can't
    // race the assertions above.
    await page.goto(`/projects/${shortId}/files`)
    await fontsLoaded()
    const baseline = (await page.getByRole('navigation', { name: 'Breadcrumb' })
      .locator('li').first().boundingBox())!
    expect(ancestor.width, '"Projects" crumb must keep its width next to a long name')
      .toBeCloseTo(baseline.width, 0)
  })

  // The auto-complete option is on by default, settable in
  // the create wizard's settings editor, and editable from the project's
  // Settings tab. It lives on the settings profile, so both surfaces are the
  // shared SettingsEditor and the writes are the create POST + settings PATCH.
  test('auto-complete option: on by default, unchecked at create, toggled back on', async ({ page }) => {
    await page.getByRole('link', { name: 'New project' }).click()
    await expect(page).toHaveURL(/\/projects\/new/)
    await startFromScratch(page)

    await page.getByPlaceholder('Project name').fill(PROJECT_NAME + ' auto-complete')
    await selectTargetLanguages(page, page.locator('[data-field="target_languages"] button').first(), ['fr'])

    // On by default, matching the column default.
    const createCheckbox = page.getByTestId('settings-auto-complete')
    await expect(createCheckbox).toBeChecked()
    await createCheckbox.uncheck()

    const { response: createResp, id: projectID } = await submitProjectWizard(page)
    expect(createResp.status()).toBe(200)
    createdProjects.push(projectID)
    await expect(page).toHaveURL(new RegExp(`/projects/${projectID}`))

    // Editable from the Settings tab, and the unchecked box persisted rather
    // than falling back to the default.
    await page.getByRole('link', { name: /settings/i }).click()
    const settingsCheckbox = page.getByTestId('settings-auto-complete')
    await expect(settingsCheckbox).not.toBeChecked()

    // Toggling it back on round-trips through the settings PATCH. Plain click
    // rather than check(): the box is driven by server state, so it only
    // flips once the PATCH resolves and the settings query refetches —
    // check() asserts the state changed synchronously and fails.
    const patchPromise = page.waitForResponse(r => r.url().endsWith(`/api/projects/${projectID}/settings`) && r.request().method() === 'PATCH')
    await settingsCheckbox.click()
    expect((await patchPromise).status()).toBe(200)
    await expect(settingsCheckbox).toBeChecked()

    // Survives a reload — the value is persisted, not just local state. The
    // tab is URL-backed, so the reload lands back on Settings.
    await page.reload()
    await expect(page.getByTestId('settings-auto-complete')).toBeChecked()
  })

  test('a short project name gets no redundant title tooltip', async ({ page }) => {
    const created = await page.request.post('/api/projects', {
      data: { name: PROJECT_NAME + ' short', source_language: 'en', target_languages: ['fr'] },
    })
    expect(created.ok()).toBe(true)
    const projectId = (await created.json()).data.id
    createdProjects.push(projectId)

    await page.goto(`/projects/${projectId}/files`)
    const nav = page.getByRole('navigation', { name: 'Breadcrumb' })
    await expect(nav).toContainText(PROJECT_NAME + ' short')
    await nav.locator('[data-slot="tooltip-trigger"]').hover()
    // Give the tooltip's open delay time to elapse before asserting absence.
    await page.waitForTimeout(800)
    await expect(page.locator('[data-slot="tooltip-content"]')).toHaveCount(0)
  })
})
