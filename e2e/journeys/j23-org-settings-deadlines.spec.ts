/**
 * J23 — Org Settings + Notifications + Deadlines (Adversarial)
 *
 * Goal: lock down the "settings + state-across-surfaces" surface — change a
 * setting, trigger the thing, verify propagation — including the `bool`
 * partial-PATCH contract (absent field = keep current).
 *
 * Locale: ko-KR (Korean, Korea) — never used by any other journey. Mock
 * supports `ko` natively, so ko-KR exercises the base-code fallback
 * path; visual sanity check via the `[KO] ` prefix.
 *
 * Honest scope notes:
 *   - **Deadline notification firing** is j27's job. j23 asserts the
 *     deadline SET/CLEAR round-trip + that the field surfaces in GET /jobs/
 *     {id}.
 *   - **Email actually sent** is NOT testable in dev (console sender only —
 *     no SMTP). j23 covers only the notification PREFERENCES and the org
 *     notification RULES (the switches); it never checks that a notification
 *     is created.
 *   - **Workflow event-driven notification creation** (assignment, rejection,
 *     completion) requires a multi-step workflow — outside scope for j23.
 */
import { test, expect, type APIRequestContext } from "@playwright/test";
import { pretranslateFileViaAPI } from "../tests/helpers";
// j23 runs as its own fresh tenant, so calls pass the owner token explicitly
// instead of relying on the storageState token the helpers read.

const API = "";
const RUN_ID = Date.now();

let ownerToken: string;
let projectId: number;
let mtEngineId: number;
let fileId: number;
let jobId: number;

async function authHeader(token: string): Promise<Record<string, string>> {
  return { Authorization: `Bearer ${token}` };
}

async function get(req: APIRequestContext, url: string, token: string) {
  return req.get(url, { headers: await authHeader(token) });
}

async function put(req: APIRequestContext, url: string, body: any, token: string) {
  return req.put(url, {
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    data: body,
  });
}

async function patch(req: APIRequestContext, url: string, body: any, token: string) {
  return req.patch(url, {
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    data: body,
  });
}

async function deleteReq(req: APIRequestContext, url: string, token: string) {
  return req.delete(url, { headers: await authHeader(token) });
}

