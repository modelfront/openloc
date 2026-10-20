/**
 * J28 — XLIFF Re-import Round-Trip Gauntlet (Adversarial)
 *
 * End-to-end verification of the XLIFF round-trip flow. The Go
 * integration tests in `internal/file/reimport_test.go` exercise the full
 * HTTP→DB stack via httptest + testcontainer; J28 covers the gap by hitting
 * the LIVE running server (port 8080) over the network. Catches:
 *
 *   - Route registration regressions (handler not wired in api.go)
 *   - Cross-cutting interactions (project + invite + tenant + auth + lock)
 *   - The actual deployed binary's behavior, not just the test binary's
 *
 * Each phase tries to break a separate invariant (the gauntlet pattern):
 *
 *   - B: happy round-trip — re-import lands the new targets, one event logged
 *   - C: force flow — approved segment skipped without force, applied with
 *   - D: locked segment never updated, even with force=true (locks > force)
 *   - E: empty <target/> = no-op
 *   - F: renumbered IDs do not match, even with the same source text
 *   - G: file-revision optimistic lock returns 409 with current_revision
 *   - H: > 50,000-segment cap returns 413 + X-Reimport-Max-Segments header
 *   - I: cross-tenant 404 (compound defense — both layers must hold)
 *   - J: a linguist not assigned to the task gets 403
 *   - K: file.reimported project events match the applied runs; dry-runs do NOT log
 *
 * Each segment is touched by exactly ONE phase to keep state isolated:
 *
 *   - segments 1+2 → B
 *   - segment 3   → C
 *   - segment 4   → D (locked)
 *   - segment 5   → E (empty target)
 *   - segment 6   → F (renumbered ID)
 *   - segment 7   → control (untouched)
 *
 * Honest scope notes:
 *   - This is API-level. The frontend dialog is exercised by the vitest tests
 *     in `web/src/components/file/ReimportXLIFFDialog.test.tsx`.
 *   - Anti-tautology RED proofs live with the Go tests; J28 verifies the
 *     green state against the running server.
 *   - `force_required` paths consume a request without an event; the
 *     audit-count phase K accounts for this exactly.
 *   - Each phase touches a different segment so cross-test state pollution
 *     is impossible by construction.
 */
import { test, expect, type APIRequestContext } from "@playwright/test";
import {
  inviteMember,
  login,
  createProjectViaAPI,
  uploadFileViaAPI,
  deleteProjectViaAPI,
} from "./helpers";

const API = "";
const RUN_ID = Date.now();

const MEMBER_EMAIL = `j28-member-${RUN_ID}@test.com`;
const MEMBER_PASSWORD = "MemberPass!23j28";
const TENANT2_EMAIL = `j28-tenant2-${RUN_ID}@test.com`;
const TENANT2_PASSWORD = "Tenant2Pass!23j28";

let ownerToken: string;
let ownerEmail: string;
let memberToken: string;
let memberUserId: number | undefined;
let tenant2Token: string;

let projectId: number;
let fileId: number;
/** Initial files.updated_at — used as the optimistic-lock revision token. */
let initialRevision: string;

/** segment_key (as it appears in DB) → translation_id for fr language. */
const translationByKey: Record<string, number> = {};

// ─── XLIFF fixtures (inline so this journey doesn't share with j13) ────────

/**
 * Initial upload: 7 trans-units, no <target> elements. The upload handler
 * creates 7 segments + 7 untranslated translations (one per target language).
 * Sources are distinct, so phase F's renumbered unit has exactly one segment
 * with the same source, which it still must not match.
 */
const initialUploadXLIFF = `<?xml version="1.0" encoding="utf-8"?>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
  <file source-language="en" target-language="fr">
    <body>
      <trans-unit id="1"><source>S1: Hello world</source></trans-unit>
      <trans-unit id="2"><source>S2: Goodbye for now</source></trans-unit>
      <trans-unit id="3"><source>S3: Click here to continue</source></trans-unit>
      <trans-unit id="4"><source>S4: Submit form</source></trans-unit>
      <trans-unit id="5"><source>S5: Reset password</source></trans-unit>
      <trans-unit id="6"><source>S6: Confirm action please</source></trans-unit>
      <trans-unit id="7"><source>S7: Welcome back</source></trans-unit>
    </body>
  </file>
</xliff>`;

