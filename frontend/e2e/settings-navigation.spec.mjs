import { test, expect } from "@playwright/test";

test("深链进入更多设置分类时自动展开并渲染内容", async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem(
      "cursor-byok.browser-preview.test-plan",
      JSON.stringify({}),
    );
    localStorage.removeItem("cursor-byok.browser-preview.config");
  });
  await page.goto("/settings?category=goal");

  await expect(page.getByRole("heading", { name: "Goal", exact: true })).toBeVisible();

  // 更多设置分组应已展开并高亮当前项
  const moreToggle = page.getByRole("button", { name: /更多设置/ });
  await expect(moreToggle).toHaveAttribute("aria-expanded", "true");
  await expect(page.getByRole("button", { name: /Goal 目标循环执行的预算与完成判定/ })).toHaveAttribute(
    "aria-current",
    "page",
  );
});

test("默认进入设置页只展示常用分类，更多设置默认收起", async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem(
      "cursor-byok.browser-preview.test-plan",
      JSON.stringify({}),
    );
    localStorage.removeItem("cursor-byok.browser-preview.config");
    localStorage.removeItem("cursor-byok.settings.category");
  });
  await page.goto("/settings");

  const moreToggle = page.getByRole("button", { name: /更多设置/ });
  await expect(moreToggle).toHaveAttribute("aria-expanded", "false");

  // 展开后可见收纳分类
  await moreToggle.click();
  await expect(moreToggle).toHaveAttribute("aria-expanded", "true");
  await expect(page.getByRole("button", { name: /历史与日志/ })).toBeVisible();
});