test.describe.serial("J23 — Org Settings + Notifications + Deadlines", () => {
  // ════════════════════════════════════════════════════════════════════════
  // Phase A — Setup: register a fresh tenant (so we can hard-delete it at end)
  // ════════════════════════════════════════════════════════════════════════

  test("A1: provision a fresh tenant + owner — owner token used throughout", async ({ page }) => {
    const email = `J23-owner-${RUN_ID}@example.com`;
    // Dev-only tenant provisioning.
    const res = await page.request.post(`${API}/api/auth/dev-login`, {
      headers: { "Content-Type": "application/json" },
      data: { email, tenant_name: `J23 Tenant ${RUN_ID}` },
    });
    expect(res.ok(), "A1: fresh tenant provision must succeed").toBe(true);
    const body = await res.json();
    ownerToken = body.token;
    expect(body.user.role).toBe("owner");

    // Inject the token into the page so existing helpers (which read from
    // localStorage) work for this fresh user instead of dev-login.
    await page.goto("/");
    await page.evaluate((t) => localStorage.setItem("token", t), ownerToken);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase B — Org settings (open_registration toggle)
  //
  // Org settings handler uses *bool (absent = keep current). We assert that
  // behavior + the cross-surface effect (admin role required for write).
  // ════════════════════════════════════════════════════════════════════════

  test("B1: GET /api/org/settings — fresh tenant defaults to open_registration=false", async ({ page }) => {
    const res = await get(page.request, `${API}/api/org/settings`, ownerToken);
    expect(res.status()).toBe(200);
    const body = await res.json();
    expect(body.open_registration,
      "B1: a freshly provisioned tenant must default to open_registration=false " +
      "(migration 002 column default, security default).",
    ).toBe(false);
  });

  test("B2: PUT toggles open_registration true, GET reflects it", async ({ page }) => {
    const setRes = await put(page.request, `${API}/api/org/settings`, { open_registration: true }, ownerToken);
    expect(setRes.status()).toBe(200);
    const setBody = await setRes.json();
    expect(setBody.open_registration, "B2: response echoes new value").toBe(true);

    // Independent re-read
    const getRes = await get(page.request, `${API}/api/org/settings`, ownerToken);
    const getBody = await getRes.json();
    expect(getBody.open_registration,
      "B2: GET after PUT must reflect the new value — proves the write actually persisted (not just echoed).",
    ).toBe(true);
  });

  test("B3: PUT with omitted open_registration field preserves current value (org settings already uses *bool tri-state)", async ({ page }) => {
    // Sanity: send an empty body. The handler decodes `OpenRegistration *bool` —
    // omitting it sets req.OpenRegistration to nil, handler skips the UPDATE.
    const res = await put(page.request, `${API}/api/org/settings`, {}, ownerToken);
    expect(res.status()).toBe(200);
    const getRes = await get(page.request, `${API}/api/org/settings`, ownerToken);
    const getBody = await getRes.json();
    expect(getBody.open_registration,
      "B3: empty PUT body must KEEP open_registration=true (set in B2). " +
      "If it flipped to false, handleUpdateOrgSettings regressed away from the *bool pattern.",
    ).toBe(true);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase C — Notification preferences (per-user, 4 bool fields)
  // ════════════════════════════════════════════════════════════════════════

  test("C1: GET /api/notification-preferences — a user with no stored row has every switch on", async ({ page }) => {
    const res = await get(page.request, `${API}/api/notification-preferences`, ownerToken);
    expect(res.status()).toBe(200);
    const body = await res.json();
    const prefs = body.data ?? body;
    expect(prefs.email_assignment, "C1: email_assignment defaults to true").toBe(true);
    expect(prefs.email_rejection, "C1: email_rejection defaults to true").toBe(true);
    expect(prefs.email_completion, "C1: email_completion defaults to true").toBe(true);
    expect(prefs.email_deadline, "C1: email_deadline defaults to true").toBe(true);
  });

  test("C2: PUT all 4 prefs to specific values, GET round-trips them", async ({ page }) => {
    const desired = {
      email_assignment: true,
      email_rejection:  false,
      email_completion: true,
      email_deadline:   false,
    };
    const setRes = await put(page.request, `${API}/api/notification-preferences`, desired, ownerToken);
    expect(setRes.status()).toBe(200);

    const getRes = await get(page.request, `${API}/api/notification-preferences`, ownerToken);
    const got = await getRes.json();
    // The response is wrapped in { data: {...} } per httputil.Data
    const prefs = got.data ?? got;
    expect(prefs.email_assignment, "C2: email_assignment round-trips true").toBe(true);
    expect(prefs.email_rejection,  "C2: email_rejection round-trips false").toBe(false);
    expect(prefs.email_completion, "C2: email_completion round-trips true").toBe(true);
    expect(prefs.email_deadline,   "C2: email_deadline round-trips false").toBe(false);
  });

  test("C3: partial PUT (omit email_completion) must NOT silently flip it to false", async ({ page }) => {
    // Step 1: set ALL 4 to true via a full PUT.
    const allTrue = {
      email_assignment: true,
      email_rejection:  true,
      email_completion: true,
      email_deadline:   true,
    };
    const setRes = await put(page.request, `${API}/api/notification-preferences`, allTrue, ownerToken);
    expect(setRes.status(), "C3 setup: bulk-true PUT must succeed").toBe(200);

    // Step 2: send a PARTIAL PUT that omits email_completion.
    // A correctly-designed handler treats absent fields as keep-current.
    const partial = {
      email_assignment: true,
      email_rejection:  true,
      // email_completion intentionally omitted
      email_deadline:   true,
    };
    const partialRes = await put(page.request, `${API}/api/notification-preferences`, partial, ownerToken);
    expect(partialRes.status(), "C3: partial PUT must succeed").toBe(200);

    // Step 3: GET and assert email_completion is STILL true.
    const getRes = await get(page.request, `${API}/api/notification-preferences`, ownerToken);
    const prefs = (await getRes.json()).data ?? (await getRes.json());
    expect(prefs.email_completion,
      "C3: partial PUT omitting email_completion must keep the current value " +
      "(absent → keep current, present → use new value).",
    ).toBe(true);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase D — Org notification rules (admin-level, 4 bool fields)
  //   Same partial-PUT contract as Phase C.
  // ════════════════════════════════════════════════════════════════════════

  test("D1: GET /api/org/notification-rules — defaults exist (all-true if no row yet)", async ({ page }) => {
    const res = await get(page.request, `${API}/api/org/notification-rules`, ownerToken);
    expect(res.status()).toBe(200);
    const body = await res.json();
    const rules = body.data ?? body;
    // Per HandleGetOrgRules: missing row → defaults to all-true.
    expect(rules.notify_assignment, "D1: default notify_assignment=true").toBe(true);
    expect(rules.notify_rejection,  "D1: default notify_rejection=true").toBe(true);
    expect(rules.notify_completion, "D1: default notify_completion=true").toBe(true);
    expect(rules.notify_deadline,   "D1: default notify_deadline=true").toBe(true);
  });

  test("D2: PUT all 4 rules to specific values, GET round-trips them", async ({ page }) => {
    const desired = {
      notify_assignment: false,
      notify_rejection:  true,
      notify_completion: false,
      notify_deadline:   true,
    };
    const setRes = await put(page.request, `${API}/api/org/notification-rules`, desired, ownerToken);
    expect(setRes.status()).toBe(200);

    const getRes = await get(page.request, `${API}/api/org/notification-rules`, ownerToken);
    const rules = (await getRes.json()).data ?? (await getRes.json());
    expect(rules.notify_assignment).toBe(false);
    expect(rules.notify_rejection).toBe(true);
    expect(rules.notify_completion).toBe(false);
    expect(rules.notify_deadline).toBe(true);
  });

  test("D3: partial PUT (omit notify_deadline) must NOT silently flip it", async ({ page }) => {
    // Step 1: bulk-true via full PUT
    await put(page.request, `${API}/api/org/notification-rules`, {
      notify_assignment: true,
      notify_rejection:  true,
      notify_completion: true,
      notify_deadline:   true,
    }, ownerToken);

    // Step 2: partial PUT omitting notify_deadline
    const partialRes = await put(page.request, `${API}/api/org/notification-rules`, {
      notify_assignment: true,
      notify_rejection:  true,
      notify_completion: true,
      // notify_deadline intentionally omitted
    }, ownerToken);
    expect(partialRes.status(), "D3: partial PUT must succeed").toBe(200);

    // Step 3: assert notify_deadline still true
    const getRes = await get(page.request, `${API}/api/org/notification-rules`, ownerToken);
    const rules = (await getRes.json()).data ?? (await getRes.json());
    expect(rules.notify_deadline,
      "D3 (C3 sibling): partial PUT omitting notify_deadline must keep the current value.",
    ).toBe(true);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase F — Job deadline set/clear round-trip + ko-KR pipeline sanity
  // ════════════════════════════════════════════════════════════════════════

  test("F1: setup project + MT engine + small file for the deadline tests (raw API, owner token)", async ({ page }) => {
    const headers = await authHeader(ownerToken);

    // Create MT engine
    const engRes = await page.request.post(`${API}/api/mt-engines`, {
      headers, data: { name: `J23 Mock ${RUN_ID}`, engine_type: "mock" },
    });
    expect(engRes.ok(), `F1: create MT engine: ${engRes.status()}`).toBe(true);
    mtEngineId = (await engRes.json()).data.id;

    // Create project
    const projRes = await page.request.post(`${API}/api/projects`, {
      headers, data: {
        name: `J23 Project ${RUN_ID}`, source_language: "en", target_languages: ["ko-KR"],
      },
    });
    expect(projRes.ok(), `F1: create project: ${projRes.status()}`).toBe(true);
    projectId = (await projRes.json()).data.id;

    // Disable pre-translate-on-upload (we'll trigger explicitly in F5)
    const settingsRes = await page.request.patch(`${API}/api/projects/${projectId}/settings`, {
      headers, data: { mt_on_upload: false },
    });
    expect(settingsRes.ok()).toBe(true);

    // Route ko-KR to the engine: one MT router, pointed at by the project.
    // (Cascade-deleted with the tenant in G1.)
    const routerRes = await page.request.post(`${API}/api/mt-routers`, {
      headers, data: { name: `J23 Router ${RUN_ID}`, default: {}, rules: [{ target_lang: "ko-KR", mt_engine_id: mtEngineId }] },
    });
    expect(routerRes.ok(), `F1: create MT router: ${routerRes.status()}`).toBe(true);
    const routerId = (await routerRes.json()).data.id as number;
    const mtRes = await page.request.patch(`${API}/api/projects/${projectId}/settings`, {
      headers, data: { mt_router_id: routerId },
    });
    expect(mtRes.ok(), `F1: point project at MT router: ${mtRes.status()}`).toBe(true);
    expect((await mtRes.json()).data.mt_router_id, "F1: settings echo the router").toBe(routerId);

    // Upload file (multipart)
    const uploadRes = await page.request.post(`${API}/api/projects/${projectId}/files`, {
      headers,
      multipart: {
        file: { name: "j23.txt", mimeType: "text/plain", buffer: Buffer.from("Hello deadline") },
      },
    });
    expect(uploadRes.ok(), `F1: upload file: ${uploadRes.status()}`).toBe(true);
    const uploadBody = await uploadRes.json();
    fileId = uploadBody.data?.id ?? uploadBody.id;

    // Find the job
    const jobsRes = await get(page.request, `${API}/api/projects/${projectId}/jobs`, ownerToken);
    const jobs = (await jobsRes.json()).data as Array<{ id: number; file_id: number }>;
    jobId = jobs.find((j) => j.file_id === fileId)!.id;
    expect(jobId, "F1: job for the uploaded file must exist").toBeGreaterThan(0);
  });

  // STALE: F2–F4 PATCH the removed `/api/jobs/{id}/deadline`
  // endpoint. The "Deadlines" refactor (migrations 046/047) moved deadlines to
  // per-workflow-step (`PATCH /api/jobs/{id}/workflow/steps/{stepID}/deadline`)
  // and dropped the `jobs.deadline` column. These need repointing to the
  // per-step route — tracked as follow-up, out of scope for the org work.
  test.skip("F2: PATCH job deadline (3 days from now), GET reflects it", async ({ page }) => {
    const future = new Date(Date.now() + 3 * 24 * 60 * 60 * 1000).toISOString();
    const res = await patch(page.request, `${API}/api/jobs/${jobId}/deadline`, { deadline: future }, ownerToken);
    expect(res.status(), `F2: set deadline: ${res.status()}`).toBe(200);

    const getRes = await get(page.request, `${API}/api/jobs/${jobId}`, ownerToken);
    const job = (await getRes.json()).data;
    expect(job.deadline,
      "F2: GET /jobs/{id} must include the deadline field after it was set. " +
      "Empty/null here means HandleSetDeadline didn't actually persist (or the GET response struct doesn't surface deadline).",
    ).toBeTruthy();
    // Format check: must contain a date-ish prefix
    expect(typeof job.deadline).toBe("string");
    expect(job.deadline.length, "F2: deadline string non-empty").toBeGreaterThan(0);
  });

  test.skip("F3: PATCH deadline back to null clears it, GET reflects empty", async ({ page }) => {
    const res = await patch(page.request, `${API}/api/jobs/${jobId}/deadline`, { deadline: null }, ownerToken);
    expect(res.status()).toBe(200);

    const getRes = await get(page.request, `${API}/api/jobs/${jobId}`, ownerToken);
    const job = (await getRes.json()).data;
    expect(job.deadline,
      "F3: deadline must be cleared (empty/falsy) after PATCH with null. " +
      "If the previous date string still shows, the null path doesn't reach the SET clause.",
    ).toBeFalsy();
  });

  test.skip("F4: invalid deadline format returns 400 (not 500)", async ({ page }) => {
    const res = await patch(page.request, `${API}/api/jobs/${jobId}/deadline`, { deadline: "not-a-date" }, ownerToken);
    expect(res.status(),
      "F4: malformed deadline must 400 — handler does time.Parse(RFC3339) and returns BadRequest on parse error",
    ).toBe(400);
  });

  test("F5: pipeline sanity — pre-translate produces [KO] prefix (proves ko-KR fallback path end-to-end)", async ({ page }) => {
    // Shared helper with the explicit owner token (j23 holds tokens per user
    // for its multi-user phases). It tolerates the config-missing 400 (this
    // project has an MT engine but no TM) and waits out the upload hook's
    // auto-enqueued run on a 409.
    await pretranslateFileViaAPI(page, projectId, fileId, ownerToken);

    const transRes = await get(page.request, `${API}/api/jobs/${jobId}/translations`, ownerToken);
    const translations = (await transRes.json()).data as Array<{ active_text: string | null }>;
    expect(translations.length, "F5: at least 1 translation").toBeGreaterThanOrEqual(1);
    for (const t of translations) {
      expect(t.active_text,
        "F5: every segment must have non-null mt_text (sanity that the new tenant's engine assignment works)",
      ).toBeTruthy();
      expect(t.active_text!.startsWith("[KO] "),
        `F5: mt_text must start with "[KO] " — proves ko-KR → ko base-code fallback ran for this tenant. ` +
        `Got ${JSON.stringify(t.active_text!.slice(0, 30))}.`,
      ).toBe(true);
    }
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase G — Cleanup: hard-delete the entire tenant (cascades everything)
  // ════════════════════════════════════════════════════════════════════════

  test("G1: delete the j23 tenant — closed at once, data deleted in the background", async ({ page }) => {
    expect(ownerToken, "A1 must have provisioned the tenant").toBeTruthy();
    const res = await deleteReq(page.request, `${API}/api/tenants/current`, ownerToken);
    expect(res.status()).toBe(202);
    // Closed from the moment the request answers: the owner is shut out.
    const after = await get(page.request, `${API}/api/projects`, ownerToken);
    expect(after.status()).toBe(403);
  });

  test.afterAll(async ({ browser }) => {
    if (!ownerToken) return;
    const page = await browser.newPage();
    try {
      await deleteReq(page.request, `${API}/api/tenants/current`, ownerToken).catch(() => {});
    } catch {} finally {
      await page.close();
    }
  });
});
