import { expect, test } from "@playwright/test";
import { openDelegationSettingsPage } from "./helpers.mjs";

test("桌面设置工作区使用侧栏以外的可用宽度", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 860 });
  await openDelegationSettingsPage(page);

  const header = page.getByRole("heading", { name: "模型与委派", exact: true }).locator("..");
  await expect(header).toBeVisible();

  const box = await header.boundingBox();
  expect(box).not.toBeNull();
  expect(box.width).toBeGreaterThan(860);
});

test("窄屏设置页不产生横向滚动", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await openDelegationSettingsPage(page);

  await expect(page.getByRole("heading", { name: "模型与委派", exact: true })).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
    .toBe(true);
});
