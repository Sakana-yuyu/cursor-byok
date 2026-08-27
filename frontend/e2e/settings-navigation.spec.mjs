import { test, expect } from "@playwright/test";

test("深链进入设置分类时渲染对应内容并高亮所属分组", async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem(
      "cursor-byok.browser-preview.test-plan",
      JSON.stringify({}),
    );
    localStorage.removeItem("cursor-byok.browser-preview.config");
  });
  await page.goto("/settings?category=advanced");

  await expect(page.getByRole("heading", { name: "高级", exact: true })).toBeVisible();

  // 分组折叠顶栏：所属分组高亮并带当前项标记
  const moreGroup = page.getByRole("button", { name: /更多设置/ });
  await expect(moreGroup).toHaveAttribute("aria-expanded", "false");
  await expect(moreGroup).toHaveClass(/bg-\[var\(--active-bg\)\]/);
});

test("分组菜单悬浮/点击展开且选中状态一致", async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem(
      "cursor-byok.browser-preview.test-plan",
      JSON.stringify({}),
    );
    localStorage.removeItem("cursor-byok.browser-preview.config");
    localStorage.removeItem("cursor-byok.settings.category");
  });
  await page.goto("/settings");

  const generalButton = page.getByRole("button", { name: "通用", exact: true });
  await expect(generalButton).toHaveAttribute("aria-current", "page");
  await expect(generalButton).toHaveClass(/bg-\[var\(--active-bg\)\]/);

  // 展开「更多设置」分组菜单并选择 Skills 与 MCP
  const moreGroup = page.getByRole("button", { name: /更多设置/ });
  await moreGroup.click();
  const skillsButton = page.getByRole("menuitem", { name: /Skills 与 MCP/ });
  await expect(skillsButton).toBeVisible();
  await skillsButton.click();
  await expect(page.getByRole("heading", { name: "Skills 与 MCP", exact: true })).toBeVisible();
  await expect(moreGroup).toHaveClass(/bg-\[var\(--active-bg\)\]/);
});

test("分类选择在菜单、内容、URL、存储和浏览器历史间保持同步", async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem(
      "cursor-byok.browser-preview.test-plan",
      JSON.stringify({}),
    );
    localStorage.removeItem("cursor-byok.browser-preview.config");
    localStorage.removeItem("cursor-byok.settings.category");
  });
  await page.goto("/settings?category=skills-mcp");

  const moreGroup = page.getByRole("button", { name: /更多设置/ });
  await expect(moreGroup).toHaveClass(/bg-\[var\(--active-bg\)\]/);

  await moreGroup.click();
  const promptsButton = page.getByRole("menuitem", { name: /提示词/ });
  await promptsButton.click();
  await expect(page).toHaveURL(/\/settings\?category=prompts$/);
  await expect(page.getByRole("heading", { name: "提示词", exact: true })).toBeVisible();
  await expect(moreGroup).toHaveClass(/bg-\[var\(--active-bg\)\]/);
  await expect.poll(() => page.evaluate(() => localStorage.getItem("cursor-byok.settings.category"))).toBe("prompts");

  await page.goBack();
  await expect(page).toHaveURL(/\/settings\?category=skills-mcp$/);
  await expect(page.getByRole("heading", { name: "Skills 与 MCP", exact: true, level: 1 })).toBeVisible();
  await expect(moreGroup).toHaveClass(/bg-\[var\(--active-bg\)\]/);
  await expect.poll(() => page.evaluate(() => localStorage.getItem("cursor-byok.settings.category"))).toBe("skills-mcp");
});

test("设置页经侧边栏回首页，不受进入设置前的历史页面影响", async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem(
      "cursor-byok.browser-preview.test-plan",
      JSON.stringify({}),
    );
    localStorage.removeItem("cursor-byok.browser-preview.config");
  });
  await page.goto("/model-config");
  await page.getByRole("button", { name: "设置" }).first().click();
  await expect(page).toHaveURL(/\/settings$/);

  await page.getByRole("button", { name: "首页" }).click();

  await expect(page).toHaveURL(/\/$/);
});
