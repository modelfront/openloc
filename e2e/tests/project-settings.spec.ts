import { test, expect } from "@playwright/test";
import { login, authHeaders, getSeedProjectId, createProjectViaAPI, deleteProjectViaAPI, selectTargetLanguages, setDeadline } from "./helpers";

/**
 * Project as Core Entity — E2E tests
 *
 * Tests the Settings tab on ProjectDetail: project settings (fuzzy threshold,
 * MT on upload, QA profile) and assignments.
 *
 * Templates are create-time-only — the tab must NOT offer an "apply
 * template" action.
 */

test.describe("Project settings", () => {
  // Projects a test makes, deleted after it with the `request` fixture: the
  // hook has its own budget, so a test that ran out of time is cleaned up too.
  let projectIds: number[] = [];
  let headers: Record<string, string> = {};

  test.beforeEach(async ({ page }) => {
    await login(page);
    headers = await authHeaders(page);
  });

  test.afterEach(async ({ request }) => {
    for (const id of projectIds) await request.delete(`/api/projects/${id}`, { headers }).catch(() => {});
    projectIds = [];
  });

  test("Settings tab shows project settings and assignments", async ({
    page,
  }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await page.getByRole("link", { name: /settings/i }).click();

    await expect(page.getByText("Project settings")).toBeVisible();
    await expect(page.getByText(/fuzzy match threshold/i)).toBeVisible();
    await expect(page.getByRole('checkbox', { name: /machine translation.*start or upload/i })).toBeVisible();

    // Should see assignment sections. Scope to headings so the assertions
    // don't strict-mode-collide with the global nav link "Terminology" (or
    // "Translation memory" → "Translation memories" nav link). The settings
    // tab renders these as h3 section headings.
    await expect(
      // exact: the "Translation memories" sub-heading would otherwise match too.
      page.getByRole("heading", { name: "Translation memory", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "Machine translation" }),
    ).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "Terminology" }),
    ).toBeVisible();
    // Workflow has its own tab — not in Settings.
    await expect(page.getByRole("heading", { name: "Workflow steps" })).toHaveCount(0);
    await page.getByRole("link", { name: /^workflow$/i }).click();
    await expect(page.getByTestId("workflow-picker")).toBeVisible();
  });

  test("displays seed data assignments", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await page.getByRole("link", { name: /settings/i }).click();

    // The seed creates project-level TM assignments — the TM section
    // (TMAssignmentSection) shows a heading + a row per assigned TM. `getByText`
    // is ambiguous ("Translation memory" heading vs the "Add Translation
    // Memory" control), so scope to the heading, then assert a real row loaded.
    await expect(
      page.getByRole("heading", { name: "Translation memory", exact: true })
    ).toBeVisible({ timeout: 5000 });
    // Seed assigns "Default TM" → at least one TM assignment row is rendered.
    await expect(
      page.locator('[data-testid^="tm-assignment-row-"]').first()
    ).toBeVisible();

    // Workflow steps from seed (Translation + Review) live
    // on the Workflow tab. The seed project is based on
    // the "Standard" library workflow, and are shown read-only.
    await page.getByRole("link", { name: /^workflow$/i }).click();
    await expect(page.getByTestId("use-workflow-select")).toContainText("Standard");
    await expect(page.getByTestId("workflow-step-badge-0")).toContainText("Translation");
    await expect(page.getByTestId("workflow-step-badge-1")).toContainText("Review");
  });

  test("can toggle MT on upload", async ({ page }) => {
    test.setTimeout(15_000);

    // Its own project, not the seeded Demo Project: tests run fully parallel,
    // and another test's settings autosave on the shared project can land
    // between this toggle and the read-back below.
    const projectId = await createProjectViaAPI(page, `mt-toggle ${Date.now()}`);
    projectIds.push(projectId);
    const token = await page.evaluate(() => localStorage.getItem("token"));

    const seed = await page.request.patch(`/api/projects/${projectId}/settings`, {
      headers: { Authorization: `Bearer ${token}` },
      data: { mt_on_upload: false },
    });
    expect(seed.ok(), "seed: PATCH mt_on_upload=false must succeed").toBe(true);

    await page.goto(`/projects/${projectId}`);
    await page.getByRole("link", { name: /settings/i }).click();

    const checkbox = page.getByRole('checkbox', { name: /machine translation.*start or upload/i });
    await expect(checkbox).toBeVisible({ timeout: 5000 });
    await expect(checkbox).not.toBeChecked({ timeout: 3000 });

    // Toggle the checkbox and wait for the PATCH to commit.
    await Promise.all([
      page.waitForResponse(
        resp => resp.url().includes("/settings") && resp.request().method() === "PATCH",
        { timeout: 8000 },
      ),
      checkbox.click(),
    ]);

    // Verify via the API (authoritative): mt_on_upload must now be true.
    const after = await page.request.get(
      `/api/projects/${projectId}/settings`,
      { headers: { Authorization: `Bearer ${token}` } },
    );
    expect(after.ok(), "settings GET after toggle").toBe(true);
    // Response shape: { data: { project: { mt_on_upload, ... }, ... } }
    expect(
      (await after.json()).data?.project?.mt_on_upload,
      "PATCH must persist server-side",
    ).toBe(true);

    // And the UI eventually reflects it. Generous poll because React Query's
    // invalidate → refetch can briefly settle on the stale cached value.
    await expect(checkbox).toBeChecked({ timeout: 8000 });
  });

  test("can edit the TM match penalties", async ({ page }) => {
    test.setTimeout(30_000);

    // Seed a known state via the API, same reasoning as the MT-on-upload test:
    // a parallel worker may have left the Demo Project's profile elsewhere.
    const projectId = await getSeedProjectId(page);
    const token = await page.evaluate(() => localStorage.getItem("token"));
    const auth = { Authorization: `Bearer ${token}` };
    const seed = await page.request.patch(`/api/projects/${projectId}/settings`, {
      headers: auth,
      data: { sublanguage_penalty_pct: 1, near_exact_penalty_pct: 1 },
    });
    expect(seed.ok(), "seed: PATCH penalties must succeed").toBe(true);

    await page.getByRole("link", { name: /demo project/i }).click();
    await page.getByRole("link", { name: /settings/i }).click();

    const sublanguage = page.getByRole("spinbutton", { name: /language-variant mismatch penalty/i });
    await expect(sublanguage).toBeVisible({ timeout: 5000 });
    await expect(sublanguage).toHaveValue("1", { timeout: 3000 });

    // Edit commits on blur in edit mode.
    await sublanguage.fill("6");
    await Promise.all([
      page.waitForResponse(
        resp => resp.url().includes("/settings") && resp.request().method() === "PATCH",
        { timeout: 8000 },
      ),
      sublanguage.blur(),
    ]);

    // The API is authoritative — the input showing "6" only proves local state.
    const after = await page.request.get(`/api/projects/${projectId}/settings`, { headers: auth });
    expect(after.ok(), "settings GET after edit").toBe(true);
    expect(
      (await after.json()).data?.project?.sublanguage_penalty_pct,
      "penalty must persist server-side",
    ).toBe(6);

    // And it survives a reload rather than living in component state.
    await page.reload();
    await page.getByRole("link", { name: /settings/i }).click();
    await expect(page.getByRole("spinbutton", { name: /language-variant mismatch penalty/i }))
      .toHaveValue("6", { timeout: 8000 });

    // Per-TM penalty column renders on the assignment row (seeded Default TM).
    await expect(page.getByRole("spinbutton", { name: /penalty for/i }).first())
      .toBeVisible({ timeout: 5000 });

    // Restore the default so later tests / manual QA see a clean project.
    const restore = await page.request.patch(`/api/projects/${projectId}/settings`, {
      headers: auth,
      data: { sublanguage_penalty_pct: 1 },
    });
    expect(restore.ok(), "restore: PATCH penalties must succeed").toBe(true);
  });

  test("no post-create apply-template control (templates are create-time-only)", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await page.getByRole("link", { name: /settings/i }).click();

    // Anchor on a block that IS there so the negative assertions below aren't
    // trivially green on a broken/blank tab.
    await expect(page.getByText("Project settings")).toBeVisible();
    await expect(page.getByText("Apply template:")).toHaveCount(0);
    await expect(page.getByRole("button", { name: /save as template/i })).toHaveCount(0);
  });

  test("settings and workflow tabs show an error, not an endless loader, when the project is gone", async ({ page }) => {
    const projectId = await createProjectViaAPI(page, `Vanishing ${Date.now()}`);
    await deleteProjectViaAPI(page, projectId);
    await page.goto(`/projects/${projectId}/settings`);
    const alert = page.getByRole("alert");
    await expect(alert).toContainText(/couldn't load the project settings/i);
    await expect(alert.getByRole("button", { name: /retry/i })).toBeVisible();
    await expect(page.getByText(/loading settings/i)).toHaveCount(0);
    await page.getByRole("link", { name: /^workflow$/i }).click();
    // Filtered, not a bare getByRole("alert"): the outgoing settings panel stays
    // mounted until its close transition finishes, so for a moment both panels
    // render a LoadFailed and a global alert locator matches two elements.
    await expect(
      page.getByRole("alert").filter({ hasText: /couldn't load the workflow/i }),
    ).toBeVisible();
  });

  // Deadlines are the one thing editable on the Workflow tab, where the
  // steps themselves are library-only: they are
  // scheduling, not part of the workflow's identity.
  // Both the step's own date and a per-language override must survive the
  // round trip through the save mappers and the settings read.
  test("step deadlines, step-level and per-language, survive the autosave round trip", async ({ page }) => {
    // A project, two date pickers, an autosave and a reload: about 9 s under suite load.
    test.slow();
    const projectId = await createProjectViaAPI(page, `Deadline round trip ${Date.now()}`);
    projectIds.push(projectId);
    await page.goto(`/projects/${projectId}/workflow`);
    await expect(page.getByTestId("workflow-deadline-input-0")).toBeVisible();

    // One language pulled earlier than the step's own date, as an override row.
    const lang = "de";
    await page.getByTestId("workflow-deadline-langs-toggle-0").click();
    await selectTargetLanguages(
      page,
      page.getByTestId("workflow-deadline-override-0-0").getByTestId("lang"),
      [lang],
    );
    await setDeadline(page, page.getByTestId("workflow-deadline-override-input-0-0"), "2026-09-01T09:00");

    // No Save button — each edit autosaves a moment after it settles, so the
    // override above may save on its own while the step's date is still being
    // picked. Wait for the save whose body carries the step's date, not for
    // whichever PUT comes first.
    const [resp] = await Promise.all([
      page.waitForResponse(
        r =>
          r.url().includes(`/projects/${projectId}/workflow-steps`) &&
          r.request().method() === "PUT" &&
          Boolean(r.request().postDataJSON()?.steps?.[0]?.deadline),
        { timeout: 10000 },
      ),
      setDeadline(page, page.getByTestId("workflow-deadline-input-0"), "2026-09-03T17:00"),
    ]);
    expect(resp.ok()).toBe(true);
    const steps = (await resp.json()).data;
    expect(steps[0].deadline, "deadline was dropped by the save mapper").toBeTruthy();
    expect(
      steps[0].language_deadlines,
      "the per-language date was dropped by the save mapper",
    ).toEqual([{ target_language: lang, deadline: expect.any(String) }]);

    // Full loop: reload, so the READ mapper must map the saved deadline
    // back into the field.
    await page.reload();
    await expect(
      page.getByTestId("workflow-deadline-input-0"),
      "deadline was dropped by the settings read mapper",
    ).toHaveAttribute("data-value", new Date("2026-09-03T17:00").toISOString());
    await page.getByTestId("workflow-deadline-langs-toggle-0").click();
    await expect(page.getByTestId("workflow-deadline-override-input-0-0")).toHaveAttribute(
      "data-value",
      new Date("2026-09-01T09:00").toISOString(),
    );
  });
});

// The ⚠ on a TM row whose declared targets miss every
// project target must carry a real tooltip body, not just the icon. Real hover on real tooltip primitives — the unit test mocks them.
test.describe("TM row target-overlap warning", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("warns when the TM covers none of the project's targets, with a tooltip", async ({ page }) => {
    test.setTimeout(30_000);
    const token = await page.evaluate(() => localStorage.getItem("token"));
    const headers = { Authorization: `Bearer ${token}` };
    let projectId: number | null = null;
    try {
      // Default TM declares en → es/fr/de; a Japanese project shares no target.
      const tms = await page.request.get("/api/tms", { headers });
      const tmId = (await tms.json()).data.find((t: { name: string }) => t.name === "Default TM").id as number;
      const created = await page.request.post("/api/projects", {
        headers,
        data: {
          name: `E2E overlap ${Date.now()}`, source_language: "en", target_languages: ["ja"],
          tm_assignments: [{ tm_id: tmId, mode: "read_write", priority: 0 }],
        },
      });
      expect(created.ok()).toBe(true);
      projectId = (await created.json()).data.id;

      await page.goto(`/projects/${projectId}/files`);
      await page.getByRole("link", { name: /settings/i }).click();
      const warn = page.getByTestId(`tm-assignment-row-${tmId}`).getByTestId("tm-assignment-no-overlap");
      await expect(warn).toBeVisible({ timeout: 5000 });
      await warn.hover();
      await expect(page.locator('[data-slot="tooltip-content"]')).toContainText(/no target language in common/i, { timeout: 3000 });
    } finally {
      if (projectId) await page.request.delete(`/api/projects/${projectId}`, { headers }).catch(() => {});
    }
  });
});

