import { expect, test } from "@playwright/test";
import { basePreviewConfig, seedPreviewTestPlan } from "./helpers.mjs";

// 覆盖全部一级页面和设置页的每个设计分类。仅使用浏览器预览 mock，
// 防止路由回归检查触发真实的供应商请求或桌面端操作。
const routes = [
  "/",
  "/model-config",
  "/model-editor",
  "/model-catalog",
  "/model-groups",
  "/supplier?mode=name&groupName=%E6%B5%8F%E8%A7%88%E5%99%A8%E9%A2%84%E8%A7%88%E7%A4%BA%E4%BE%8B",
  "/metrics-detail",
  "/request-metrics",
  "/stats-overlay",
  "/diagnostics",
  "/settings?category=general",
  "/settings?category=cursor-service",
  "/settings?category=delegation",
  "/settings?category=skills-mcp",
  "/settings?category=prompts",
  "/settings?category=overlay",
  "/settings?category=history",
  "/settings?category=advanced",
  "/settings?category=about",
];

for (const route of routes) {
  test(`路由冒烟：${route}`, async ({ page }) => {
    const pageErrors = [];
    const consoleErrors = [];
    page.on("pageerror", (error) => pageErrors.push(error.message));
    page.on("console", (message) => {
      if (message.type() === "error") consoleErrors.push(message.text());
    });
    await seedPreviewTestPlan(page, {}, basePreviewConfig());

    await page.goto(route);
    await page.waitForTimeout(250);
    expect(pageErrors).toEqual([]);
    expect(consoleErrors).toEqual([]);
    await expect(page.locator("#root")).not.toBeEmpty();
    await expect
      .poll(() => page.evaluate(() => document.readyState))
      .toBe("complete");

    const layout = await page.evaluate(() => ({
      viewportWidth: window.innerWidth,
      documentWidth: document.documentElement.scrollWidth,
      bodyWidth: document.body.scrollWidth,
    }));
    expect(layout.documentWidth).toBeLessThanOrEqual(layout.viewportWidth + 1);
    expect(layout.bodyWidth).toBeLessThanOrEqual(layout.viewportWidth + 1);
    expect(pageErrors).toEqual([]);
  });
}
