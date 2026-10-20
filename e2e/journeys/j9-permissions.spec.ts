/**
 * J9 — Permission Boundaries / RBAC (Adversarial)
 *
 * Core purpose: members can translate but can't do admin ops.
 * UI hides forbidden actions. Direct API access returns 403.
 *
 * NOT NEGOTIABLE: "button hidden" alone is insufficient — must also test
 * direct API calls to admin endpoints. A member who knows the URL
 * shouldn't be able to bypass the UI guard.
 *
 * Creates a real second user via invite flow (not dev-login hack).
 */
import { test, expect } from "@playwright/test";
import {
  inviteMember,
  login,
  createProjectViaAPI,
  createMTEngineViaAPI,
  setProjectMTViaAPI,
  uploadFileViaAPI,
  pretranslateFileViaAPI,
  deleteProjectViaAPI,
  deleteMTEngineViaAPI,
  createTMViaAPI,
  importTMXViaAPI,
  setProjectTMsViaAPI,
  deleteTMViaAPI,
  selectWordInActiveSource,
  segmentEditor,
} from "./helpers";

const API = "";

// One searchable entry, en→es (the project is en→es-mx; concordance falls back
// to the base language, same as J11).
const J9_TMX = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header creationtool="test" datatype="plaintext" segtype="sentence" adminlang="en" srclang="en"/>
  <body>
    <tu tuid="j9-001"><tuv xml:lang="en"><seg>Reset your password.</seg></tuv><tuv xml:lang="es"><seg>Restablezca su contraseña.</seg></tuv></tu>
  </body>