// Real-pointer drag-to-reorder of TM rows (dnd-kit). The unit test cannot
// drive this (jsdom has no layout); this is the regression guard for
// "reordering sometimes does not work" / "blinks between two slots".
test.describe("TM rows drag-to-reorder", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("dragging the second row above the first swaps the sequence and saves it once", async ({ page }) => {
    test.setTimeout(30_000);
    const token = await page.evaluate(() => localStorage.getItem("token"));
    const headers = { Authorization: `Bearer ${token}` };
    const stamp = Date.now();
    let projectId: number | null = null;
    let tm2: number | null = null;
    try {
      const tms = await page.request.get("/api/tms", { headers });
      const tm1 = (await tms.json()).data.find((t: { name: string }) => t.name === "Default TM").id as number;
      const created2 = await page.request.post("/api/tms", {
        headers, data: { name: `E2E second TM ${stamp}`, source_language: "en", target_languages: ["es"] },
      });
      expect(created2.ok()).toBe(true);
      tm2 = (await created2.json()).data.id;
      const created = await page.request.post("/api/projects", {
        headers,
        data: {
          name: `E2E reorder ${stamp}`, source_language: "en", target_languages: ["es"],
          tm_assignments: [{ tm_id: tm1, mode: "read_write", priority: 0 }, { tm_id: tm2, mode: "read", priority: 1 }],
        },
      });
      expect(created.ok()).toBe(true);
      projectId = (await created.json()).data.id;

      await page.goto(`/projects/${projectId}/files`);
      await page.getByRole("link", { name: /settings/i }).click();
      const row1 = page.getByTestId(`tm-assignment-row-${tm1}`);
      const row2 = page.getByTestId(`tm-assignment-row-${tm2}`);
      await expect(row1).toBeVisible({ timeout: 5000 });
      // The rows sit below the fold; pointer events outside the viewport are no-ops.
      await row2.scrollIntoViewIfNeeded();
      await expect(row2).toBeInViewport();
      await expect(page.getByTestId(`tm-assignment-sequence-${tm1}`)).toContainText("Master");

      // Grab row 2's handle and move it onto row 1 in small steps — dnd-kit's
      // pointer sensor needs real pointer travel, not a synthetic drop.
      const handle = page.getByTestId(`tm-assignment-drag-${tm2}`);
      const hb = (await handle.boundingBox())!;
      const r1 = (await row1.boundingBox())!;
      const x = hb.x + hb.width / 2;
      const startY = hb.y + hb.height / 2;
      await page.mouse.move(x, startY);
      await page.mouse.down();
      // The sensor activates at 4px of travel. Cross it, then wait for dnd-kit
      // to mark the row as dragging before moving on: moves dispatched before
      // activation are not tracked, so the drop lands with no target and no PUT
      // is sent.
      await page.mouse.move(x, startY + 6);
      await expect(row2).toHaveAttribute("data-dragging", "true");
      // Land the dragged row's centre on row 1's centre — collision detection is
      // closestCenter, so centre-on-centre is the unambiguous drop.
      const targetY = r1.y + r1.height / 2;
      for (let i = 1; i <= 8; i++) {
        await page.mouse.move(x, startY + 6 + ((targetY - startY - 6) * i) / 8);
      }
      const [putResp] = await Promise.all([
        page.waitForResponse(r => r.url().includes(`/api/projects/${projectId}/tms`) && r.request().method() === "PUT"),
        page.mouse.up(),
      ]);
      expect(putResp.ok()).toBe(true);
      const saved = (await putResp.json()).data as { tm_id: number; priority: number }[];
      expect(saved.map(a => a.tm_id)).toEqual([tm2, tm1]);
      expect(saved.map(a => a.priority)).toEqual([0, 1]);
      await expect(page.getByTestId(`tm-assignment-sequence-${tm2}`)).toContainText("Master");
      await expect(page.getByTestId(`tm-assignment-sequence-${tm1}`)).not.toContainText("Master");

      // Holding the top row far below the list must not grow
      // scroll bars on the table — the dragged row is clamped to the list box.
      const table = page.getByTestId("tm-assignment-table");
      const topHandle = page.getByTestId(`tm-assignment-drag-${tm2}`); // tm2 is now the top row
      const th = (await topHandle.boundingBox())!;
      await page.mouse.move(th.x + th.width / 2, th.y + th.height / 2);
      await page.mouse.down();
      for (let i = 1; i <= 10; i++) {
        await page.mouse.move(th.x + th.width / 2 + i * 30, th.y + th.height / 2 + i * 60);
      }
      // A scroll bar appears when an axis overflows AND that axis is auto/scroll.
      // (Overlay scroll bars on macOS have zero thickness, so measure the rule,
      // not the pixels.)
      const bars = await table.evaluate(el => {
        const cs = getComputedStyle(el);
        const scrolls = (v: string) => v === "auto" || v === "scroll";
        return {
          vertical: scrolls(cs.overflowY) && el.scrollHeight > el.clientHeight,
          horizontal: scrolls(cs.overflowX) && el.scrollWidth > el.clientWidth,
        };
      });
      expect(bars, "no scroll bars while a row is held far outside the list").toEqual({ vertical: false, horizontal: false });
      await page.mouse.up();
    } finally {
      if (projectId) await page.request.delete(`/api/projects/${projectId}`, { headers }).catch(() => {});
      if (tm2) await page.request.delete(`/api/tms/${tm2}`, { headers }).catch(() => {});
    }
  });
});
