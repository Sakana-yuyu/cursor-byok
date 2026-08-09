import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem("cursor-byok.browser-preview.test-plan", JSON.stringify({}));
    localStorage.removeItem("cursor-byok.browser-preview.config");
    if (!sessionStorage.getItem("cursor-byok.history.e2e-initialized")) {
      localStorage.removeItem("cursor-byok.history.view-mode");
      sessionStorage.setItem("cursor-byok.history.e2e-initialized", "true");
    }
  });
});

test("历史记录默认使用 Windows 文件夹图标视图并支持逐级浏览", async ({ page }) => {
  await page.setViewportSize({ width: 1033, height: 920 });
  await page.goto("/settings?category=history");

  const panel = page.getByTestId("history-panel");
  const viewport = page.getByTestId("history-list-viewport");
  const iconView = page.getByRole("list", { name: "历史图标视图" });

  await expect(panel).toBeVisible();
  await expect(iconView).toBeVisible();
  await expect(page.getByRole("button", { name: "图标视图" })).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByRole("button", { name: "2026，7 个会话" })).toBeVisible();
  await expect(page.getByRole("button", { name: "2025，1 个会话" })).toBeVisible();
  await expect(page.getByRole("columnheader", { name: "名称" })).toHaveCount(0);

  const dimensions = await Promise.all([panel.boundingBox(), viewport.boundingBox()]);
  expect(dimensions[0]?.height).toBeGreaterThan(570);
  expect(dimensions[1]?.height).toBeGreaterThan(360);

  await page.getByRole("button", { name: "2026，7 个会话" }).dblclick();
  await expect(page.getByRole("button", { name: "2026年7月，5 个会话" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "历史路径" })).toContainText("2026");

  await page.getByRole("button", { name: "2026年7月，5 个会话" }).dblclick();
  await expect(page.getByRole("button", { name: "2026年7月31日，3 个会话" })).toBeVisible();

  await page.getByRole("button", { name: "2026年7月31日，3 个会话" }).dblclick();
  await expect(page.getByText("调试视觉委派任务条轮换逻辑", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "返回上级" }).click();
  await expect(page.getByRole("button", { name: "2026年7月31日，3 个会话" })).toBeVisible();
});

test("图标和详细信息视图共享选择并记住最后使用的模式", async ({ page }) => {
  await page.goto("/settings?category=history");
  await page.getByRole("button", { name: "2026，7 个会话" }).dblclick();
  await page.getByRole("button", { name: "2026年7月，5 个会话" }).dblclick();
  await page.getByRole("button", { name: "2026年7月31日，3 个会话" }).dblclick();

  const sessionTile = page.getByRole("listitem").filter({ hasText: "调试视觉委派任务条轮换逻辑" });
  await sessionTile.click();
  await expect(sessionTile).toHaveAttribute("aria-selected", "true");

  await page.getByRole("button", { name: "详细信息视图" }).click();
  const detailsGrid = page.getByRole("grid", { name: "历史详细信息列表" });
  await expect(detailsGrid).toBeVisible();
  await expect(detailsGrid.getByRole("columnheader", { name: "名称" })).toBeVisible();
  const selectedRow = detailsGrid.getByRole("row").filter({ hasText: "调试视觉委派任务条轮换逻辑" });
  await expect(selectedRow).toHaveAttribute("aria-selected", "true");
  await expect.poll(() => page.evaluate(() => localStorage.getItem("cursor-byok.history.view-mode"))).toBe("details");

  await page.reload();
  await expect(page.getByRole("button", { name: "详细信息视图" })).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByRole("grid", { name: "历史详细信息列表" })).toBeVisible();
});

test("窄屏图标视图使用单列且页面不产生横向溢出", async ({ page }) => {
  await page.setViewportSize({ width: 520, height: 820 });
  await page.goto("/settings?category=history");

  const viewport = page.getByTestId("history-list-viewport");
  await expect(viewport).toBeVisible();
  const metrics = await page.evaluate(() => {
    const view = document.querySelector('[aria-label="历史图标视图"]');
    return {
      documentWidth: document.documentElement.scrollWidth,
      viewportWidth: document.documentElement.clientWidth,
      columns: view ? getComputedStyle(view).gridTemplateColumns.split(" ").length : 0,
    };
  });
  expect(metrics.documentWidth).toBe(metrics.viewportWidth);
  expect(metrics.columns).toBe(1);
});