/** Helper — build an XLIFF body with the given trans-units. */
function buildXLIFF(units: { id: string; source: string; target?: string }[], targetLang = "fr"): string {
  const body = units.map(u => {
    if (u.target === undefined) {
      return `<trans-unit id="${u.id}"><source>${u.source}</source></trans-unit>`;
    }
    return `<trans-unit id="${u.id}"><source>${u.source}</source><target>${u.target}</target></trans-unit>`;
  }).join("\n      ");
  return `<?xml version="1.0" encoding="utf-8"?>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
  <file source-language="en" target-language="${targetLang}">
    <body>
      ${body}
    </body>
  </file>
</xliff>`;
}

// ─── Helpers ───────────────────────────────────────────────────────────────

async function bearerHeader(token: string): Promise<Record<string, string>> {
  return { Authorization: `Bearer ${token}` };
}

interface SegmentRow { id: number; segment_key: string | null; source_text: string }
interface TranslationRow { id: number; segment_id: number; status: string; active_text: string | null; locked: boolean }

async function loadTranslationsByKey(req: APIRequestContext, headers: Record<string, string>) {
  // Two-call lookup: segments give us key→segment_id, then translations give
  // us segment_id→translation_id. We don't have a single endpoint that joins
  // them; this matches what the dialog does internally.
  const segRes = await req.get(`${API}/api/files/${fileId}/segments`, { headers });
  expect(segRes.ok(), `loadTranslationsByKey: segments fetch ${segRes.status()}`).toBe(true);
  const segments = (await segRes.json()).data as SegmentRow[];

  const tRes = await req.get(`${API}/api/files/${fileId}/translations?lang=fr`, { headers });
  expect(tRes.ok(), `loadTranslationsByKey: translations fetch ${tRes.status()}`).toBe(true);
  const translations = (await tRes.json()).data as TranslationRow[];

  const segIdByKey = new Map<string, number>();
  for (const s of segments) {
    if (s.segment_key) segIdByKey.set(s.segment_key, s.id);
  }
  return { segments, translations, segIdByKey };
}

interface ReimportResult {
  updated: number;
  skipped_locked: number;
  skipped_approved: number;
  untouched_no_change: number;
  extra_in_upload: number;
  missing_from_upload: number;
  force_required: boolean;
  applied: boolean;
}

async function reimportViaAPI(
  req: APIRequestContext,
  token: string,
  fileID: number,
  xliff: string,
  query: { lang: string; dry_run?: boolean; force?: boolean; revision?: string },
) {
  const params = new URLSearchParams();
  params.set("lang", query.lang);
  if (query.dry_run) params.set("dry_run", "true");
  if (query.force) params.set("force", "true");
  if (query.revision) params.set("revision", query.revision);
  return req.post(
    `${API}/api/files/${fileID}/reimport-xliff?${params.toString()}`,
    {
      headers: await bearerHeader(token),
      multipart: {
        file: { name: "reimport.xliff", mimeType: "application/xml", buffer: Buffer.from(xliff) },
      },
    },
  );
}

async function getActiveText(req: APIRequestContext, headers: Record<string, string>, key: string): Promise<{ active_text: string | null; status: string; locked: boolean }> {
  const { translations, segIdByKey } = await loadTranslationsByKey(req, headers);
  const segId = segIdByKey.get(key);
  if (!segId) throw new Error(`getActiveText: no segment with key=${key}`);
  const t = translations.find(t => t.segment_id === segId);
  if (!t) throw new Error(`getActiveText: no fr translation for segment_key=${key}`);
  return { active_text: t.active_text, status: t.status, locked: t.locked };
}

interface ReimportEvent {
  file_id: number | null;
  actor: { email: string } | null;
  details: { target_language: string; updated: number; force: boolean };
}

// The file's applied re-imports, newest first: its `file.reimported` project events.
async function getReimportEvents(req: APIRequestContext, headers: Record<string, string>): Promise<ReimportEvent[]> {
  const r = await req.get(`${API}/api/projects/${projectId}/events?type=file.reimported&limit=200`, { headers });
  expect(r.ok(), `events fetch ${r.status()}`).toBe(true);
  return ((await r.json()).data as ReimportEvent[]).filter(e => e.file_id === fileId);
}

