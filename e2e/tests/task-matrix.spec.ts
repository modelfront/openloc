import { test, expect, type Page } from "@playwright/test";
import { authHeaders, createProjectViaAPI, deleteProjectViaAPI, getSeedProjectId, login, openTaskSteps, withResizeStripsHidden } from "./helpers";

// Project page → Tasks tab (the page's index route): a section per workflow
// step holding each job's current task, and Expand for the job × step matrix. Runs on the seeded Demo Project (one file, three languages,
// Translation → Review).

interface Task {
  id: number;
  job_id: number;
  workflow_step_id: number;
  step_order: number;
  status: string;
  deadline: string | null;
}

async function listTasks(page: Page, projectId: number): Promise<Task[]> {
  const res = await page.request.get(`/api/projects/${projectId}/tasks`, { headers: await authHeaders(page) });
  expect(res.ok(), `list tasks: ${res.status()}`).toBe(true);
  return (await res.json()).data;
}

test.describe("Task matrix", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("the page shows a closed section per step, each job once; Expand shows a cell per job and step", async ({ page }) => {
    const projectId = await getSeedProjectId(page);
    const tasks = await listTasks(page, projectId);
    const jobIds = [...new Set(tasks.map((t) => t.job_id))];
    const stepIds = [...new Set(tasks.map((t) => t.workflow_step_id))];

    await page.goto(`/projects/${projectId}`);
    await expect(page.locator('[data-testid^="task-step-section-"]')).toHaveCount(stepIds.length);
    await expect(page.locator('[data-testid^="task-row-job:"]')).toHaveCount(0);
    await openTaskSteps(page);
    await expect(page.locator('[data-testid^="task-row-job:"]')).toHaveCount(jobIds.length);
    await expect(page.locator('[data-testid^="task-status-job:"]')).toHaveCount(jobIds.length);

    await page.getByTestId("task-matrix-expand").click();
    const modal = page.getByTestId("task-matrix-modal");
    await expect(modal.locator('[data-testid^="task-row-header-job:"]')).toHaveCount(jobIds.length);
    await expect(modal.locator('[data-testid^="task-matrix-step-"]')).toHaveCount(stepIds.length);
    await expect(modal.locator('[role="gridcell"]')).toHaveCount(tasks.length);
  });

  test("Set deadline on selected Review tasks writes those steps, not the active one", async ({ page }) => {
    const projectId = await getSeedProjectId(page);
    const before = await listTasks(page, projectId);
    const firstOrder = Math.min(...before.map((t) => t.step_order));
    const reviews = before.filter((t) => t.step_order > firstOrder && t.status === "pending").slice(0, 2);
    expect(reviews.length, "seed should have two not-started later steps").toBe(2);
    const headers = await authHeaders(page);

    try {
      // Tasks that have not started are not current: only Expand shows them.
      await page.goto(`/projects/${projectId}`);
      await page.getByTestId("task-matrix-expand").click();
      const modal = page.getByTestId("task-matrix-modal");
      const cellOf = (t: Task) => modal.getByTestId(`task-cell-job:${t.job_id}|${t.workflow_step_id}`);

      await cellOf(reviews[0]).click();
      await cellOf(reviews[1]).click({ modifiers: ["ControlOrMeta"] });
      await expect(cellOf(reviews[0])).toHaveAttribute("aria-selected", "true");
      await expect(cellOf(reviews[1])).toHaveAttribute("aria-selected", "true");
      await expect(modal.getByTestId("bulk-selected-count")).toHaveText("2 tasks selected");

      const patched = reviews.map((t) =>
        page.waitForResponse(
          (r) => r.url().endsWith(`/api/jobs/${t.job_id}/workflow/steps/${t.id}/deadline`) && r.request().method() === "PATCH",
        ),
      );
      // A preset applies at once; paging the calendar to a far month is slow.
      await modal.getByTestId("bulk-deadline-btn").click();
      await page.locator('[data-slot="popover-content"]').getByRole("button", { name: "2 days" }).click();
      for (const r of await Promise.all(patched)) expect(r.ok()).toBe(true);

      const after = await listTasks(page, projectId);
      for (const t of after) {
        const picked = reviews.some((r) => r.id === t.id);
        if (picked) expect(t.deadline, `task ${t.id} should carry the new deadline`).not.toBeNull();
        else expect(t.deadline, `task ${t.id} was not selected`).toBe(before.find((b) => b.id === t.id)!.deadline);
      }
    } finally {
      for (const t of reviews) {
        await page.request.patch(`/api/jobs/${t.job_id}/workflow/steps/${t.id}/deadline`, {
          headers,
          data: { deadline: t.deadline },
        });
      }
    }
  });

  test("full screen: Escape clears the selection wherever focus is, then closes the modal", async ({ page }) => {
    const projectId = await getSeedProjectId(page);
    await page.goto(`/projects/${projectId}`);
    await page.getByTestId("task-matrix-expand").click();

    const modal = page.getByTestId("task-matrix-modal");
    await expect(modal.getByTestId("task-matrix")).toBeVisible();

    // Select from a step header, which takes no focus, so Escape lands outside
    // the grid.
    await modal.locator('[data-testid^="task-matrix-step-"]').first().click();
    await expect(modal.getByTestId("bulk-actions-bar")).toBeVisible();

    // An open menu inside the modal takes the first Escape for itself.
    await modal.getByTestId("bulk-assign-btn").click();
    const member = page.locator('[data-testid^="bulk-assign-user-"]').first();
    await expect(member).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(member).toHaveCount(0);
    await expect(modal.getByTestId("bulk-actions-bar")).toBeVisible();
    // The modal plays a close animation, during which it still reads as
    // visible; its content unmounts at once, so that is what tells open
    // from closing.
    await expect(modal.getByTestId("task-matrix")).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(modal.getByTestId("bulk-actions-bar")).toHaveCount(0);
    await expect(modal.getByTestId("task-matrix")).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(modal).toHaveCount(0);
  });

  test("a click on a step row opens its job in the CAT editor in a new tab; its box only selects", async ({ page }) => {
    const projectId = await getSeedProjectId(page);
    await page.goto(`/projects/${projectId}`);
    await openTaskSteps(page);
    const row = page.locator('[data-testid^="task-row-job:"]').first();
    const jobId = (await row.getAttribute("data-testid"))!.replace("task-row-job:", "");

    await row.locator('[data-testid^="task-select-job:"]').click();
    await expect(page.getByTestId("bulk-selected-count")).toHaveText("1 task selected");
    await expect(page).toHaveURL(new RegExp(`/projects/${projectId}$`));

    // A click on the file name opens the editor in a new tab; this one stays put.
    const [editor] = await Promise.all([
      page.waitForEvent("popup"),
      row.locator("td").nth(2).click(),
    ]);
    await expect(editor).toHaveURL(new RegExp(`/projects/${projectId}/jobs/${jobId}/edit$`));
    await expect(page).toHaveURL(new RegExp(`/projects/${projectId}$`));
    await editor.close();
  });

  test("a step table never scrolls: narrow, it drops Words and nothing overflows", async ({ page }) => {
    const projectId = await getSeedProjectId(page);
    const stepTable = '[data-testid="task-step-sections"] table';
    const fits = async () => ({
      ...(await page.evaluate((sel) => {
        const table = document.querySelector(sel) as HTMLTableElement;
        const box = table.parentElement!;
        const visible = [...table.querySelectorAll("th")].filter((th) => getComputedStyle(th).display !== "none");
        return {
          tableFitsBox: table.scrollWidth <= box.clientWidth,
          boxScrolls: box.scrollHeight > box.clientHeight || box.scrollWidth > box.clientWidth,
          pageScrollsSideways: document.documentElement.scrollWidth > document.documentElement.clientWidth,
          headers: visible.map((th) => th.textContent),
        };
      }, stepTable)),
      overflowingCells: await withResizeStripsHidden(page, () => page.evaluate((sel) =>
        [...document.querySelector(sel)!.querySelectorAll("th, td")].filter(
          (c) => (c as HTMLElement).offsetParent && c.scrollWidth > c.clientWidth + 1,
        ).length, stepTable)),
    });

    try {
      for (const [width, headers] of [
        [1400, ["", "Lang", "File", "Assignee", "Deadline", "Words", ""]],
        [900, ["", "Lang", "File", "Assignee", "Deadline", ""]],
      ] as const) {
        await page.setViewportSize({ width, height: 800 });
        await page.goto(`/projects/${projectId}`);
        await openTaskSteps(page);
        expect(await fits(), `at ${width}px`).toEqual({
          tableFitsBox: true, boxScrolls: false, pageScrollsSideways: false, overflowingCells: 0, headers,
        });
      }
    } finally {
      await page.setViewportSize({ width: 1280, height: 720 });
    }
  });

  test("a header edge drags its column wider, the width survives a reload, and a step edge drag selects nothing", async ({ page }) => {
    // Two drags and a reload of the project page: about 9 s under suite load.
    test.slow();
    const projectId = await getSeedProjectId(page);
    await page.goto(`/projects/${projectId}`);
    await page.evaluate(() => localStorage.removeItem("task-matrix-column-widths:fullScreen"));
    await page.reload();
    const modal = page.getByTestId("task-matrix-modal");
    // The modal scales in, so a header measured mid-animation reads narrow.
    const expand = async () => {
      await page.getByTestId("task-matrix-expand").click();
      await expect(modal.getByTestId("task-matrix")).toBeVisible();
      await modal.evaluate((el) => Promise.allSettled(el.getAnimations({ subtree: true }).map((a) => a.finished)));
    };
    await expand();

    const edge = modal.getByTestId("task-matrix-resize-file");
    const header = modal.locator("th").filter({ has: page.getByTestId("task-matrix-resize-file") });
    const drag = async (handle: typeof edge, dx: number) => {
      const b = (await handle.boundingBox())!;
      const x = b.x + b.width / 2, y = b.y + b.height / 2;
      await page.mouse.move(x, y);
      await page.mouse.down();
      await page.mouse.move(x + dx, y, { steps: 5 });
      await page.mouse.up();
    };
    const width = async () => Math.round((await header.boundingBox())!.width);

    const before = await width();
    await drag(edge, 80);
    await expect.poll(width).toBe(before + 80);
    await page.reload();
    await expand();
    await expect.poll(width).toBe(before + 80);

    // A step's edge resizes it too, and letting go over the header does not
    // select the step.
    await drag(modal.locator('[data-testid^="task-matrix-resize-step-"]').first(), -40);
    await expect(page.getByTestId("bulk-actions-bar")).toHaveCount(0);

    await page.evaluate(() => localStorage.removeItem("task-matrix-column-widths:fullScreen"));
  });

  test("the step sections line up with the rest of the tab, and every table stays inside them", async ({ page }) => {
    const projectId = await getSeedProjectId(page);
    await page.goto(`/projects/${projectId}`);
    await openTaskSteps(page);
    const sections = page.getByTestId("task-step-sections");
    // The column the toolbar sits in: the sections must not stick out of it.
    const [own, column] = await sections.evaluate((el) =>
      [el, el.parentElement!].map((e) => { const r = e.getBoundingClientRect(); return [Math.round(r.left), Math.round(r.right)]; }));
    expect(own).toEqual(column);
    for (const table of await sections.locator("table").all()) {
      const t = (await table.boundingBox())!, s = (await sections.boundingBox())!;
      expect(t.x).toBeGreaterThanOrEqual(s.x);
      expect(t.x + t.width).toBeLessThanOrEqual(s.x + s.width);
    }
  });

  test("a step table's header edge drags a column wider, Assignee gives way down to 120px, and the width survives a reload", async ({ page }) => {
    // Drags and a reload of the project page: about 8 s under suite load.
    test.slow();
    const projectId = await getSeedProjectId(page);
    await page.goto(`/projects/${projectId}`);
    await page.evaluate(() => localStorage.removeItem("task-step-column-widths"));
    await page.reload();
    await openTaskSteps(page);

    const table = page.getByTestId("task-step-sections").locator("table").first();
    const edge = table.getByRole("separator", { name: "Resize file column" });
    const width = (name: string) => async () =>
      Math.round((await table.locator("th").filter({ hasText: new RegExp(`^${name}$`, "i") }).boundingBox())!.width);
    const drag = async (dx: number) => {
      const b = (await edge.boundingBox())!;
      const x = b.x + b.width / 2, y = b.y + b.height / 2;
      await page.mouse.move(x, y);
      await page.mouse.down();
      await page.mouse.move(x + dx, y, { steps: 5 });
      await page.mouse.up();
    };
    const fits = () => table.evaluate((t) => t.parentElement!.scrollWidth <= t.parentElement!.clientWidth);

    const file = await width("File")();
    const assignee = await width("Assignee")();
    await drag(60);
    await expect.poll(width("File")).toBe(file + 60);
    await expect.poll(width("Assignee")).toBe(assignee - 60);

    // Far past the edge: File stops where Assignee is down to 120px.
    await drag(600);
    await expect.poll(width("Assignee")).toBe(120);
    expect(await fits()).toBe(true);
    const dragged = await width("File")();

    await page.reload();
    await openTaskSteps(page);
    await expect.poll(width("File")).toBe(dragged);

    // Double-click hands the space back to File and Assignee alike.
    await edge.dblclick();
    await expect.poll(width("File")).toBe(file);
    expect(await page.evaluate(() => localStorage.getItem("task-step-column-widths"))).toBe("{}");
  });

  test("the filter icon opens the picked filter's options; the chip appears with a value and goes when cleared", async ({ page }) => {
    const projectId = await getSeedProjectId(page);
    const tasks = await listTasks(page, projectId);
    const jobCount = new Set(tasks.map((t) => t.job_id)).size;
    await page.goto(`/projects/${projectId}`);

    await expect(page.getByTestId("task-filter-language")).toHaveCount(0);
    await page.getByTestId("task-filter-menu").click();
    const popup = page.getByTestId("task-filter-menu-popup");
    await popup.getByTestId("task-filter-menu-option-target").click();
    // The same popup now offers the languages; no chip until one is picked.
    await expect(page.getByTestId("task-filter-language")).toHaveCount(0);
    await popup.getByRole("button", { name: "de", exact: true }).click();
    await expect(page).toHaveURL(/target=de/);
    await expect(page.getByTestId("task-filter-language")).toContainText("de");
    await openTaskSteps(page);
    await expect(page.locator('[data-testid^="task-row-job:"]')).toHaveCount(1);

    // The filter lives in the URL, so a reload keeps the chip.
    await page.reload();
    await expect(page.getByTestId("task-filter-language")).toContainText("de");

    await page.getByTestId("task-filter-language-remove").click();
    await expect(page.getByTestId("task-filter-language")).toHaveCount(0);
    await expect(page).not.toHaveURL(/target=/);
    await openTaskSteps(page);
    await expect(page.locator('[data-testid^="task-row-job:"]')).toHaveCount(jobCount);
  });

  test("a project with files opens on Tasks", async ({ page }) => {
    const projectId = await getSeedProjectId(page);
    await page.goto(`/projects/${projectId}`);
    const tabs = page.getByRole("navigation", { name: "Section" });
    await expect(tabs.getByRole("link", { name: "Tasks" })).toHaveAttribute("aria-current", "page");
    await expect(page.getByTestId("task-step-sections")).toBeVisible();
    await expect(page).toHaveURL(new RegExp(`/projects/${projectId}$`));
  });

  test("a project without files opens on Files, with Tasks disabled", async ({ page }) => {
    const projectId = await createProjectViaAPI(page, `E2E no files ${Date.now()}`);
    try {
      await page.goto(`/projects/${projectId}`);
      await expect(page).toHaveURL(new RegExp(`/projects/${projectId}/files$`));
      const tabs = page.getByRole("navigation", { name: "Section" });
      await expect(tabs.getByRole("link", { name: "Files" })).toHaveAttribute("aria-current", "page");

      const tasksTab = tabs.getByRole("link", { name: "Tasks" });
      await expect(tasksTab).toHaveAttribute("aria-disabled", "true");
      await expect(tasksTab).not.toHaveAttribute("href");
      await tasksTab.hover();
      await expect(page.getByText("Upload a file to create tasks.")).toBeVisible();
      // Playwright waits forever to click an aria-disabled element; force the
      // click to prove it goes nowhere.
      await tasksTab.click({ force: true });
      await expect(page).toHaveURL(new RegExp(`/projects/${projectId}/files$`));
    } finally {
      await deleteProjectViaAPI(page, projectId);
    }
  });
});
