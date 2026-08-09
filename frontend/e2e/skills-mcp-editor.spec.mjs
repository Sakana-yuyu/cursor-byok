import { expect, test } from "@playwright/test";

test("技能 Markdown 编辑器按需加载后仍可打开", async ({ page }) => {
  const pageErrors = [];
  const editorRequests = [];
  page.on("pageerror", (error) => pageErrors.push(error.stack || error.message));
  page.on("request", (request) => {
    const url = request.url();
    if (url.includes("MarkdownEditorModal") || url.includes("md-editor-v3") || url.includes("codemirror")) {
      editorRequests.push(url);
    }
  });

  await page.goto("/settings?category=skills-mcp");

  const editButton = page.getByRole("button", { name: /^编辑技能 / }).first();
  await expect(editButton).toBeVisible();
  expect(editorRequests).toEqual([]);
  await editButton.click();

  await expect(page.getByRole("heading", { name: /^编辑 /, level: 3 })).toBeVisible();
  await expect(page.locator(".md-editor")).toBeVisible();
  await expect(page.getByRole("button", { name: "保存到文件" })).toBeVisible();
  expect(editorRequests.length).toBeGreaterThan(0);
  expect(pageErrors).toEqual([]);
});