async function getHistoryCount(req: APIRequestContext, headers: Record<string, string>): Promise<number> {
  return (await getReimportEvents(req, headers)).length;
}

test.describe.serial("J28 — XLIFF Re-import Round-Trip Gauntlet", () => {
  // ════════════════════════════════════════════════════════════════════════
  // Phase A — Setup
  // ════════════════════════════════════════════════════════════════════════

  test("A1: owner logs in, creates project (en→fr) + uploads 7-segment XLIFF", async ({ page }) => {
    await login(page);
    ownerToken = (await page.evaluate(() => localStorage.getItem("token"))) as string;
    expect(ownerToken).toBeTruthy();

    // Capture the owner's email dynamically so K's history-row assertion
    // doesn't hard-code the dev-seed user. Different envs (CI, fresh dev
    // boxes, alternate dev users) will all work.
    const meRes = await page.request.get(`${API}/api/auth/me`, {
      headers: await bearerHeader(ownerToken),
    });
    expect(meRes.ok(), `A1: /api/auth/me must return 200: ${meRes.status()}`).toBe(true);
    const meBody = await meRes.json();
    ownerEmail = meBody.data?.email ?? meBody.email;
    expect(ownerEmail, "A1: owner email must be present in /api/auth/me response").toBeTruthy();

    projectId = await createProjectViaAPI(page, `J28 Project ${RUN_ID}`, "en", ["fr"]);

    const upload = await uploadFileViaAPI(
      page, projectId, "j28-source.xliff", initialUploadXLIFF, "application/xml",
    );
    fileId = upload.data.id;
    initialRevision = upload.data.updated_at;
    expect(fileId).toBeGreaterThan(0);
    expect(initialRevision, "A1: file.updated_at must be present — used as revision lock token").toBeTruthy();

    // Verify the 7 segments + 7 untranslated fr translations landed.
    const headers = await bearerHeader(ownerToken);
    const { segments, translations } = await loadTranslationsByKey(page.request, headers);
    expect(segments.length, "A1: must have 7 segments after upload").toBe(7);
    expect(translations.length, "A1: must have 7 fr translations").toBe(7);
    for (const t of translations) {
      // 'untranslated' is derived: raw status is
      // 'needs_review' with no active_text. The empty active_text below is the
      // canonical untranslated check.
      expect(t.status, "A1: fresh translations start at needs_review").toBe("needs_review");
      expect(t.active_text, "A1: untranslated translations have no active_text").toBeNull();
    }
    for (const s of segments) {
      if (s.segment_key) {
        const t = translations.find(t => t.segment_id === s.id);
        if (t) translationByKey[s.segment_key] = t.id;
      }
    }
    expect(Object.keys(translationByKey).length, "A1: all 7 segments mapped to translations").toBe(7);

    // Sanity: history is empty for a fresh file.
    expect(await getHistoryCount(page.request, headers), "A1: fresh file has zero re-import history rows").toBe(0);
  });

  test("A2: owner invites a linguist for the role-gate phase", async ({ page }) => {
    ({ userId: memberUserId, token: memberToken } = await inviteMember(page, MEMBER_EMAIL, "linguist", await bearerHeader(ownerToken)));
  });

  test("A3: register a SECOND tenant for cross-tenant attack tests", async ({ page }) => {
    const regRes = await page.request.post(`${API}/api/auth/dev-login`, {
      data: { email: TENANT2_EMAIL, tenant_name: `J28 Tenant2 ${RUN_ID}` },
    });
    expect(regRes.ok(), `A3: provision tenant 2: ${regRes.status()}`).toBe(true);
    tenant2Token = (await regRes.json()).token;
    expect(tenant2Token).toBeTruthy();
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase B — Happy round-trip
  // ════════════════════════════════════════════════════════════════════════

  test("B1: dry-run preview returns counts without writing — applied=false, no history row", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const before = await getHistoryCount(page.request, headers);

    const xliff = buildXLIFF([
      { id: "1", source: "S1: Hello world", target: "B1: Bonjour" },
      { id: "2", source: "S2: Goodbye for now", target: "B1: Au revoir" },
    ]);
    const r = await reimportViaAPI(page.request, ownerToken, fileId, xliff, { lang: "fr", dry_run: true });
    expect(r.status()).toBe(200);
    const result = (await r.json()).data as ReimportResult;

    expect(result.updated, "B1: dry-run reports same count as a real run would").toBe(2);
    expect(result.applied, "B1 (CORE invariant for dry-run): applied=false. If true, dry-run wrote.").toBe(false);

    // DB unchanged: segments 1+2 must still be untranslated.
    expect((await getActiveText(page.request, headers, "1")).active_text,
      "B1 defensive: dry-run must NOT have written segment 1.").toBeNull();
    expect((await getActiveText(page.request, headers, "2")).active_text).toBeNull();

    // Dry-runs do NOT generate history rows.
    expect(await getHistoryCount(page.request, headers),
      "B1 (audit log invariant): dry-runs must NOT add history rows. If history grew, " +
      "the audit log is polluted with previews — every modal interaction would log.",
    ).toBe(before);
  });

  test("B2: real apply writes segments 1+2 — Updated:2, Missing:5 (other segs not in upload)", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const before = await getHistoryCount(page.request, headers);

    const xliff = buildXLIFF([
      { id: "1", source: "S1: Hello world", target: "B2: Bonjour le monde" },
      { id: "2", source: "S2: Goodbye for now", target: "B2: Au revoir pour l'instant" },
    ]);
    const r = await reimportViaAPI(page.request, ownerToken, fileId, xliff, { lang: "fr" });
    expect(r.status()).toBe(200);
    const result = (await r.json()).data as ReimportResult;

    expect(result.applied, "B2: real apply must report applied=true").toBe(true);
    expect(result.updated, "B2: 2 of 5 untouched-by-upload segments updated").toBe(2);
    expect(result.missing_from_upload,
      "B2: 5 segments in DB but not in upload (3-7) — counted as missing.").toBe(5);

    // Anti-tautology: assert DB rows actually changed (not just response counts).
    const t1 = await getActiveText(page.request, headers, "1");
    expect(t1.active_text, "B2: segment 1 active_text must reflect new value.").toBe("B2: Bonjour le monde");
    expect(t1.status,
      "B2: re-import flips status to needs_review — not approved (the user hasn't reviewed yet).",
    ).toBe("needs_review");
    const t2 = await getActiveText(page.request, headers, "2");
    expect(t2.active_text).toBe("B2: Au revoir pour l'instant");

    expect(await getHistoryCount(page.request, headers), "B2: applied run logs exactly 1 history row").toBe(before + 1);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase C — Force flow (approved segments)
  // ════════════════════════════════════════════════════════════════════════

  test("C1 setup: pre-approve segment 3 via the standard human-update endpoint", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const transId = translationByKey["3"];
    expect(transId).toBeTruthy();

    const r = await page.request.put(`${API}/api/files/${fileId}/translations/${transId}`, {
      headers, data: { human_text: "C1: Cliquez approuvé", editing_time_ms: 0 },
    });
    expect(r.status(), `C1 setup: approve segment 3 via PUT translation: ${r.status()}`).toBe(200);

    const t3 = await getActiveText(page.request, headers, "3");
    expect(t3.status, "C1 setup: segment 3 must be approved before C2's force test").toBe("approved");
    expect(t3.active_text).toBe("C1: Cliquez approuvé");
  });

  test("C2: re-import target for approved segment WITHOUT force → applied=false, force_required=true", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const before = await getHistoryCount(page.request, headers);

    const xliff = buildXLIFF([{ id: "3", source: "S3: Click here to continue", target: "C2: Cliquez ici" }]);
    const r = await reimportViaAPI(page.request, ownerToken, fileId, xliff, { lang: "fr" });
    expect(r.status()).toBe(200);
    const result = (await r.json()).data as ReimportResult;

    expect(result.force_required,
      "C2 (ACL invariant): approved segment must set force_required when force omitted.",
    ).toBe(true);
    expect(result.applied,
      "C2: applied=false — force_required without force=true must NOT write.",
    ).toBe(false);
    expect(result.skipped_approved).toBe(1);
    expect(result.updated, "C2: zero updates when force_required+!force.").toBe(0);

    // DB unchanged: still C1's value.
    const t3 = await getActiveText(page.request, headers, "3");
    expect(t3.active_text, "C2: rejected re-import must NOT have overwritten approved segment.").toBe("C1: Cliquez approuvé");
    expect(t3.status, "C2: status stays approved").toBe("approved");

    expect(await getHistoryCount(page.request, headers),
      "C2: force-required-without-force must NOT log history (preview, not event).",
    ).toBe(before);
  });

  test("C3: re-import same target for approved segment WITH force=true → applied=true, segment overwritten", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const before = await getHistoryCount(page.request, headers);

    const xliff = buildXLIFF([{ id: "3", source: "S3: Click here to continue", target: "C3: Cliquez (force)" }]);
    const r = await reimportViaAPI(page.request, ownerToken, fileId, xliff, { lang: "fr", force: true });
    expect(r.status()).toBe(200);
    const result = (await r.json()).data as ReimportResult;

    expect(result.applied).toBe(true);
    expect(result.updated, "C3: force=true overrides approved → segment is updated.").toBe(1);
    expect(result.skipped_approved, "C3: with force, no skipped_approved.").toBe(0);

    const t3 = await getActiveText(page.request, headers, "3");
    expect(t3.active_text).toBe("C3: Cliquez (force)");
    expect(t3.status,
      "C3: force-applied re-import flips status to needs_review (not approved) — re-import is " +
      "a fresh edit, the user has to re-confirm. If status stays 'approved', the user " +
      "thinks the new text is reviewer-blessed when it isn't.",
    ).toBe("needs_review");

    expect(await getHistoryCount(page.request, headers), "C3: applied-with-force logs history").toBe(before + 1);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase D — Lock invariant (locks > force)
  // ════════════════════════════════════════════════════════════════════════

  test("D1 setup: lock segment 4's translation via the lock endpoint", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const transId = translationByKey["4"];

    // A lock approves the segment, so an empty one cannot be locked: give it a
    // translation first.
    const draft = await page.request.put(`${API}/api/files/${fileId}/translations/${transId}/draft`, {
      headers, data: { human_text: "D1 setup: Envoyer", editing_time_ms: 0 },
    });
    expect(draft.status(), `D1 setup: draft segment 4: ${draft.status()}`).toBe(200);

    const r = await page.request.post(`${API}/api/files/${fileId}/translations/${transId}/lock`, { headers });
    expect(r.status(), `D1 setup: lock segment 4: ${r.status()}`).toBe(200);

    const t4 = await getActiveText(page.request, headers, "4");
    expect(t4.locked, "D1 setup: segment 4 must be locked").toBe(true);
    expect(t4.status, "D1 setup: a locked segment is approved").toBe("approved");
  });

  test("D2: re-import target for locked segment WITH force=true → SkippedLocked, segment unchanged", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const before = await getHistoryCount(page.request, headers);

    const xliff = buildXLIFF([{ id: "4", source: "S4: Submit form", target: "D2: ATTACK against lock" }]);
    // force=true ON PURPOSE — proves locks beat force (architecture spec §E).
    const r = await reimportViaAPI(page.request, ownerToken, fileId, xliff, { lang: "fr", force: true });
    expect(r.status()).toBe(200);
    const result = (await r.json()).data as ReimportResult;

    expect(result.applied).toBe(true);
    expect(result.skipped_locked,
      "D2 (CORE invariant — locks > force): locked segments are NEVER overwritten, even with " +
      "force=true. Admins must unlock first.",
    ).toBe(1);
    expect(result.updated).toBe(0);

    const t4 = await getActiveText(page.request, headers, "4");
    expect(t4.active_text,
      "D2 defensive: locked segment must keep the text it was locked with.",
    ).toBe("D1 setup: Envoyer");
    expect(t4.locked, "D2: locked flag must still be true after the rejected re-import.").toBe(true);

    expect(await getHistoryCount(page.request, headers),
      "D2: applied=true even when nothing was written — the operation happened, history reflects it.",
    ).toBe(before + 1);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase E — Empty target = no-op (R4 default)
  // ════════════════════════════════════════════════════════════════════════

  test("E: re-import with empty <target/> → UntouchedNoChange, no wipe", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const before = await getHistoryCount(page.request, headers);

    // Set up: pre-translate segment 5 so we can verify the empty target doesn't wipe it.
    const transId = translationByKey["5"];
    const seedRes = await page.request.put(`${API}/api/files/${fileId}/translations/${transId}`, {
      headers, data: { human_text: "E setup: Réinitialiser", editing_time_ms: 0 },
    });
    expect(seedRes.status()).toBe(200);

    // No force=true — the empty-target check in computeReimportDiff fires
    // BEFORE the locked/approved check. Passing force here would just be
    // noise; this phase exclusively exercises the R4 invariant.
    const xliff = buildXLIFF([{ id: "5", source: "S5: Reset password", target: "" }]);
    const r = await reimportViaAPI(page.request, ownerToken, fileId, xliff, { lang: "fr" });
    expect(r.status()).toBe(200);
    const result = (await r.json()).data as ReimportResult;

    expect(result.untouched_no_change,
      "E (R4 invariant): empty incoming <target/> = no-op. An empty <target/> never " +
      "wipes an existing translation.",
    ).toBe(1);
    expect(result.updated, "E: empty target → 0 updates").toBe(0);

    const t5 = await getActiveText(page.request, headers, "5");
    expect(t5.active_text,
      "E (CORE data-loss check): the existing translation must survive an empty-target re-import. " +
      "If null, R4 is broken — every empty-target upload silently wipes the translation.",
    ).toBe("E setup: Réinitialiser");

    expect(await getHistoryCount(page.request, headers), "E: applied=true even with all-untouched, history grows by 1").toBe(before + 1);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase F — Renumbered IDs
  // ════════════════════════════════════════════════════════════════════════

  test("F1: renumbered IDs → 0 updates, segment is missing + extra", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const before = await getHistoryCount(page.request, headers);

    // Source matches DB segment 6's source exactly, but ID is renamed.
    const xliff = buildXLIFF([{ id: "renamed-by-external-tool", source: "S6: Confirm action please", target: "F1: Confirmer" }]);
    const r = await reimportViaAPI(page.request, ownerToken, fileId, xliff, { lang: "fr" });
    expect(r.status()).toBe(200);
    const result = (await r.json()).data as ReimportResult;

    expect(result.updated, "F1: segments match by ID only; a matching source does NOT update.").toBe(0);
    expect(result.extra_in_upload, "F1: incoming with unknown key counts as extra").toBe(1);
    // Note: DB has 7 segments; only 1 was in the upload (with the renamed key, not matching).
    expect(result.missing_from_upload).toBe(7);

    const t6 = await getActiveText(page.request, headers, "6");
    expect(t6.active_text, "F1: segment 6 must be untouched.").toBeNull();

    expect(await getHistoryCount(page.request, headers)).toBe(before + 1);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase G — Optimistic file-revision lock
  // ════════════════════════════════════════════════════════════════════════

  test("G1: stale ?revision= → 409 with current_revision in body, no history row", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const before = await getHistoryCount(page.request, headers);

    const stale = "2020-01-01T00:00:00.000000Z";
    const xliff = buildXLIFF([{ id: "7", source: "S7: Welcome back", target: "G1: stale" }]);
    const r = await reimportViaAPI(page.request, ownerToken, fileId, xliff, { lang: "fr", revision: stale });
    expect(r.status(),
      "G1 (concurrency invariant): stale revision must 409. If 200, the optimistic-lock check is " +
      "skipped — concurrent admin edits could clobber each other's view of the file.",
    ).toBe(409);

    const body = await r.json();
    expect(body.current_revision, "G1: 409 body must include current_revision so the client can refresh").toBeTruthy();

    // No history row for a 409 — request rejected before tx commit.
    expect(await getHistoryCount(page.request, headers),
      "G1: rejected request must NOT add a history row.",
    ).toBe(before);

    // Defensive: segment 7 still untouched.
    const t7 = await getActiveText(page.request, headers, "7");
    expect(t7.active_text, "G1: rejected re-import must NOT have written segment 7.").toBeNull();
  });

  test("G2: matching ?revision= → 200, applies normally", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const xliff = buildXLIFF([{ id: "7", source: "S7: Welcome back", target: "G2: Bon retour" }]);
    const r = await reimportViaAPI(page.request, ownerToken, fileId, xliff, { lang: "fr", revision: initialRevision });
    expect(r.status(),
      "G2: matching revision must succeed. files.updated_at hasn't bumped from re-imports " +
      "(translations are a separate table) so the original revision is still valid.",
    ).toBe(200);

    const result = (await r.json()).data as ReimportResult;
    expect(result.applied).toBe(true);
    expect(result.updated).toBe(1);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase H — Hard cap (50,000 segments)
  // ════════════════════════════════════════════════════════════════════════

  test("H: > 50,000-segment XLIFF → 413 Payload Too Large + X-Reimport-Max-Segments header", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const before = await getHistoryCount(page.request, headers);

    // Build 50001 trans-units (one over the cap). String concatenation is fast
    // enough for one-shot test setup.
    const sb: string[] = [];
    sb.push(`<?xml version="1.0" encoding="utf-8"?>\n<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">\n  <file source-language="en" target-language="fr"><body>`);
    for (let i = 0; i <= 50000; i++) {
      sb.push(`<trans-unit id="big-${i}"><source>SBig${i}</source><target>TBig${i}</target></trans-unit>`);
    }
    sb.push(`</body></file></xliff>`);
    const xliff = sb.join("");

    const r = await reimportViaAPI(page.request, ownerToken, fileId, xliff, { lang: "fr" });
    expect(r.status(),
      "H (operational guard): re-imports above the cap must 413 — sync apply at 50k+ segments " +
      "would exceed HTTP timeouts and lock the translations table for too long.",
    ).toBe(413);

    expect(r.headers()["x-reimport-max-segments"],
      "H: 413 response must include the X-Reimport-Max-Segments header so the client knows the cap.",
    ).toBe("50000");

    // The user-facing error message must mention the workaround ("Split the
    // file"). Defensive — catches a regression where the 413 fires with a
    // generic message that doesn't tell users what to do.
    const body = await r.text();
    expect(body,
      "H: 413 body must tell the user how to recover (split the file). If the message changes " +
      "to a generic 'too large', users have no path forward until async ships.",
    ).toContain("Split the file");

    expect(await getHistoryCount(page.request, headers),
      "H: rejected request must NOT add a history row.",
    ).toBe(before);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase I — Cross-tenant 404 (compound defense)
  // ════════════════════════════════════════════════════════════════════════

  test("I1: tenant-2 owner CANNOT POST re-import on tenant-1's file (404)", async ({ page }) => {
    const before = await getHistoryCount(page.request, await bearerHeader(ownerToken));

    const xliff = buildXLIFF([{ id: "7", source: "S7: Welcome back", target: "I1: ATTACK" }]);
    const r = await reimportViaAPI(page.request, tenant2Token, fileId, xliff, { lang: "fr" });
    expect(r.status(),
      "I1 (CORE security invariant): tenant-2 token + tenant-1 fileId must 404. " +
      "Compound defense — both GetFileTenant + GetProject tenant filter must hold.",
    ).toBe(404);

    // Defensive: tenant-1's segment 7 was set by G2 to "G2: Bon retour" — still that, not "I1: ATTACK".
    const t7 = await getActiveText(page.request, await bearerHeader(ownerToken), "7");
    expect(t7.active_text,
      "I1 defensive: tenant-1's segment 7 must still hold G2's value. If 'I1: ATTACK', " +
      "the 404 was returned AFTER the destructive write — worst-case bug.",
    ).toBe("G2: Bon retour");

    expect(await getHistoryCount(page.request, await bearerHeader(ownerToken)),
      "I1: rejected cross-tenant attempt must NOT add a history row to tenant 1's file.",
    ).toBe(before);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase J — Role gate (a linguist needs the task)
  // ════════════════════════════════════════════════════════════════════════

  test("J1: unassigned linguist CANNOT import (403) — segment unchanged + no history row", async ({ page }) => {
    const ownerHeaders = await bearerHeader(ownerToken);
    const before = await getHistoryCount(page.request, ownerHeaders);

    const xliff = buildXLIFF([{ id: "7", source: "S7: Welcome back", target: "J1: linguist-attack" }]);
    const r = await reimportViaAPI(page.request, memberToken, fileId, xliff, { lang: "fr" });
    expect(r.status(),
      "J1 (RBAC invariant): a linguist not assigned to the task must get 403 on import.",
    ).toBe(403);

    // Defensive (mirrors I1): assert the target row was NOT mutated. Catches
    // "403 returned AFTER write" — a regression where the gate is checked
    // post-handler instead of pre-handler.
    const t7 = await getActiveText(page.request, ownerHeaders, "7");
    expect(t7.active_text,
      "J1 defensive: segment 7 must still hold G2's value. If 'J1: linguist-attack', " +
      "the 403 was returned AFTER the destructive write — worst-case bug.",
    ).toBe("G2: Bon retour");

    expect(await getHistoryCount(page.request, ownerHeaders),
      "J1: rejected linguist attempt must NOT add a history row.",
    ).toBe(before);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase K — Audit trail: file.reimported project events
  // ════════════════════════════════════════════════════════════════════════

  test("K: re-import events exactly match the applied runs from this journey", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const events = await getReimportEvents(page.request, headers);

    // Applied runs from this journey (events are newest-first):
    //   B2  — applied
    //   C3  — applied with force
    //   D2  — applied with force (locks > force)
    //   E   — applied (empty target → untouched)
    //   F1  — applied (renumbered ID, nothing matched)
    //   G2  — applied (matching revision)
    // Total: 6. NOTHING from B1 (dry-run), C2 (force_required+!force),
    // G1 (409), H (413), I (404), or J1 (403) may be logged.
    expect(events.length,
      "K (CORE audit invariant): EXACTLY 6 file.reimported events for this journey's applied runs. " +
      "If <6, an applied run was lost. If >6, a non-event (dry-run / force-required-without-force / " +
      "rejected attempt) leaked into the project history.",
    ).toBe(6);

    const expectedNewestFirst = [
      { phase: "G2", force: false, updated: 1 },
      { phase: "F1", force: false, updated: 0 },
      { phase: "E",  force: false, updated: 0 },
      { phase: "D2", force: true,  updated: 0 },
      { phase: "C3", force: true,  updated: 1 },
      { phase: "B2", force: false, updated: 2 },
    ];
    events.forEach((ev, i) => {
      const expected = expectedNewestFirst[i];
      expect(ev.details.target_language, `K[${i}] (${expected.phase}): target_language`).toBe("fr");
      expect(ev.details.force, `K[${i}] (${expected.phase}): force flag accuracy`).toBe(expected.force);
      expect(ev.details.updated, `K[${i}] (${expected.phase}): updated count`).toBe(expected.updated);
      expect(ev.actor?.email, `K[${i}] (${expected.phase}): the actor is the owner (email captured in A1)`).toBe(ownerEmail);
    });
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase L — Cleanup
  // ════════════════════════════════════════════════════════════════════════

  test("L: cleanup project + member + tenant 2", async ({ page }) => {
    const cleanup = async (fn: () => Promise<void>) => { try { await fn(); } catch {} };

    if (projectId) await cleanup(() => deleteProjectViaAPI(page, projectId));

    if (memberUserId && ownerToken) {
      await cleanup(async () => {
        await page.request.delete(`${API}/api/members/${memberUserId}`, {
          headers: await bearerHeader(ownerToken),
        });
      });
    }

    if (tenant2Token) {
      await cleanup(async () => {
        await page.request.delete(`${API}/api/tenants/current`, {
          headers: await bearerHeader(tenant2Token),
        });
      });
    }
  });

  test.afterAll(async ({ browser }) => {
    // Safety net: if the serial chain stopped early, still clean up.
    const page = await browser.newPage();
    try {
      await login(page);
      if (projectId) await deleteProjectViaAPI(page, projectId).catch(() => {});
      if (memberUserId && ownerToken) {
        await page.request.delete(`${API}/api/members/${memberUserId}`, {
          headers: await bearerHeader(ownerToken),
        }).catch(() => {});
      }
      if (tenant2Token) {
        await page.request.delete(`${API}/api/tenants/current`, {
          headers: await bearerHeader(tenant2Token),
        }).catch(() => {});
      }
    } catch { /* nothing more we can do */ } finally {
      await page.close();
    }
  });
});