</tmx>`;
const MEMBER_EMAIL = `j9-member-${Date.now()}@test.com`;
const MEMBER_PASSWORD = "J9TestPassword123!";

let ownerToken: string;
let memberToken: string;
let memberUserId: number;
let projectId: number;
let mtEngineId: number;
let concordanceTmId: number;
let tmIdFromSeed: number; // we'll use the Default TM from seed
let fileId: number;
let jobId: number;

/** Login as member. Installs the member Bearer BEFORE the first navigation so
 * LoginPage's dev-mode auto-login never mounts and cannot overwrite it with
 * the seed admin's token (testing.md pattern "Multi-context journey:
 * dev-mode auto-login clobbers your second user's auth"). Each test gets a
 * fresh context, so there are no cookies to clear. */
async function loginAsMember(page: import("@playwright/test").Page) {
  await page.context().addInitScript((token) => {
    window.localStorage.setItem("token", token);
  }, memberToken);
  await page.goto("/");
}

test.describe.serial("J9 — Permissions", () => {
  // ─── Phase A: Setup — invite + create member ──────────────────────

  test("A1: owner invites a new member", async ({ page }) => {
    await login(page);
    ownerToken = await page.evaluate(() => localStorage.getItem("token")) as string;

    const member = await inviteMember(page, MEMBER_EMAIL, "linguist");
    memberToken = member.token;
    memberUserId = member.userId;
    expect(memberUserId, "should get member user id").toBeTruthy();
  });

  test("A2: owner creates a project for the member to work on", async ({ page }) => {
    await login(page);
    mtEngineId = await createMTEngineViaAPI(page, "J9 MT", "mock", "");
    projectId = await createProjectViaAPI(page, "J9 Permissions Project", "en", ["es-mx"]);
    await setProjectMTViaAPI(page, projectId, [
      { mt_engine_id: mtEngineId, source_language: "en", target_language: "es-mx" },
    ]);

    // Upload a file + pre-translate so member has something to work with
    const result = await uploadFileViaAPI(page, projectId, "j9-test.txt", "Hello world.\nReset password.\n", "text/plain");
    fileId = result.data?.id ?? result.id;

    const headers = { Authorization: `Bearer ${ownerToken}` };
    const jobsRes = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    jobId = (await jobsRes.json()).data[0].id;

    // Assign the linguist to the job so they can work it (linguists
    // only reach jobs assigned to them).
    const assignRes = await page.request.post(`${API}/api/jobs/${jobId}/assign`, {
      headers,
      data: { user_id: memberUserId },
    });
    expect(assignRes.ok(), `assign to linguist failed: ${assignRes.status()}`).toBe(true);

    await pretranslateFileViaAPI(page, projectId, fileId);
  });

  // ─── Phase B: Member forbidden actions (API-level) ────────────────

  test("B1: member CANNOT delete a project (403)", async ({ page }) => {
    const headers = { Authorization: `Bearer ${memberToken}` };

    const res = await page.request.delete(`${API}/api/projects/${projectId}`, { headers });
    // OBSERVABLE OUTCOME: 403 Forbidden, not 200
    expect(res.status(), "member should get 403 on project delete").toBe(403);
  });

  test("B2: member CANNOT create an MT engine (403)", async ({ page }) => {
    const headers = { Authorization: `Bearer ${memberToken}` };

    const res = await page.request.post(`${API}/api/mt-engines`, {
      headers,
      data: { name: "Hacked Engine", engine_type: "mock", api_url: "" },
    });
    expect(res.status(), "member should get 403 on MT engine create").toBe(403);
  });

  test("B2b: member CANNOT create an MT router (403) — same manager-level gate as engines", async ({ page }) => {
    const headers = { Authorization: `Bearer ${memberToken}` };

    const res = await page.request.post(`${API}/api/mt-routers`, {
      headers,
      data: { name: "Hacked Router", default: { mt_engine_id: mtEngineId }, rules: [] },
    });
    expect(res.status(), "member should get 403 on MT router create").toBe(403);

    // Nothing slipped through: the owner sees no router by that name.
    const ownerList = await page.request.get(`${API}/api/mt-routers`, {
      headers: { Authorization: `Bearer ${ownerToken}` },
    });
    const names = ((await ownerList.json()).data as Array<{ name: string }>).map((r) => r.name);
    expect(names, "403 must not have created the router").not.toContain("Hacked Router");
  });

  test("B3: member CANNOT change project settings (403)", async ({ page }) => {
    const headers = { Authorization: `Bearer ${memberToken}` };

    const res = await page.request.patch(`${API}/api/projects/${projectId}/settings`, {
      headers,
      data: { fuzzy_threshold: 0.99 },
    });
    expect(res.status(), "member should get 403 on settings change").toBe(403);
  });

  // ─── Phase C: Member CAN translate (positive test) ────────────────

  test("C1: linguist canNOT view the project but CAN read their assigned job's translations", async ({ page }) => {
    const headers = { Authorization: `Bearer ${memberToken}` };

    // Linguists are blocked from the project-management surface.
    const projRes = await page.request.get(`${API}/api/projects/${projectId}`, { headers });
    expect(projRes.status(), "linguist should get 403 on project read").toBe(403);

    // But they CAN read the translations of the job assigned to them.
    const transRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    expect(transRes.ok(), "linguist should read their assigned job's translations").toBe(true);
    const translations = (await transRes.json()).data;
    expect(translations.length, "should see 2 segments").toBe(2);
  });

  test("C2: member CAN confirm a segment (the whole point of members)", async ({ page }) => {
    const headers = { Authorization: `Bearer ${memberToken}` };

    // Get first translation
    const transRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    const translations = (await transRes.json()).data.sort((a: any, b: any) => a.id - b.id);
    const seg = translations[0];

    // Confirm it
    const confirmRes = await page.request.put(
      `${API}/api/files/${fileId}/translations/${seg.id}`,
      {
        headers,
        data: { human_text: seg.active_text || "[ES-MX] Hello world." },
      },
    );
    expect(confirmRes.ok(), `member confirm failed: ${confirmRes.status()}`).toBe(true);

    // Verify it's approved
    const verifyRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    const verified = (await verifyRes.json()).data.sort((a: any, b: any) => a.id - b.id);
    expect(verified[0].status, "segment should be approved by member").toBe("approved");
  });

  test("C3: owner assigns a TM with searchable entries", async ({ page }) => {
    await login(page);
    concordanceTmId = await createTMViaAPI(page, "J9 Concordance TM");
    await importTMXViaAPI(page, concordanceTmId, J9_TMX, "j9-concordance.tmx");
    await setProjectTMsViaAPI(page, projectId, [
      { tm_id: concordanceTmId, source_language: "en", target_language: "es-mx", mode: "read", priority: 1 },
    ]);
  });

  // Pins that the CAT editor takes its project context from the job response,
  // which linguists CAN read — GET /api/projects is 403 for them (C1). Without
  // a project id, concordance opens prefilled and silently sends nothing.
  //
  // The `waitForResponse` is the load-bearing assertion: it fails if no
  // concordance request is issued.
  test("C4: linguist can run a concordance search in the editor", async ({ page }) => {
    await loginAsMember(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset password/i).first()).toBeVisible({ timeout: 10000 });

    // Activate the segment whose source holds the word we want to look up.
    await page.getByText(/Reset password/i).first().click();
    await expect(page.locator('[data-testid="active-source-box"]')).toBeVisible({ timeout: 5000 });
    // The row's editor takes focus a tick after it mounts; that focus would
    // collapse a selection made before it.
    await expect(segmentEditor(page).first()).toBeFocused({ timeout: 5000 });
    await selectWordInActiveSource(page, "password");

    const searchDone = page.waitForResponse(
      (r) => r.url().includes("/api/tms/concordance") && r.status() === 200,
      { timeout: 10000 },
    );
    const isMac = process.platform === "darwin";
    await page.keyboard.press(isMac ? "ControlOrMeta+k" : "Control+k");
    await searchDone;

    const modal = page.getByRole("dialog");
    await expect(modal, "concordance modal should open for a linguist").toBeVisible();
    await expect(
      modal.locator("input").first(),
      "query should be prefilled from the linguist's selection",
    ).toHaveValue("password");
    await expect(
      modal.getByText(/Restablezca su contraseña/i),
      "linguist should see the TM hit",
    ).toBeVisible({ timeout: 5000 });
    // Not merely "results appeared" — the missing-context error must be absent,
    // so a future regression can't satisfy this test by rendering an error.
    await expect(
      modal.getByRole("alert"),
      "no missing-project-context error for a linguist",
    ).toBeHidden();

    await page.keyboard.press("Escape");
  });

  // ─── Phase D: Member UI guards ────────────────────────────────────

  // The sidebar's MT entry (routers / engines / prompts) is the link named
  // exactly "MT". Anchored so a future "MT quality" link can't satisfy it.
  const MT_LINK = /^MT$/;

  test("D0: owner sidebar shows the MT link (positive control for D1)", async ({ page }) => {
    await login(page);
    await page.goto("/");
    await expect(page.getByRole("link", { name: MT_LINK }),
      "owner must see the MT link — otherwise D1's 'hidden for member' check proves nothing",
    ).toBeVisible({ timeout: 10000 });
  });

  test("D1: member sidebar hides admin links (MT, Org Settings)", async ({ page }) => {
    await loginAsMember(page);
    await page.goto("/");
    // Linguists land on /tasks; wait for the Tasks view to load.
    await expect(page.getByRole("heading", { name: /tasks/i }).first()).toBeVisible({ timeout: 10000 });

    // OBSERVABLE OUTCOME: admin-only links should NOT be visible
    const sidebar = page.locator("nav, [role='navigation'], aside").first();
    await expect(sidebar).toBeVisible();

    // These links should be hidden for members
    const mtLink = page.getByRole("link", { name: MT_LINK });
    const orgSettingsLink = page.getByRole("link", { name: /org.*settings|organization/i });

    await expect(mtLink, "MT link should be hidden for member").toHaveCount(0);
    await expect(orgSettingsLink, "Org Settings link should be hidden for member").toHaveCount(0);
  });

  test("D2: member navigating to /mt gets redirected or blocked + API returns 403", async ({ page }) => {
    await loginAsMember(page);

    // PRIMARY ASSERTION (audit P3 CP-3): the API must enforce RBAC. Without
    // this, a redirect-to-/ that hides the page in the UI looks like a pass
    // even if the role guard was removed from the server. Direct API call
    // with the member's JWT must 403 — that proves the boundary is real.
    const apiRes = await page.request.get(`${API}/api/mt-engines`, {
      headers: { Authorization: `Bearer ${memberToken}` },
    });
    expect(
      apiRes.status(),
      `member calling GET /api/mt-engines must get 403 (got ${apiRes.status()})`,
    ).toBe(403);

    // MT routers share the engines' manager-level gate: read AND write 403.
    const routersRes = await page.request.get(`${API}/api/mt-routers`, {
      headers: { Authorization: `Bearer ${memberToken}` },
    });
    expect(routersRes.status(),
      `member calling GET /api/mt-routers must get 403 (got ${routersRes.status()})`,
    ).toBe(403);

    // Linguists are fully blocked from the MT-engines surface — even
    // the languages sub-endpoint (managers configure engines, not linguists).
    const enginesRes = await page.request.get(`${API}/api/mt-engines`, {
      headers: { Authorization: `Bearer ${ownerToken}` },
    });
    const engineId = (await enginesRes.json()).data[0].id;
    const langRes = await page.request.get(`${API}/api/mt-engines/${engineId}/languages`, {
      headers: { Authorization: `Bearer ${memberToken}` },
    });
    expect(
      langRes.status(),
      `linguist GET /api/mt-engines/:id/languages must be 403 (got ${langRes.status()})`,
    ).toBe(403);

    // Secondary: the UI blocks too. `linguistBlock` on the /mt route sends a
    // linguist to /tasks, so there is exactly one outcome to assert.
    await page.goto("/mt");
    await expect(page).toHaveURL(/\/tasks/);
    await expect(
      page.getByRole("link", { name: /^(Routers|Engines|Prompts)$/ }),
      "member should NOT see the MT page (its Routers/Engines/Prompts tabs)",
    ).toHaveCount(0);
  });

  // ─── Phase E: Cleanup ─────────────────────────────────────────────

  test("E1: cleanup (as owner)", async ({ page }) => {
    await login(page); // back to owner
    await deleteProjectViaAPI(page, projectId);
    await deleteMTEngineViaAPI(page, mtEngineId);
    if (concordanceTmId) await deleteTMViaAPI(page, concordanceTmId);
    // Remove the invited member, or every run leaves one more member in the org.
    const removeRes = await page.request.delete(`${API}/api/members/${memberUserId}`, {
      headers: { Authorization: `Bearer ${ownerToken}` },
    });
    expect(removeRes.ok(), `E1: removing the invited member failed: ${removeRes.status()}`).toBe(true);
  });

  test.afterAll(async ({ browser }) => {
    // Belt-and-suspenders cleanup. Without this, a mid-suite
    // failure skips E1 and leaks the project + MT engine across runs.
    const page = await browser.newPage();
    try {
      await login(page);
      if (projectId) await deleteProjectViaAPI(page, projectId).catch(() => {});
      if (mtEngineId) await deleteMTEngineViaAPI(page, mtEngineId).catch(() => {});
      if (concordanceTmId) await deleteTMViaAPI(page, concordanceTmId).catch(() => {});
      if (memberUserId && ownerToken) {
        await page.request.delete(`${API}/api/members/${memberUserId}`, {
          headers: { Authorization: `Bearer ${ownerToken}` },
        }).catch(() => {});
      }
    } catch {
      // best-effort
    } finally {
      await page.close();
    }
  });
});
