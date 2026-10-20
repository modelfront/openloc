import { test, expect, type Page } from "@playwright/test";
import { login, createIsolatedCATJob, createTMViaAPI, importTMXViaAPI, deleteTMViaAPI, segmentEditor } from "./helpers";
import {
  setProjectTMsViaAPI,
  createMTEngineViaAPI,
  setProjectMTViaAPI,
  deleteMTEngineViaAPI,
  deleteProjectViaAPI,
} from "../journeys/helpers";

// Inserting a TM match or MT suggestion from the Resources panel records it on
// the segment, as pre-translation does: the Match column shows the TM % or
// MT, and it is still there after a reload.

const RUN = Date.now();
const TMX = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header creationtool="panel-insert" datatype="plaintext" segtype="sentence" adminlang="en" srclang="en"/>
  <body>
    <tu><tuv xml:lang="en"><seg>Hello, world</seg></tuv><tuv xml:lang="es"><seg>Hola, mundo</seg></tuv></tu>
    <tu><tuv xml:lang="en"><seg>Thank you very much</seg></tuv><tuv xml:lang="es"><seg>Muchas gracias</seg></tuv></tu>
  </body>
</tmx>`;

const isDraftSave = (page: Page) =>
  page.waitForResponse((r) => r.request().method() === "PUT" && /\/translations\/\d+\/draft$/.test(r.url()));

test.describe("CAT editor — Resources panel insert", () => {
  // Each delete is registered right after its create, so a failure partway
  // through setup still removes what was made. They run in the order they
  // were registered: the project goes first, as its TM assignment and MT
  // router block deleting the TM and the engine.
  let cleanups: (() => Promise<void>)[] = [];

  test.afterEach(async () => {
    for (const fn of cleanups) await fn();
    cleanups = [];
  });

  test("an inserted TM match (click or Ctrl/Cmd+1) and MT suggestion show in the Match column", async ({ page }) => {
    // A job, a TM, an MT engine, three inserts and a reload: more than the
    // default budget under suite load, and a timeout runs the cleanup that
    // deletes the project mid-test.
    test.slow();
    await login(page);
    const job = await createIsolatedCATJob(page, {
      fileName: `panel-insert-${RUN}.txt`,
      content: "Hello world\nGood morning\nThank you very much!\n",
      targetLanguages: ["es"],
    });
    cleanups.push(() => deleteProjectViaAPI(page, job.projectId));
    const tmId = await createTMViaAPI(page, `Panel insert TM ${RUN}`, { source: "en", targets: ["es"] });
    cleanups.push(() => deleteTMViaAPI(page, tmId));
    const engineId = await createMTEngineViaAPI(page, `Panel insert MT ${RUN}`, "mock");
    cleanups.push(() => deleteMTEngineViaAPI(page, engineId));
    await importTMXViaAPI(page, tmId, TMX);
    await setProjectTMsViaAPI(page, job.projectId, [
      { tm_id: tmId, source_language: "en", target_language: "es", mode: "read_write", priority: 0 },
    ]);
    await setProjectMTViaAPI(page, job.projectId, [{ mt_engine_id: engineId, target_language: "es" }]);

    await job.navigateToEditor(/Hello world/);
    const sidebar = page.getByTestId("editor-sidebar");

    // TM: the fuzzy match's % lands in the row's Match column.
    const tmRow = page.locator('[data-segment-idx="0"]');
    await tmRow.getByText("Hello world").click();
    const card = sidebar.getByTestId("tm-match-card").filter({ hasText: "Hola, mundo" });
    await expect(card).toBeVisible({ timeout: 10000 });
    const pct = (await card.innerText()).match(/(\d+)%/)?.[1];
    expect(pct, "the card shows a match %").toBeTruthy();
    expect(Number(pct)).toBeLessThan(100);

    const tmSave = isDraftSave(page);
    await card.click();
    expect((await tmSave).status()).toBe(200);
    await expect(tmRow.getByTitle("Fuzzy TM match")).toHaveText(`${pct}%`);

    // MT: the suggestion inserts as MT.
    const mtRow = page.locator('[data-segment-idx="1"]');
    await mtRow.getByText("Good morning").first().click();
    const suggestion = sidebar.getByTestId("mt-suggestion");
    await expect(suggestion).toContainText("Good morning", { timeout: 10000 });
    const mtSave = isDraftSave(page);
    await suggestion.click();
    expect((await mtSave).status()).toBe(200);
    await expect(mtRow.getByTitle("Machine translated")).toBeVisible();

    // Ctrl/Cmd+1 inserts the first hit the same way.
    const keyRow = page.locator('[data-segment-idx="2"]');
    await keyRow.getByText("Thank you very much!").first().click();
    const keyCard = sidebar.getByTestId("tm-match-card").filter({ hasText: "Muchas gracias" });
    await expect(keyCard).toBeVisible({ timeout: 10000 });
    const keyPct = (await keyCard.innerText()).match(/(\d+)%/)?.[1];
    expect(Number(keyPct)).toBeLessThan(100);
    const keySave = isDraftSave(page);
    await page.keyboard.press(process.platform === "darwin" ? "Meta+1" : "Control+1");
    expect((await keySave).status()).toBe(200);
    await expect(keyRow.getByTitle("Fuzzy TM match")).toHaveText(`${keyPct}%`);

    // All three are stored, not just drawn: they survive a reload.
    await page.reload();
    await expect(page.locator('[data-segment-idx="0"]').getByTitle("Fuzzy TM match")).toHaveText(`${pct}%`, {
      timeout: 10000,
    });
    await expect(page.locator('[data-segment-idx="1"]').getByTitle("Machine translated")).toBeVisible();
    await expect(page.locator('[data-segment-idx="2"]').getByTitle("Fuzzy TM match")).toHaveText(`${keyPct}%`);
  });

  test("an in-context exact match reads 101% on its card and in the Match column", async ({ page }) => {
    // A job, a TM, a confirm, a TM poll of up to 10 s and a reload.
    test.slow();
    await login(page);
    const job = await createIsolatedCATJob(page, {
      fileName: `panel-insert-101-${RUN}.txt`,
      content: "Open the file.\nSave your work.\nClose the file.\n",
      targetLanguages: ["es"],
    });
    cleanups.push(() => deleteProjectViaAPI(page, job.projectId));
    const tmId = await createTMViaAPI(page, `Panel insert 101 TM ${RUN}`, { source: "en", targets: ["es"] });
    cleanups.push(() => deleteTMViaAPI(page, tmId));
    await setProjectTMsViaAPI(page, job.projectId, [
      { tm_id: tmId, source_language: "en", target_language: "es", mode: "read_write", priority: 0 },
    ]);

    // Confirming the middle segment writes it to the TM with its neighbours.
    await job.navigateToEditor(/Open the file/);
    const row = page.locator('[data-segment-idx="1"]');
    await row.getByText("Save your work.").first().click();
    await segmentEditor(page).first().click();
    await page.keyboard.type("Guarde su trabajo.");
    const confirm = page.waitForResponse(
      (r) => r.request().method() === "PUT" && /\/translations\/\d+$/.test(r.url()),
    );
    await page.keyboard.press("ControlOrMeta+Enter");
    expect((await confirm).status()).toBe(200);
    const headers = { Authorization: `Bearer ${job.token}` };
    await expect
      .poll(async () => {
        const res = await page.request.get(
          `/api/jobs/${job.jobId}/tm-matches?source_text=${encodeURIComponent("Save your work.")}`,
          { headers },
        );
        return ((await res.json()).matches ?? []).length;
      }, { timeout: 10000 })
      .toBeGreaterThan(0);

    // Back on the same segment: same sentence, same neighbours.
    await page.reload();
    const sameRow = page.locator('[data-segment-idx="1"]');
    await sameRow.getByText("Save your work.").first().click();
    const card = page.getByTestId("editor-sidebar").getByTestId("tm-match-card").filter({ hasText: "Guarde su trabajo." });
    await expect(card).toContainText("101%", { timeout: 10000 });

    const save = isDraftSave(page);
    await card.click();
    expect((await save).status()).toBe(200);
    await expect(sameRow.getByTitle(/101%/)).toHaveText("101%");
  });
});

