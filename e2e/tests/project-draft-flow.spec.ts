// Draft projects: the New Project wizard autosaves a
// status='draft' project as the form is filled, the project list reopens a
// draft in the wizard, and "Create project" publishes it.
//
// Every assertion waits on the request it depends on (waitForResponse), never
// on a timer — the autosave is debounced, so a sleep would be a guess.

import { test, expect } from '@playwright/test'
import { login, selectTargetLanguages, startFromScratch, startFromTemplate, submitProjectWizard } from './helpers'

const TS = Date.now()

/** Mirrors DRAFT_SAVE_DELAY_MS in web/src/hooks/useProjectDraft.ts. */
const DRAFT_SAVE_DELAY_MS = 800

test.describe('Draft projects', () => {
  test.beforeEach(async ({ page }) => {
    await login(page)
  })

  // Worker-local cleanup registry: each parallel worker loads the module
  // separately, so a test only removes what it created itself.
  const createdProjects: number[] = []
  test.afterEach(async ({ page }) => {
    while (createdProjects.length) {
      await page.request.delete(`/api/projects/${createdProjects.pop()}`)
    }
  })

  test('typing a name saves a draft; reload and the list bring it back; Create publishes it', async ({ page }) => {
    test.setTimeout(30_000)
    const name = `E2E Draft ${TS}`

    // Count every create the page makes, so the negative assertion below is
    // about requests and not about a label that has yet to change.
    const creates: string[] = []
    page.on('request', r => {
      if (r.url().endsWith('/api/projects') && r.method() === 'POST') creates.push(r.url())
    })

    await page.getByRole('link', { name: 'New project' }).click()
    await expect(page).toHaveURL(/\/projects\/new$/)
    await startFromScratch(page)

    // An untouched wizard saves nothing. The mount-time work that would betray
    // a wrongly-wired autosave has to have happened first: the step library
    // arrives and seeds the workflow, and the settings editor reports its own
    // draft. Then the debounce window has to elapse — `data-state` stays 'idle'
    // while a save is merely PENDING, so only the absence of a request proves
    // it. This is the negative-assertion exception to "never wait on a timer":
    // there is no event to wait for when the correct behaviour is silence.
    await expect(page.getByTestId('use-workflow-select')).toBeVisible()
    await page.waitForTimeout(DRAFT_SAVE_DELAY_MS + 700)
    expect(creates, 'an untouched wizard must not create a draft').toHaveLength(0)
    await expect(page).toHaveURL(/\/projects\/new$/)
    await expect(page.getByTestId('draft-save-state')).toHaveAttribute('data-state', 'idle')
    // …and no Draft badge: there is no draft yet.
    await expect(page.getByTestId('project-status-badge-draft')).toHaveCount(0)

    // ── 1. First edit → the draft row exists before anything is submitted.
    const nameInput = page.getByPlaceholder('Project name')
    const [createResp] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith('/api/projects') && r.request().method() === 'POST'),
      (async () => {
        await nameInput.fill(name)
        // Leaving the field saves right away (no debounce wait).
        await nameInput.blur()
      })(),
    ])
    expect(createResp.status()).toBe(201)
    const created = await createResp.json()
    const draftId: number = created.data.id
    createdProjects.push(draftId)
    expect(created.data.status).toBe('draft')
    expect(created.data.name).toBe(name)
    await expect(page).toHaveURL(new RegExp(`/projects/new\\?draft=${draftId}$`))
    await expect(page.getByTestId('draft-save-state')).toHaveAttribute('data-state', 'saved')
    // The page now wears the same grey Draft chip the project list row does.
    await expect(page.getByTestId('project-status-badge-draft')).toHaveText('Draft')

    // ── 2. A later change replays the form with PUT /draft.
    const [saveResp] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith(`/api/projects/${draftId}/draft`) && r.request().method() === 'PUT'),
      selectTargetLanguages(page, page.locator('[data-field="target_languages"] button').first(), ['fr']),
    ])
    expect(saveResp.status()).toBe(200)
    expect((await saveResp.json()).data.target_languages).toEqual(['fr'])

    // ── 3. Reload: the wizard resumes the draft, form included.
    await page.reload()
    await expect(page.getByRole('heading', { name: /new project/i })).toBeVisible()
    await expect(nameInput).toHaveValue(name)
    await expect(page.locator('[data-field="target_languages"]')).toContainText('FR')
    // Resuming is not an edit: nothing is saved until the user changes something.
    await expect(page.getByTestId('draft-save-state')).toHaveAttribute('data-state', 'idle')
    // But the badge is back with the draft: it tracks the row, not the save.
    await expect(page.getByTestId('project-status-badge-draft')).toHaveText('Draft')

    // ── 4. The list shows the draft and reopens it in the wizard.
    await page.getByRole('complementary').getByRole('link', { name: 'Projects', exact: true }).click()
    const row = page.getByTestId(`project-row-${draftId}`)
    await expect(row).toBeVisible()
    await expect(row.getByTestId('project-status-badge-draft')).toBeVisible()
    // A half-filled draft is no source for Duplicate. The seeded Demo Project
    // in the same list is the positive control: without it this assertion
    // would also pass if the action were renamed or dropped everywhere.
    const liveRow = page.locator('[data-testid^="project-row-"]')
      .filter({ has: page.getByTestId('project-status-badge-in_progress') }).first()
    await expect(liveRow.getByRole('button', { name: 'Duplicate' })).toBeVisible()
    await expect(row.getByTestId(`duplicate-project-${draftId}`)).toHaveCount(0)
    await expect(row.getByRole('button', { name: 'Duplicate' })).toHaveCount(0)
    await row.click()
    await expect(page).toHaveURL(new RegExp(`/projects/new\\?draft=${draftId}$`))
    await expect(nameInput).toHaveValue(name)

    // ── 5. A file goes into the draft — stored, no jobs.
    const [uploadResp] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith(`/api/projects/${draftId}/files`) && r.request().method() === 'POST'),
      page.locator('[data-testid="create-file-dropzone"] input[type="file"]').setInputFiles({
        name: 'draft-upload.json',
        mimeType: 'application/json',
        buffer: Buffer.from('{"greeting":"Hello world","farewell":"Goodbye"}'),
      }),
    ])
    expect(uploadResp.status()).toBe(201)
    await expect(page.getByTestId('create-file-list')).toContainText('draft-upload.json')
    const draftJobs = await page.request.get(`/api/projects/${draftId}/jobs`)
    expect(((await draftJobs.json()).data ?? []).length, 'a draft has no jobs').toBe(0)

    // ── 5b. A file dropped in by mistake comes back out, from the form. This
    //        is the UI half of the manager-level delete-while-drafting gate
    //        (the API half is pinned in internal/file's integration tests).
    const [strayResp] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith(`/api/projects/${draftId}/files`) && r.request().method() === 'POST'),
      page.locator('[data-testid="create-file-dropzone"] input[type="file"]').setInputFiles({
        name: 'dropped-by-mistake.json',
        mimeType: 'application/json',
        buffer: Buffer.from('{"oops":"wrong file"}'),
      }),
    ])
    const strayId: number = (await strayResp.json()).data.id
    await expect(page.getByTestId(`create-file-${strayId}`)).toBeVisible()
    const [removeResp] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith(`/api/projects/${draftId}/files/${strayId}`) && r.request().method() === 'DELETE'),
      page.getByRole('button', { name: 'Remove dropped-by-mistake.json' }).click(),
    ])
    expect(removeResp.status()).toBe(204)
    await expect(page.getByTestId(`create-file-${strayId}`)).toHaveCount(0)
    await expect(page.getByTestId('create-file-list')).toContainText('draft-upload.json')

    // ── 6. Create project publishes the same row and lands on it.
    const { response: publishResp, id } = await submitProjectWizard(page)
    expect(publishResp.status()).toBe(200)
    expect(id).toBe(draftId)
    expect((await publishResp.json()).data.status).toBe('in_progress')
    // Tasks, or Files while the upload has produced no jobs yet.
    await expect(page).toHaveURL(new RegExp(`/projects/${draftId}(/files)?$`))
    await expect(page.getByTestId('project-status-badge-in_progress')).toBeVisible()

    // The draft's file got its job at publish.
    const jobs = await page.request.get(`/api/projects/${draftId}/jobs`)
    const jobRows = (await jobs.json()).data ?? []
    expect(jobRows.map((j: { target_language: string }) => j.target_language)).toEqual(['fr'])

    // Published: the row now opens the project page, and the wizard route is
    // done with it.
    const late = await page.request.put(`/api/projects/${draftId}/draft`, { data: { name: 'late' } })
    expect(late.status()).toBe(404)
  })

  // Choosing how to start is not filling the form in: neither "From
  // scratch" nor picking a template creates a draft.
  test('picking a template does not create a draft; the first real edit does, template included', async ({ page }) => {
    const creates: string[] = []
    page.on('request', r => {
      if (r.url().endsWith('/api/projects') && r.method() === 'POST') creates.push(r.url())
    })

    await page.goto('/projects/new')
    await expect(page.getByRole('heading', { name: /new project/i })).toBeVisible()
    // The template's own fetch is what the negative window has to outlast:
    // handleTemplateChange sets the template id BEFORE awaiting getTemplate, so
    // the form and the "Using template" caption both appear while the data is
    // still in flight. Waiting on the response first means the debounce below
    // starts after every autosave the pick could possibly trigger.
    const [templateResp] = await Promise.all([
      page.waitForResponse(r => /\/api\/templates\/\d+$/.test(r.url()) && r.request().method() === 'GET'),
      startFromTemplate(page, 'Default Template'),
    ])
    expect(templateResp.ok()).toBe(true)
    // …and on the template's data actually being applied to the form, which is
    // the last thing that could schedule a save.
    await expect(page.getByTestId('use-workflow-select')).toBeVisible()
    await expect(page.locator('[data-field="target_languages"]').getByTestId(/^lang-remove-/).locator('visible=true')).not.toHaveCount(0)

    await page.waitForTimeout(DRAFT_SAVE_DELAY_MS + 700)
    expect(creates, 'picking a template must not create a draft').toHaveLength(0)
    await expect(page).toHaveURL(/\/projects\/new$/)
    await expect(page.getByTestId('draft-save-state')).toHaveAttribute('data-state', 'idle')

    // The user's own first edit creates it, and the draft carries the template
    // the form was filled from — the pick was not lost, only deferred.
    const name = `E2E Template Draft ${TS}`
    const nameInput = page.getByPlaceholder('Project name')
    const [createResp] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith('/api/projects') && r.request().method() === 'POST'),
      (async () => {
        await nameInput.fill(name)
        await nameInput.blur()
      })(),
    ])
    expect(createResp.status()).toBe(201)
    const created = (await createResp.json()).data
    createdProjects.push(created.id)
    expect(created.status).toBe('draft')
    expect(created.template_id, 'the draft remembers the template it was started from').toBeTruthy()
    // Exactly one create in the whole test: a late save from the template pick
    // would otherwise be absorbed by the waitForResponse above and go unseen.
    expect(creates, 'the edit created one draft, the template pick none').toHaveLength(1)
  })

  // The project page cannot render a draft: its Status dropdown has no Draft
  // option to match, so the field would read "Select…". A draft is edited in
  // the wizard, which is where the list links it — a pasted or bookmarked
  // /projects/{id} goes to the same place.
  test('opening a draft at its project URL lands in the wizard, not the project page', async ({ page }) => {
    const name = `E2E Draft URL ${TS}`
    await page.goto('/projects/new')
    await startFromScratch(page)

    const nameInput = page.getByPlaceholder('Project name')
    const [createResp] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith('/api/projects') && r.request().method() === 'POST'),
      (async () => {
        await nameInput.fill(name)
        await nameInput.blur()
      })(),
    ])
    const draftId: number = (await createResp.json()).data.id
    createdProjects.push(draftId)
    // Publishable below needs a target language too.
    await Promise.all([
      page.waitForResponse(r => r.url().endsWith(`/api/projects/${draftId}/draft`) && r.request().method() === 'PUT'),
      selectTargetLanguages(page, page.locator('[data-field="target_languages"] button').first(), ['fr']),
    ])

    await page.goto(`/projects/${draftId}`)
    await expect(page).toHaveURL(new RegExp(`/projects/new\\?draft=${draftId}$`))
    await expect(nameInput).toHaveValue(name)

    // The positive control: a live project's URL still opens the project page.
    const publish = await page.request.post(`/api/projects/${draftId}/publish`, { data: { analyze: false } })
    expect(publish.status()).toBe(200)
    await page.goto(`/projects/${draftId}`)
    // No files, so the project page opens on its Files tab.
    await expect(page).toHaveURL(new RegExp(`/projects/${draftId}/files$`))
    await expect(page.getByTestId('project-status-badge-in_progress')).toBeVisible()
  })

  // "Discard" means the draft is gone, not emptied: an abandoned start
  // must not leave an "Untitled draft" behind in the project list.
  test('Discard deletes the draft and the wizard begins again clean', async ({ page }) => {
    const name = `E2E Start Over ${TS}`
    await page.goto('/projects/new')
    await startFromScratch(page)

    const nameInput = page.getByPlaceholder('Project name')
    const [createResp] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith('/api/projects') && r.request().method() === 'POST'),
      (async () => {
        await nameInput.fill(name)
        await nameInput.blur()
      })(),
    ])
    const draftId: number = (await createResp.json()).data.id
    // Registered even though this test deletes it: if the discard is what
    // breaks, the afterEach is the only thing left to clean up (its 404 on the
    // happy path is harmless).
    createdProjects.push(draftId)
    await expect(page).toHaveURL(new RegExp(`/projects/new\\?draft=${draftId}$`))

    // A file in the draft: the warning has to count what it is about to take,
    // and the count has to read as English on both sides of the plural.
    await Promise.all([
      page.waitForResponse(r => r.url().endsWith(`/api/projects/${draftId}/files`) && r.request().method() === 'POST'),
      page.locator('[data-testid="create-file-dropzone"] input[type="file"]').setInputFiles({
        name: 'discard-me.json',
        mimeType: 'application/json',
        buffer: Buffer.from('{"greeting":"Hello world"}'),
      }),
    ])
    await page.getByTestId('discard').click()
    const oneFile = page.getByRole('alertdialog')
    await expect(oneFile).toContainText('the file you have uploaded into it')
    await oneFile.getByRole('button', { name: 'Cancel' }).click()
    await expect(oneFile).toHaveCount(0)

    await Promise.all([
      page.waitForResponse(r => r.url().endsWith(`/api/projects/${draftId}/files`) && r.request().method() === 'POST'),
      page.locator('[data-testid="create-file-dropzone"] input[type="file"]').setInputFiles({
        name: 'discard-me-too.json',
        mimeType: 'application/json',
        buffer: Buffer.from('{"farewell":"Goodbye"}'),
      }),
    ])
    await page.getByTestId('discard').click()
    const confirm = page.getByRole('alertdialog')
    await expect(confirm).toContainText('the 2 files you have uploaded into it')
    const [deleteResp] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith(`/api/projects/${draftId}`) && r.request().method() === 'DELETE'),
      confirm.getByRole('button', { name: 'Discard' }).click(),
    ])
    expect(deleteResp.status()).toBe(204)

    // The wizard is back to its first screen and holds no draft — badge included.
    await expect(page.getByTestId('start-from')).toBeVisible()
    await expect(page).toHaveURL(/\/projects\/new$/)
    await expect(page.getByTestId('project-status-badge-draft')).toHaveCount(0)
    const gone = await page.request.get(`/api/projects/${draftId}`)
    expect(gone.status(), 'the draft is deleted, not emptied').toBe(404)
    const orphanedFiles = await page.request.get(`/api/projects/${draftId}/files`)
    expect(orphanedFiles.status(), "the draft's files went with it").toBe(404)

    // And the next start is a NEW draft, not a resurrection of the old id.
    await startFromScratch(page)
    const [secondResp] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith('/api/projects') && r.request().method() === 'POST'),
      (async () => {
        await nameInput.fill(`${name} second`)
        await nameInput.blur()
      })(),
    ])
    const secondId: number = (await secondResp.json()).data.id
    createdProjects.push(secondId)
    expect(secondId).not.toBe(draftId)
  })

  test('Cancelling Discard keeps the draft and the form', async ({ page }) => {
    const name = `E2E Start Over Cancel ${TS}`
    await page.goto('/projects/new')
    await startFromScratch(page)

    const nameInput = page.getByPlaceholder('Project name')
    const [createResp] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith('/api/projects') && r.request().method() === 'POST'),
      (async () => {
        await nameInput.fill(name)
        await nameInput.blur()
      })(),
    ])
    const draftId: number = (await createResp.json()).data.id
    createdProjects.push(draftId)

    const deletes: string[] = []
    page.on('request', r => {
      if (r.url().endsWith(`/api/projects/${draftId}`) && r.method() === 'DELETE') deletes.push(r.url())
    })

    await page.getByTestId('discard').click()
    const confirm = page.getByRole('alertdialog')
    await expect(confirm).toBeVisible()
    await confirm.getByRole('button', { name: 'Cancel' }).click()
    await expect(confirm).toHaveCount(0)

    // Nothing was thrown away: same draft, same form, still on the form screen.
    expect(deletes, 'cancelling must not delete the draft').toHaveLength(0)
    await expect(nameInput).toHaveValue(name)
    await expect(page).toHaveURL(new RegExp(`/projects/new\\?draft=${draftId}$`))
    const still = await page.request.get(`/api/projects/${draftId}`)
    expect(still.status()).toBe(200)
  })

  // Discard and the Draft chip both mean "there is a saved draft". Neither is
  // offered on a form that has not saved one — there is nothing to remove.
  test('Discard and the Draft chip appear only once a draft exists', async ({ page }) => {
    await page.goto('/projects/new')
    await startFromScratch(page)
    await expect(page.getByTestId('discard')).toHaveCount(0)
    await expect(page.getByTestId('project-status-badge-draft')).toHaveCount(0)

    const nameInput = page.getByPlaceholder('Project name')
    const [createResp] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith('/api/projects') && r.request().method() === 'POST'),
      (async () => { await nameInput.fill(`E2E Discard Gate ${TS}`); await nameInput.blur() })(),
    ])
    createdProjects.push((await createResp.json()).data.id)
    await expect(page.getByTestId('discard')).toBeVisible()
    await expect(page.getByTestId('project-status-badge-draft')).toBeVisible()
  })

  test('Create project on a draft without a name is refused and the draft stays a draft', async ({ page }) => {
    await page.goto('/projects/new')
    await expect(page.getByRole('heading', { name: /new project/i })).toBeVisible()
    await startFromScratch(page)

    // Only a target language: enough to autosave, not enough to publish.
    const [createResp] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith('/api/projects') && r.request().method() === 'POST'),
      selectTargetLanguages(page, page.locator('[data-field="target_languages"] button').first(), ['de']),
    ])
    expect(createResp.status()).toBe(201)
    const draftId: number = (await createResp.json()).data.id
    createdProjects.push(draftId)

    // The form's own validation stops the click before any request.
    await page.getByRole('button', { name: /^create project$/i }).click()
    await expect(page.getByText('Project name is required')).toBeVisible()
    await expect(page).toHaveURL(new RegExp(`/projects/new\\?draft=${draftId}$`))

    // And the server refuses a publish that skips the form.
    const publish = await page.request.post(`/api/projects/${draftId}/publish`, { data: { analyze: false } })
    expect(publish.status()).toBe(400)
    const project = await page.request.get(`/api/projects/${draftId}`)
    expect((await project.json()).data.status).toBe('draft')
  })
})
