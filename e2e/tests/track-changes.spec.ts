import { test, expect, type Page } from "@playwright/test";
import { authHeaders, createIsolatedCATJob, login } from "./helpers";

// Track changes: in a later workflow step, the target column marks what
// changed since the previous step ended. The toggle stays off on a first step.

const API = process.env.API_BASE ?? "";

interface Translation {
  id: number;
  segment_id: number;
}

async function listTranslations(page: Page, jobId: number): Promise<Translation[]> {
  const res = await page.request.get(`/api/jobs/${jobId}/translations`, { headers: await authHeaders(page) });
  expect(res.ok(), `list translations: ${res.status()}`).toBe(true);
  return ((await res.json()).data as Translation[]).sort((a, b) => a.segment_id - b.segment_id);
}

// The project the running test created, deleted in afterEach with headers read
// right after login: the hook has its own time budget. Best-effort.
let headers: Record<string, string> = {};
let projectId: number | null = null;

test.afterEach(async ({ request }) => {
  if (projectId) await request.delete(`${API}/api/projects/${projectId}`, { headers }).catch(() => {});
  projectId = null;
});

test.describe("Track changes", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
    headers = await authHeaders(page);
    await page.evaluate(() => localStorage.removeItem("cat-track-changes"));
  });

  test("the reviewer sees the translator's words struck through and their own highlighted", async ({ page }) => {
    test.setTimeout(60_000);
    const job = await createIsolatedCATJob(page, {
      fileName: `track-changes-${Date.now()}.txt`,
      content: "Report received today\nSecond tracked line\n",
      onCreated: (id) => (projectId = id),
    });
    const [a] = await listTranslations(page, job.jobId);
    const save = async (text: string) => {
      const res = await page.request.put(`/api/files/${job.fileId}/translations/${a.id}`, {
        headers,
        data: { human_text: text, editing_time_ms: 0 },
      });
      expect(res.ok(), `save: ${res.status()} ${await res.text()}`).toBe(true);
    };
    const stepStatusesLoaded = () =>
      page.waitForResponse((r) => r.url().includes(`/jobs/${job.jobId}/step-statuses`) && r.ok());

    // First step: nothing to compare with yet, once the step statuses say so.
    await save("Denuncia enviada hoy");
    const firstLoad = stepStatusesLoaded();
    await job.navigateToEditor(/Report received today/);
    await firstLoad;
    const toggle = page.getByRole("button", { name: "Track changes" });
    await expect(toggle).toBeDisabled();

    // The reviewer changes one word.
    const advance = await page.request.post(`/api/jobs/${job.jobId}/workflow/advance`, { headers });
    expect(advance.ok(), `advance: ${advance.status()} ${await advance.text()}`).toBe(true);
    await save("Denuncia recibida hoy");

    await page.reload();
    await expect(toggle).toBeEnabled();
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-pressed", "true");

    const marked = page.getByTestId("track-changes");
    await expect(marked).toHaveCount(1);
    await expect(marked.locator("span.line-through")).toHaveText("enviada");
    await expect(marked.locator("span.bg-yellow-300")).toHaveText("recibida");

    // Editing the row shows plain text in the editor, with no marks.
    await page.locator("[data-translation-cell]").first().click();
    await expect(page.getByTestId("track-changes")).toHaveCount(0);
  });
});
