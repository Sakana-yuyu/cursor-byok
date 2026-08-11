import { expect, test } from "@playwright/test";

test("更新说明 Markdown 解析器仅在弹窗打开后加载", async ({ page }) => {
  const markdownParserRequests = [];
  page.on("request", (request) => {
    if (request.url().toLowerCase().includes("marked")) {
      markdownParserRequests.push(request.url());
    }
  });

  await page.goto("/");
  await expect(page.getByRole("main")).toBeVisible();
  expect(markdownParserRequests).toEqual([]);

  await page.evaluate(async () => {
    const { appState } = await import("/src/state/appState.js");
    appState.updateVersion = "84.0.0";
    appState.updateReleaseDate = "2026-08-11";
    appState.updateReleaseNotes = "## 性能优化\n\n**首屏依赖已精简**";
    appState.updatePromptKind = "ready";
    appState.updatePromptVisible = true;
  });

  const dialog = page.getByRole("dialog", { name: "发现新版本" });
  await expect(dialog.locator(".modal-md h2")).toHaveText("性能优化");
  await expect(dialog.locator(".modal-md strong")).toHaveText("首屏依赖已精简");
  expect(markdownParserRequests.length).toBeGreaterThan(0);
});
