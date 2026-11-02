import { test, expect } from "@playwright/test";
import { login, authHeaders, getSeedProjectId, createProjectViaAPI, createTMViaAPI, deleteProjectViaAPI, standardWorkflowId, selectTargetLanguages, setDeadline } from "./helpers";

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
    // the "Standard" library workflow.
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

  // Case-sensitive on purpose: the picker label is sentence case.
  test("the terminology picker reads 'Add term base'", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await page.getByRole("link", { name: /settings/i }).click();

    await expect(page.getByRole("heading", { name: "Terminology" })).toBeVisible();
    await expect(page.getByTestId("resource-select")).toHaveText("Add term base");
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

  // Both the step's own date and a per-language override must survive the
  // round trip through the save mappers and the settings read.
  test("step deadlines, step-level and per-language, survive the save round trip", async ({ page }) => {
    // A project, two date pickers, a save through its preview and a reload: about 9 s under suite load.
    test.slow();
    const projectId = await createProjectViaAPI(page, `Deadline round trip ${Date.now()}`);
    projectIds.push(projectId);
    await page.goto(`/projects/${projectId}/workflow`);
    await page.getByTestId("workflow-steps-edit").click();
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
    await setDeadline(page, page.getByTestId("workflow-deadline-input-0"), "2026-09-03T17:00");

    // Nothing saves on its own: Save changes shows the preview, the
    // confirmation saves.
    await page.getByTestId("workflow-steps-save").click();
    const dialog = page.getByRole("alertdialog");
    await expect(dialog).toContainText("This save changes assignees and deadlines only.");
    const [resp] = await Promise.all([
      page.waitForResponse(r => r.url().includes(`/projects/${projectId}/workflow-steps`) && r.request().method() === "PUT"),
      dialog.getByTestId("workflow-steps-confirm").click(),
    ]);
    expect(resp.ok()).toBe(true);
    const steps = (await resp.json()).data;
    expect(steps[0].deadline, "deadline was dropped by the save mapper").toBeTruthy();
    expect(
      steps[0].language_deadlines,
      "the per-language date was dropped by the save mapper",
    ).toEqual([{ target_language: lang, deadline: expect.any(String) }]);

    // Full loop: reload, so the READ mapper must map the saved deadlines back
    // into the read-only view.
    await page.reload();
    await expect(
      page.getByTestId("workflow-deadline-0"),
      "deadline was dropped by the settings read mapper",
    ).toHaveAttribute("data-value", new Date("2026-09-03T17:00").toISOString());
    await page.getByTestId("workflow-deadline-langs-toggle-0").click();
    await expect(page.getByTestId("workflow-deadline-override-value-0-0")).toHaveAttribute(
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
          name: `E2E overlap ${Date.now()}`, source_language: "en", target_languages: ["ja"], workflow_id: await standardWorkflowId(page),
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
          name: `E2E reorder ${stamp}`, source_language: "en", target_languages: ["es"], workflow_id: await standardWorkflowId(page),
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

// Where the target codes, the +4 chip and the content of a TM languages cell
// sit. A code's line count comes from a Range over its characters, so a code
// the browser broke after its hyphen counts as two lines. Runs in the page.
function measureLangs(el: Element, codes: string[]) {
  const chars: { node: Text; offset: number }[] = [];
  const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const t = n as Text;
    for (let i = 0; i < t.data.length; i++) chars.push({ node: t, offset: i });
  }
  const text = chars.map(c => c.node.data[c.offset]).join("").toLowerCase();
  const tops = (rects: DOMRect[]) => new Set(rects.filter(r => r.width > 0).map(r => Math.round(r.top)));
  const shown = codes
    .map(code => ({ code, at: text.indexOf(code.toLowerCase()) }))
    .filter(c => c.at >= 0)
    .sort((a, b) => a.at - b.at)
    .map(({ code, at }) => {
      const range = document.createRange();
      range.setStart(chars[at].node, chars[at].offset);
      const end = chars[at + code.length - 1];
      range.setEnd(end.node, end.offset + 1);
      return { code, rects: Array.from(range.getClientRects()) };
    });
  const last = shown[shown.length - 1].rects.at(-1)!;
  const chip = Array.from(el.querySelectorAll("*"))
    .find(n => n.children.length === 0 && n.textContent === "+4")!
    .getBoundingClientRect();
  const chipMiddle = chip.top + chip.height / 2;
  return {
    splitCodes: shown.filter(c => tops(c.rects).size !== 1).map(c => c.code),
    lines: new Set(shown.flatMap(c => [...tops(c.rects)])).size,
    chipOnLastLine: chipMiddle > last.top && chipMiddle < last.bottom,
    chipLeft: chip.left,
    lastCodeRight: last.right,
    contentRight: Math.max(...Array.from(el.querySelectorAll("*"), n => n.getBoundingClientRect().right)),
    cellRight: el.getBoundingClientRect().right,
  };
}

// A TM that declares many locales keeps its language list inside the
// Languages column: it wraps between codes, never inside one, the +N chip
// stays after the last shown target, and nothing runs under the Read and
// Write checkboxes. Where a line breaks depends on the column width, so it is
// checked across window widths. Real layout: jsdom cannot measure it.
test.describe("TM row with many target languages", () => {
  let headers: Record<string, string> = {};
  let projectId: number | null = null;
  let tmId: number | null = null;

  test.beforeEach(async ({ page }) => {
    await login(page);
    headers = await authHeaders(page);
  });

  test.afterEach(async ({ request }) => {
    if (projectId) await request.delete(`/api/projects/${projectId}`, { headers }).catch(() => {});
    if (tmId) await request.delete(`/api/tms/${tmId}`, { headers }).catch(() => {});
    projectId = null;
    tmId = null;
  });

  test("the language list wraps between codes inside its column, +N chip last", async ({ page }) => {
    test.setTimeout(30_000);
    const targets = ["fr-CA", "fr-FR", "it-IT", "de-DE", "es-419", "pt-BR", "pt-PT", "nl-NL", "sv-SE"];
    await createTMViaAPI(page, `E2E wide TM ${Date.now()}`, { targets, onCreated: id => { tmId = id; } });
    projectId = await createProjectViaAPI(page, `E2E wide TM ${Date.now()}`, {
      target_languages: ["de-DE"],
      tm_assignments: [{ tm_id: tmId, mode: "read_write", priority: 0 }],
    });

    await page.goto(`/projects/${projectId}/files`);
    await page.getByRole("link", { name: /settings/i }).click();
    const cell = page.getByTestId(`tm-assignment-langs-${tmId}`);
    // Five targets, then a +4 chip for the rest.
    await expect(cell).toContainText(/es-419/i);
    await expect(cell).toContainText("+4");
    const read = page.getByTestId(`tm-assignment-read-${tmId}`);

    for (let width = 1024; width <= 1280; width += 8) {
      await page.setViewportSize({ width, height: 900 });
      const m = await cell.evaluate(measureLangs, targets);
      const at = `at ${width}px`;
      expect(m.splitCodes, `no code is split over two lines ${at}`).toEqual([]);
      expect(m.lines, `the list wraps ${at}`).toBeGreaterThan(1);
      expect(m.chipOnLastLine, `+4 is on the last code's line ${at}`).toBe(true);
      expect(m.chipLeft, `+4 comes after the last code ${at}`).toBeGreaterThanOrEqual(m.lastCodeRight - 0.5);
      expect(m.contentRight, `languages end inside their column ${at}`).toBeLessThanOrEqual(m.cellRight + 0.5);
      expect(m.contentRight, `languages end before the Read checkbox ${at}`).toBeLessThan((await read.boundingBox())!.x);
    }
  });
});
