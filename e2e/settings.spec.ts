import { test, expect } from "@playwright/test";
import {
  createTestApi,
  loginAsDefault,
  waitForPageText,
} from "./helpers";

test.describe("Settings", () => {
  test("mobile settings page can reopen sidebar and navigate away", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const api = await createTestApi();
    const workspaceId = api.getWorkspaceId();
    const workspaceSlug = api.getWorkspaceSlug();
    if (!workspaceId || !workspaceSlug) {
      throw new Error("Expected createTestApi() to ensure an E2E workspace");
    }

    // This endpoint is one-way for the shared E2E user. Keep it in this
    // regression only to prevent the starter modal from covering the mobile
    // sidebar trigger.
    await api.dismissStarterContent(workspaceId);
    await page.addInitScript((token) => {
      localStorage.setItem("multica_token", token);
    }, api.getToken());
    await page.goto(`/${workspaceSlug}/settings`);
    await page.waitForURL("**/settings");

    const sidebarTrigger = page.getByRole("button", {
      name: /toggle sidebar/i,
    });
    await expect(sidebarTrigger).toBeVisible();

    await sidebarTrigger.click();
    const sidebarSheet = page.locator('[data-slot="sidebar"][data-mobile="true"]');
    await expect(sidebarSheet).toBeVisible();

    await page
      .getByRole("dialog", { name: "Sidebar" })
      .getByRole("link", { name: "Issues", exact: true })
      .click();
    await page.waitForURL("**/issues");
    await expect(page).toHaveURL(/\/issues/);
  });

  test("updating workspace name reflects in sidebar immediately", async ({
    page,
  }) => {
    const workspaceSlug = await loginAsDefault(page);

    // Read the current workspace name from the sidebar
    const sidebarName = page.getByRole("button", { name: /E2E Workspace/ }).first();
    const originalName = (await sidebarName.innerText()).split("\n").pop()?.trim() ?? "E2E Workspace";

    await page.goto(`/${workspaceSlug}/settings?tab=workspace`, { waitUntil: "domcontentloaded" });
    await waitForPageText(page, "General");

    // Change workspace name
    const nameInput = page
      .locator('input[type="text"]')
      .first();
    await nameInput.clear();
    const newName = "Renamed WS " + Date.now();
    await nameInput.fill(newName);

    // Save
    await page.locator("button", { hasText: "Save" }).click();

    await expect(page.getByText("Workspace settings saved").first()).toBeVisible({ timeout: 5000 });

    // Sidebar should reflect the new name WITHOUT page refresh
    await expect(page.getByRole("button", { name: new RegExp(newName) }).first()).toBeVisible();

    // Restore original name so other tests aren't affected
    await nameInput.clear();
    await nameInput.fill(originalName.trim());
    await page.locator("button", { hasText: "Save" }).click();
    await expect(page.getByText("Workspace settings saved").first()).toBeVisible({ timeout: 5000 });
    await expect(page.getByRole("button", { name: new RegExp(originalName) }).first()).toBeVisible();
  });
});
