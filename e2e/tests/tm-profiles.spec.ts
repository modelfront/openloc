import { test, expect } from "@playwright/test";
import {
  login,
  startFromScratch,
  startFromTemplate,
  selectLanguage,
  sourceLanguageTrigger,
  selectTargetLanguages,
  getSeedTMId,
  createTMProfileViaAPI,
  createTMViaAPI,
  deleteTMViaAPI,
  emptyTMProfileViaAPI,
  deleteTMProfileViaAPI,
  deleteProjectViaAPI,
  getProjectSettingsViaAPI,
  pickComboboxValue,
  chooseTMFromScratch,
  submitProjectWizard,
} from "./helpers";

/**
 * TM profiles: a shared bundle of TMs + match scoring that
 * projects follow live. Covers the TM-page section, the profile page, linking
 * from project settings (rows + scoring read-only), detach (copy back), and
 * the delete guard while a project still follows the profile.
 *
 * Every entity created here is removed in `finally`. Nothing touches the
 * seeded Demo Project's own TM rows: the test creates its own project.
 */
test.describe("TM profiles", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("TM page lists profiles; the profile page edits scoring + TMs", async ({ page }) => {
    test.setTimeout(30_000);
    const name = `E2E profile ${Date.now()}`;
    let profileId: number | null = null;
    try {
      // Create from the TM page.
      await page.goto("/memories");
      await page.getByRole("navigation", { name: "Section" }).getByRole("link", { name: "TM profiles" }).click();
      await expect(page).toHaveURL(/\/memories\/profiles$/);
      await expect(page.getByTestId("tm-profiles-section")).toBeVisible();
      await page.getByTestId("new-tm-profile").click();
      await page.getByTestId("tm-profile-name").fill(name);
      const [createResp] = await Promise.all([
        page.waitForResponse(r => r.url().includes("/api/tm-profiles") && r.request().method() === "POST"),
        page.getByTestId("tm-profile-submit").click(),
      ]);
      expect(createResp.ok()).toBe(true);
      profileId = (await createResp.json()).data.id;

      // Lands on the profile page.
      await expect(page).toHaveURL(new RegExp(`/memories/profiles/${profileId}$`), { timeout: 5000 });
      await expect(page.getByRole("navigation", { name: /breadcrumb/i })).toContainText(name);

      // Threshold saves on blur, at once.
      const threshold = page.getByLabel("Fuzzy match threshold");
      await expect(threshold).toHaveValue("75");
      await threshold.fill("60");
      const [patchResp] = await Promise.all([
        page.waitForResponse(r => r.url().includes(`/api/tm-profiles/${profileId}`) && r.request().method() === "PATCH"),
        threshold.blur(),
      ]);
      expect(patchResp.ok()).toBe(true);
      expect((await patchResp.json()).data.fuzzy_threshold).toBeCloseTo(0.6, 5);

      // A penalty saves after a pause in typing, while the field keeps focus.
      const nearExact = page.getByLabel("Near-exact penalty");
      const typedSave = page.waitForResponse(r => r.url().includes(`/api/tm-profiles/${profileId}`) && r.request().method() === "PATCH");
      await nearExact.fill("4");
      const typedResp = await typedSave;
      expect(typedResp.ok()).toBe(true);
      expect((await typedResp.json()).data.near_exact_penalty_pct).toBe(4);
      await expect(nearExact).toBeFocused();

      // The TM lookup order only matters with two TMs: hidden with none.
      await expect(page.getByTestId("tm-lookup-order-field")).toHaveCount(0);

      // Add the seed TM through the picker.
      const tmId = await getSeedTMId(page);
      await expect(page.getByTestId("tm-assignments")).toBeVisible();
      // The picker also offers inline TM creation, as in project settings.
      await page.getByTestId("tm-assignments").getByRole("button", { name: /add translation memory/i }).click();
      await expect(page.locator('[data-slot="popover-content"]').getByRole("option", { name: /create new tm/i })).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(page.locator('[data-slot="popover-content"]')).toHaveCount(0);
      // The add picker is a cmdk popover; open it and pick the row by text.
      await page.getByTestId("tm-assignments").getByRole("button", { name: /add translation memory/i }).click();
      const popover = page.locator('[data-slot="popover-content"]');
      await expect(popover).toHaveCount(1, { timeout: 3000 });
      const [putResp] = await Promise.all([
        page.waitForResponse(r => r.url().includes(`/api/tm-profiles/${profileId}/tms`) && r.request().method() === "PUT"),
        popover.getByRole("option", { name: /^default tm/i }).click(),
      ]);
      expect(putResp.ok()).toBe(true);
      await expect(page.getByTestId(`tm-assignment-row-${tmId}`)).toBeVisible();
      await expect(page.getByTestId("tm-lookup-order-field")).toHaveCount(0);

      // Back on the profiles tab the row shows 1 TM and the new threshold.
      await page.goto("/memories/profiles");
      const row = page.getByTestId(`tm-profile-row-${profileId}`);
      await expect(row).toContainText("1 TM");
      await expect(row).toContainText("60% threshold");
      await expect(page.getByTestId(`tm-profile-usage-${profileId}`)).toHaveText("not in use");
    } finally {
      if (profileId) await deleteTMProfileViaAPI(page, profileId);
    }
  });

  test("project follows a profile (read-only rows + scoring), then detaches with a copy", async ({ page }) => {
    test.setTimeout(45_000);
    const stamp = Date.now();
    const tmId = await getSeedTMId(page);
    let profileId: number | null = null;
    let savedProfileId: number | null = null;
    let projectId: number | null = null;
    let otherProjectId: number | null = null;
    const token = await page.evaluate(() => localStorage.getItem("token"));
    const headers = { Authorization: `Bearer ${token}` };
    try {
      profileId = await createTMProfileViaAPI(page, `E2E linked ${stamp}`, { tmIds: [tmId], fuzzyThreshold: 0.55 });
      const created = await page.request.post("/api/projects", {
        headers,
        data: { name: `E2E profile project ${stamp}`, source_language: "en", target_languages: ["es"] },
      });
      expect(created.ok()).toBe(true);
      projectId = (await created.json()).data.id;
      // A second project follows the profile, so for THIS project it is shared
      // and therefore read-only (a solo follower could edit it in place).
      const other = await page.request.post("/api/projects", {
        headers,
        data: { name: `E2E other follower ${stamp}`, source_language: "en", target_languages: ["es"], settings: { tm_profile_id: profileId } },
      });
      expect(other.ok()).toBe(true);
      otherProjectId = (await other.json()).data.id;

      await page.goto(`/projects/${projectId}/files`);
      await page.getByRole("link", { name: /settings/i }).click();
      await expect(page.getByTestId("tm-profile-select")).toBeVisible({ timeout: 5000 });
      // Own rows are editable before linking.
      await expect(page.getByTestId("tm-scoring")).not.toHaveAttribute("data-readonly", "true");

      // Link.
      const [linkResp] = await Promise.all([
        page.waitForResponse(r => r.url().includes(`/api/projects/${projectId}/settings`) && r.request().method() === "PATCH"),
        pickComboboxValue(page, "tm-profile-select", String(profileId)),
      ]);
      expect(linkResp.ok()).toBe(true);
      // A followed profile's settings start collapsed; expand to inspect them.
      await page.getByTestId("tm-profile-settings-toggle").click();
      await expect(page.getByTestId("tm-profile-settings-toggle")).toHaveAttribute("aria-expanded", "true");

      // Rows + scoring are the profile's and read-only.
      await expect(page.getByTestId("tm-profile-linked")).toContainText(`E2E linked ${stamp}`);
      await expect(page.getByTestId("tm-scoring")).toHaveAttribute("data-readonly", "true", { timeout: 5000 });
      await expect(page.getByTestId("tm-assignments")).toHaveAttribute("data-readonly", "true");
      await expect(page.getByTestId(`tm-assignment-row-${tmId}`)).toBeVisible();
      await expect(page.getByLabel("Fuzzy match threshold")).toHaveValue("55");
      await expect(page.getByLabel("Fuzzy match threshold")).toBeDisabled();
      await expect(page.getByTestId(`tm-assignment-remove-${tmId}`)).toHaveCount(0);

      // The server agrees.
      const linked = await getProjectSettingsViaAPI(page, projectId!);
      expect(linked.data.tm_profile?.id).toBe(profileId);
      expect(linked.data.tm_assignments.map((a: { tm_id: number }) => a.tm_id)).toEqual([tmId]);

      // Deleting a followed profile is refused.
      const del = await page.request.delete(`/api/tm-profiles/${profileId}`, { headers });
      expect(del.status()).toBe(409);
      expect((await del.json()).error.code).toBe("tm_profile_in_use");

      // Detach: confirm → copy → editable again with the copied row.
      await page.getByTestId("tm-profile-detach").click();
      const [detachResp] = await Promise.all([
        page.waitForResponse(r => r.url().includes(`/api/projects/${projectId}/tms/detach`) && r.request().method() === "POST"),
        page.getByRole("button", { name: /detach and customise/i }).last().click(),
      ]);
      expect(detachResp.ok()).toBe(true);
      await expect(page.getByTestId("tm-profile-linked")).toHaveCount(0, { timeout: 5000 });
      await expect(page.getByTestId("tm-scoring")).not.toHaveAttribute("data-readonly", "true");
      await expect(page.getByTestId(`tm-assignment-row-${tmId}`)).toBeVisible();
      await expect(page.getByTestId(`tm-assignment-remove-${tmId}`)).toBeVisible();
      await expect(page.getByLabel("Fuzzy match threshold")).toHaveValue("55");
      await expect(page.getByLabel("Fuzzy match threshold")).toBeEnabled();

      const detached = await getProjectSettingsViaAPI(page, projectId!);
      expect(detached.data.tm_profile).toBeNull();
      expect(detached.data.project.fuzzy_threshold).toBeCloseTo(0.55, 5);
      expect(detached.data.tm_assignments.map((a: { tm_id: number }) => a.tm_id)).toEqual([tmId]);

      // Save the (now custom) setup as a new profile from project settings and
      // follow it — the visible button, not a dropdown row.
      await page.getByTestId("tm-profile-save-as").click();
      await page.getByTestId("tm-profile-name").fill(`E2E saved ${stamp}`);
      await expect(page.getByTestId("tm-profile-link")).toBeChecked();
      const [saveResp] = await Promise.all([
        page.waitForResponse(r => r.url().includes(`/api/projects/${projectId}/save-tm-profile`) && r.request().method() === "POST"),
        page.getByTestId("tm-profile-submit").click(),
      ]);
      expect(saveResp.ok()).toBe(true);
      savedProfileId = (await saveResp.json()).data.tm_profile.id;
      // Followed and, being its only follower, editable in place: the dropdown
      // names the profile and the note is the profile-name link.
      await expect(page.getByTestId("tm-profile-select")).toContainText(`E2E saved ${stamp}`, { timeout: 5000 });
      await expect(page.getByTestId("tm-profile-editable")).toBeVisible();
      // Saving the draft as a profile opens its settings (the user was editing them).
      await expect(page.getByTestId("tm-profile-settings-toggle")).toHaveAttribute("aria-expanded", "true");
      const saved = await getProjectSettingsViaAPI(page, projectId!);
      expect(saved.data.tm_profile?.id).toBe(savedProfileId);
      expect(saved.data.project.fuzzy_threshold).toBeCloseTo(0.55, 5);
    } finally {
      if (projectId) await deleteProjectViaAPI(page, projectId);
      if (otherProjectId) await deleteProjectViaAPI(page, otherProjectId);
      if (profileId) await deleteTMProfileViaAPI(page, profileId);
      if (savedProfileId) await deleteTMProfileViaAPI(page, savedProfileId);
    }
  });

  test("New Project wizard can follow a profile", async ({ page }) => {
    test.setTimeout(30_000);
    const stamp = Date.now();
    const tmId = await getSeedTMId(page);
    let profileId: number | null = null;
    let projectId: number | null = null;
    try {
      profileId = await createTMProfileViaAPI(page, `E2E wizard ${stamp}`, { tmIds: [tmId] });
      await page.goto("/projects/new");
      await startFromScratch(page);
      await page.getByPlaceholder(/project name/i).fill(`E2E wizard project ${stamp}`);
      await selectLanguage(page, sourceLanguageTrigger(page), "en");
      await selectTargetLanguages(page, page.getByRole("button", { name: /add target languages/i }), ["es"]);
      await pickComboboxValue(page, "tm-profile-select", String(profileId));
      await page.getByTestId("tm-profile-settings-toggle").click();
      // The profile's rows show before the project exists; nothing follows the
      // profile yet, so they are editable in place rather than read-only.
      await expect(page.getByTestId(`tm-assignment-row-${tmId}`)).toBeVisible({ timeout: 5000 });
      await expect(page.getByTestId("tm-profile-editable")).toBeVisible();
      await expect(page.getByTestId("tm-assignments")).not.toHaveAttribute("data-readonly", "true");

      const { response: createResp, id: createdId } = await submitProjectWizard(page);
      expect(createResp.ok()).toBe(true);
      projectId = createdId;
      const settings = await getProjectSettingsViaAPI(page, projectId!);
      expect(settings.data.tm_profile?.id).toBe(profileId);
    } finally {
      if (projectId) await deleteProjectViaAPI(page, projectId);
      if (profileId) await deleteTMProfileViaAPI(page, profileId);
    }
  });

  test("New Project wizard can create a profile from its draft and follow it", async ({ page }) => {
    test.setTimeout(30_000);
    const stamp = Date.now();
    let profileId: number | null = null;
    let projectId: number | null = null;
    const tmIds: number[] = [];
    try {
      tmIds.push(await createTMViaAPI(page, `E2E wizard TM A ${stamp}`));
      tmIds.push(await createTMViaAPI(page, `E2E wizard TM B ${stamp}`));
      await page.goto("/projects/new");
      await startFromScratch(page);
      await page.getByPlaceholder(/project name/i).fill(`E2E wizard-save project ${stamp}`);
      await selectLanguage(page, sourceLanguageTrigger(page), "en");
      await selectTargetLanguages(page, page.getByRole("button", { name: /add target languages/i }), ["es"]);
      await chooseTMFromScratch(page);
      await page.getByLabel("Fuzzy match threshold").fill("62");
      for (const tmId of tmIds) await pickComboboxValue(page, "add-tm-select", String(tmId));
      await pickComboboxValue(page, "tm-lookup-order-select", "tm_order");
      await expect(page.getByTestId("tm-lookup-order-select")).toContainText("By TM order");

      await page.getByTestId("tm-profile-save-as").click();
      await page.getByTestId("tm-profile-name").fill(`E2E from wizard ${stamp}`);
      const [createResp] = await Promise.all([
        page.waitForResponse(r => r.url().endsWith("/api/tm-profiles") && r.request().method() === "POST"),
        page.getByTestId("tm-profile-submit").click(),
      ]);
      expect(createResp.ok()).toBe(true);
      const created = (await createResp.json()).data;
      profileId = created.id;
      expect(created.fuzzy_threshold).toBeCloseTo(0.62, 5);
      expect(created.tm_lookup_order).toBe("tm_order");
      await expect(page.getByTestId("tm-profile-select")).toContainText(`E2E from wizard ${stamp}`);
      await expect(page.getByTestId("tm-profile-settings-toggle")).toHaveAttribute("aria-expanded", "true");
      // Nothing else follows the new profile, so it stays editable right here and
      // the edit goes to the profile itself.
      await expect(page.getByTestId("tm-profile-editable")).toBeVisible();
      await expect(page.getByTestId("tm-scoring")).not.toHaveAttribute("data-readonly", "true");
      const threshold = page.getByLabel("Fuzzy match threshold");
      await threshold.fill("70");
      const [patchResp] = await Promise.all([
        page.waitForResponse(r => r.url().includes(`/api/tm-profiles/${profileId}`) && r.request().method() === "PATCH"),
        threshold.blur(),
      ]);
      expect(patchResp.ok()).toBe(true);
      expect((await patchResp.json()).data.fuzzy_threshold).toBeCloseTo(0.7, 5);

      const { response: projectResp, id: createdId } = await submitProjectWizard(page);
      expect(projectResp.ok()).toBe(true);
      projectId = createdId;
      const settings = await getProjectSettingsViaAPI(page, projectId!);
      expect(settings.data.tm_profile?.id).toBe(profileId);
      expect(settings.data.project.fuzzy_threshold).toBeCloseTo(0.7, 5);
    } finally {
      if (projectId) await deleteProjectViaAPI(page, projectId);
      if (profileId) {
        await emptyTMProfileViaAPI(page, profileId);
        await deleteTMProfileViaAPI(page, profileId);
      }
      for (const tmId of tmIds) await deleteTMViaAPI(page, tmId);
    }
  });

  test("New Project wizard: From scratch keeps the TM lookup order the user picks", async ({ page }) => {
    test.setTimeout(30_000);
    const stamp = Date.now();
    let projectId: number | null = null;
    const tmIds: number[] = [];
    try {
      tmIds.push(await createTMViaAPI(page, `E2E order TM A ${stamp}`));
      tmIds.push(await createTMViaAPI(page, `E2E order TM B ${stamp}`));
      await page.goto("/projects/new");
      await startFromScratch(page);
      await page.getByPlaceholder(/project name/i).fill(`E2E lookup order ${stamp}`);
      await selectLanguage(page, sourceLanguageTrigger(page), "en");
      await selectTargetLanguages(page, page.getByRole("button", { name: /add target languages/i }), ["es"]);
      await chooseTMFromScratch(page);

      const orderSelect = page.getByTestId("tm-lookup-order-select");
      const field = page.getByTestId("tm-lookup-order-field");
      // Hidden until the draft holds two TMs.
      await expect(field).toHaveCount(0);
      await pickComboboxValue(page, "add-tm-select", String(tmIds[0]));
      await expect(field).toHaveCount(0);
      await pickComboboxValue(page, "add-tm-select", String(tmIds[1]));
      await expect(orderSelect).toContainText("By match percentage");
      await pickComboboxValue(page, "tm-lookup-order-select", "tm_order");
      await expect(orderSelect).toContainText("By TM order");

      const { response, id } = await submitProjectWizard(page);
      expect(response.ok()).toBe(true);
      projectId = id;
      const settings = await getProjectSettingsViaAPI(page, projectId!);
      expect(settings.data.project.tm_lookup_order).toBe("tm_order");
    } finally {
      if (projectId) await deleteProjectViaAPI(page, projectId);
      for (const tmId of tmIds) await deleteTMViaAPI(page, tmId);
    }
  });

  test("the TM profile page saves the TM lookup order once the profile has two TMs", async ({ page }) => {
    test.setTimeout(30_000);
    const stamp = Date.now();
    let profileId: number | null = null;
    let secondTM: number | null = null;
    try {
      secondTM = await createTMViaAPI(page, `E2E profile order TM ${stamp}`);
      profileId = await createTMProfileViaAPI(page, `E2E ordered profile ${stamp}`, { tmIds: [await getSeedTMId(page), secondTM] });
      await page.goto(`/memories/profiles/${profileId}`);

      await expect(page.getByTestId("tm-lookup-order-select")).toContainText("By match percentage");
      const [orderResp] = await Promise.all([
        page.waitForResponse(r => r.url().includes(`/api/tm-profiles/${profileId}`) && r.request().method() === "PATCH"),
        pickComboboxValue(page, "tm-lookup-order-select", "tm_order"),
      ]);
      expect(orderResp.ok()).toBe(true);
      expect((await orderResp.json()).data.tm_lookup_order).toBe("tm_order");
      await page.reload();
      await expect(page.getByTestId("tm-lookup-order-select")).toContainText("By TM order");
    } finally {
      if (profileId) {
        await emptyTMProfileViaAPI(page, profileId);
        await deleteTMProfileViaAPI(page, profileId);
      }
      if (secondTM) await deleteTMViaAPI(page, secondTM);
    }
  });
});
