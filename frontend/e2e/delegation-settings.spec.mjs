import { expect, test } from "@playwright/test";
import { openDelegationSettingsPage, readStoredPreviewConfig } from "./helpers.mjs";

test("委派总开关切换后持久化到本地存储", async ({ page }) => {
  await openDelegationSettingsPage(page);

  const masterSwitch = page.getByRole("switch", { name: "启用 Multitask 委派" });
  await expect(masterSwitch).toHaveAttribute("aria-checked", "true");

  await masterSwitch.click();
  await expect(masterSwitch).toHaveAttribute("aria-checked", "false");

  await expect
    .poll(async () => {
      const stored = await readStoredPreviewConfig(page);
      return stored?.delegation?.enabled;
    })
    .toBe(false);
});

test("最大并发数输入失焦后自动保存", async ({ page }) => {
  await openDelegationSettingsPage(page);

  const input = page.getByRole("spinbutton", { name: "最大并发数" });
  await input.fill("6");
  await input.blur();

  await expect
    .poll(async () => {
      const stored = await readStoredPreviewConfig(page);
      return stored?.delegation?.maxConcurrency;
    })
    .toBe(6);
});

test("委派保存失败时展示错误并提供重试恢复", async ({ page }) => {
  await openDelegationSettingsPage(page, {
    plan: { delegationSaveFailure: true },
  });

  const input = page.getByRole("spinbutton", { name: "最大并发数" });
  await input.fill("8");
  await input.blur();

  await expect(page.getByText("E2E 注入：委派配置保存失败")).toBeVisible();
  await expect(page.getByRole("button", { name: "重试" })).toBeVisible();

  // 解除失败注入后重试成功，值持久化
  await page.evaluate((key) => {
    localStorage.setItem(key, JSON.stringify({}));
  }, "cursor-byok.browser-preview.test-plan");
  await page.getByRole("button", { name: "重试" }).click();

  await expect
    .poll(async () => {
      const stored = await readStoredPreviewConfig(page);
      return stored?.delegation?.maxConcurrency;
    })
    .toBe(8);
});

test("高级委派面板：启用监督委派并持久化", async ({ page }) => {
  await openDelegationSettingsPage(page);

  // 「高级委派」折叠区默认收起，展开后显示监督策略面板
  await page.getByRole("button", { name: /高级委派/ }).click();

  const supervisionSwitch = page.getByRole("switch", { name: "启用监督委派" });
  await expect(supervisionSwitch).toHaveAttribute("aria-checked", "false");
  await supervisionSwitch.click();
  await expect(supervisionSwitch).toHaveAttribute("aria-checked", "true");

  await expect
    .poll(async () => {
      const stored = await readStoredPreviewConfig(page);
      return stored?.delegation?.supervision?.enabled;
    })
    .toBe(true);
});