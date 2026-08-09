import { expect, test } from "@playwright/test";
import { seedPreviewTestPlan } from "./helpers.mjs";

const WORKSPACE_ROOT_STORAGE_KEY = "cursor-byok-mcp-workspace-root";
const PREVIEW_CALLS_STORAGE_KEY = "cursor-byok.browser-preview.calls";

async function seedWorkspaceRoot(page, { recent = "", stored = "" } = {}) {
  await seedPreviewTestPlan(page, { recentWorkspaceRoot: recent, recordCalls: true });
  await page.addInitScript(
    ({ key, value, callsKey }) => {
      localStorage.setItem(key, value);
      localStorage.removeItem(callsKey);
    },
    { key: WORKSPACE_ROOT_STORAGE_KEY, value: stored, callsKey: PREVIEW_CALLS_STORAGE_KEY },
  );
}

async function firstPreviewCall(page, name) {
  await expect.poll(() => page.evaluate(
    ({ callsKey, operation }) => {
      const calls = JSON.parse(localStorage.getItem(callsKey) || "[]");
      return calls.find((call) => call.name === operation) || null;
    },
    { callsKey: PREVIEW_CALLS_STORAGE_KEY, operation: name },
  )).not.toBeNull();
  return page.evaluate(
    ({ callsKey, operation }) => {
      const calls = JSON.parse(localStorage.getItem(callsKey) || "[]");
      return calls.find((call) => call.name === operation);
    },
    { callsKey: PREVIEW_CALLS_STORAGE_KEY, operation: name },
  );
}

test("Skills/MCP 扫描优先使用后端最近工作区根", async ({ page }) => {
  await seedWorkspaceRoot(page, {
    recent: "  E:\\recent-workspace  ",
    stored: "E:\\stored-workspace",
  });

  await page.goto("/settings?category=skills-mcp");

  const call = await firstPreviewCall(page, "GetSkillsMCPScanSnapshot");
  expect(call.args).toEqual(["E:\\recent-workspace"]);
  await expect.poll(() => page.evaluate((key) => localStorage.getItem(key), WORKSPACE_ROOT_STORAGE_KEY))
    .toBe("E:\\recent-workspace");
});

test("Skills/MCP 扫描在后端无工作区时使用手动保存的根", async ({ page }) => {
  await seedWorkspaceRoot(page, { stored: "  E:\\manual-workspace  " });

  await page.goto("/settings?category=skills-mcp");

  const call = await firstPreviewCall(page, "GetSkillsMCPScanSnapshot");
  expect(call.args).toEqual(["E:\\manual-workspace"]);
});

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
