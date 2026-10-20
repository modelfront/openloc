import { test, expect } from "@playwright/test";
import { authHeaders, login } from "./helpers";

// The account email is shown, never edited: invitations and sign-in trust it.
test.describe("Settings profile", () => {
  test("the email is read-only, and the API refuses to change it", async ({ page }) => {
    await login(page);
    await page.goto("/settings");
    const email = page.locator('[data-guide="settings.profile"] input[type="email"]');
    await expect(email).toHaveAttribute("readonly", "");
    const current = await email.inputValue();
    expect(current, "the profile shows the account email").toContain("@");

    const headers = await authHeaders(page);
    const res = await page.request.patch("/api/auth/me", { headers, data: { email: "someone-else@test.com" } });
    expect(res.status(), await res.text()).toBe(400);
    const me = await page.request.get("/api/auth/me", { headers });
    expect((await me.json()).data.email, "a refused change must leave the email as it was").toBe(current);
  });
});
