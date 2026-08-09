import { test, expect } from "@playwright/test";

test("深链进入更多设置分类时自动展开并渲染内容", async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem(
      "cursor-byok.browser-preview.test-plan",
      JSON.stringify({}),
    );
    localStorage.removeItem("cursor-byok.browser-preview.config");
  });
  await page.goto("/settings?category=advanced");

  await expect(page.getByRole("heading", { name: "高级", exact: true })).toBeVisible();

  // 更多设置分组应已展开并高亮当前项
  const moreToggle = page.getByRole("button", { name: /更多设置/ });
  await expect(moreToggle).toHaveAttribute("aria-expanded", "true");
  await expect(page.getByRole("button", { name: /高级 高风险或低频系统设置/ })).toHaveAttribute(
    "aria-current",
    "page",
  );
});

test("更多设置默认展开且所有分类使用一致的选中状态", async ({ page }) => {
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
  await expect(moreToggle).toHaveAttribute("aria-expanded", "true");

  const generalButton = page.getByRole("button", { name: /通用 工作区基础与常用偏好/ });
  await expect(generalButton).toHaveAttribute("aria-current", "page");
  await expect(generalButton).toHaveClass(/before:bg-\[#10AD5D\]/);

  const skillsButton = page.getByRole("button", { name: /Skills 与 MCP 跨工具技能和 MCP 扫描/ });
  await skillsButton.click();
  await expect(skillsButton).toHaveAttribute("aria-current", "page");
  await expect(skillsButton).toHaveClass(/before:bg-\[#10AD5D\]/);
});

test("分类选择在侧栏、内容、URL、存储和浏览器历史间保持同步", async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem(
      "cursor-byok.browser-preview.test-plan",
      JSON.stringify({}),
    );
    localStorage.removeItem("cursor-byok.browser-preview.config");
    localStorage.removeItem("cursor-byok.settings.category");
  });
  await page.goto("/settings?category=skills-mcp");

  const skillsButton = page.getByRole("button", { name: /Skills 与 MCP 跨工具技能和 MCP 扫描/ });
  const promptsButton = page.getByRole("button", { name: /提示词 提示词注入与本地化/ });
  await expect(skillsButton).toHaveAttribute("aria-current", "page");

  await promptsButton.click();
  await expect(page).toHaveURL(/\/settings\?category=prompts$/);
  await expect(page.getByRole("heading", { name: "提示词", exact: true })).toBeVisible();
  await expect(promptsButton).toHaveAttribute("aria-current", "page");
  await expect.poll(() => page.evaluate(() => localStorage.getItem("cursor-byok.settings.category"))).toBe("prompts");

  await page.goBack();
  await expect(page).toHaveURL(/\/settings\?category=skills-mcp$/);
  await expect(page.getByRole("heading", { name: "Skills 与 MCP", exact: true, level: 1 })).toBeVisible();
  await expect(skillsButton).toHaveAttribute("aria-current", "page");
  await expect.poll(() => page.evaluate(() => localStorage.getItem("cursor-byok.settings.category"))).toBe("skills-mcp");
});

test("常规设置子项间距大于更多设置子项间距", async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem(
      "cursor-byok.browser-preview.test-plan",
      JSON.stringify({}),
    );
    localStorage.removeItem("cursor-byok.browser-preview.config");
  });
  await page.goto("/settings?category=prompts");

  const serviceButton = page.getByRole("button", { name: /Cursor 与服务 本地服务与启动相关配置/ });
  const delegationButton = page.getByRole("button", { name: /模型与委派 模型分组、任务委托与视觉委派/ });
  const skillsButton = page.getByRole("button", { name: /Skills 与 MCP 跨工具技能和 MCP 扫描/ });
  const promptsButton = page.getByRole("button", { name: /提示词 提示词注入与本地化/ });

  const [serviceBox, delegationBox, skillsBox, promptsBox] = await Promise.all([
    serviceButton.boundingBox(),
    delegationButton.boundingBox(),
    skillsButton.boundingBox(),
    promptsButton.boundingBox(),
  ]);
  expect(serviceBox).not.toBeNull();
  expect(delegationBox).not.toBeNull();
  expect(skillsBox).not.toBeNull();
  expect(promptsBox).not.toBeNull();

  const commonGap = delegationBox.y - (serviceBox.y + serviceBox.height);
  const moreGap = promptsBox.y - (skillsBox.y + skillsBox.height);
  expect(commonGap).toBeGreaterThan(moreGap);
  expect(moreGap).toBeGreaterThanOrEqual(3);
});
