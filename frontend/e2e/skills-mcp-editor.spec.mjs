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

async function previewCalls(page) {
  return page.evaluate((key) => JSON.parse(localStorage.getItem(key) || "[]"), PREVIEW_CALLS_STORAGE_KEY);
}

async function openMcpTab(page) {
  await page.goto("/settings?category=skills-mcp");
  await page.getByRole("tab", { name: "MCP" }).click();
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

test("未信任的工作区 MCP 取消确认时不授权也不连接，批准后按顺序授权并连接", async ({ page }) => {
  await seedWorkspaceRoot(page, { stored: "E:\\workspace" });
  await seedPreviewTestPlan(page, {
    recordCalls: true,
    mcpServers: [{
      name: "Workspace tools",
      identifier: "workspace-tools",
      transport: "stdio",
      source: "cursor",
      sourceLabel: "Cursor",
      scope: "workspace",
      runtimeScope: "workspace:e:/workspace",
      isWorkspace: true,
      trusted: false,
      trustRequired: true,
      trustFingerprint: "mcp-trust-v1:sha256:test",
      sourcePath: "E:\\workspace\\.cursor\\mcp.json",
      commandPreview: "node",
      trustArgumentCount: 2,
      cwd: "E:\\workspace",
      configuredEnabled: true,
      enabled: true,
      status: "disconnected",
      toolCount: 0,
    }],
  });

  await openMcpTab(page);
  await page.getByRole("button", { name: "连接 MCP workspace-tools" }).click();

  const dialog = page.getByRole("dialog", { name: "信任工作区 MCP 配置" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("E:\\workspace\\.cursor\\mcp.json");
  await expect(dialog).toContainText("node [2 个参数已隐藏]");
  await expect(dialog).toContainText("E:\\workspace");
  await expect(dialog).toContainText("配置发生变化后需要重新批准");
  await dialog.getByRole("button", { name: "取消" }).click();

  expect((await previewCalls(page)).filter((call) => ["GrantMCPServerTrust", "ConnectMCPServer"].includes(call.name))).toEqual([]);

  await page.getByRole("button", { name: "连接 MCP workspace-tools" }).click();
  await dialog.getByRole("button", { name: "批准并连接" }).click();
  await expect.poll(async () => (await previewCalls(page))
    .filter((call) => ["GrantMCPServerTrust", "ConnectMCPServer"].includes(call.name))
    .map((call) => call.name)).toEqual(["GrantMCPServerTrust", "ConnectMCPServer"]);
});

test("用户范围 MCP 保持一键连接且已信任工作区 MCP 可撤销授权", async ({ page }) => {
  await seedWorkspaceRoot(page, { stored: "E:\\workspace" });
  await seedPreviewTestPlan(page, {
    recordCalls: true,
    mcpServers: [
      {
        name: "User tools", identifier: "user-tools", transport: "http", scope: "user",
        runtimeScope: "user", isWorkspace: false, trusted: true, trustRequired: false,
        trustURLOrigin: "https://mcp.example.com", configuredEnabled: true, enabled: true,
        status: "disconnected", toolCount: 0,
      },
      {
        name: "Trusted workspace", identifier: "trusted-workspace", transport: "stdio", scope: "workspace",
        runtimeScope: "workspace:e:/workspace", isWorkspace: true, trusted: true, trustRequired: false,
        sourcePath: "E:\\workspace\\.cursor\\mcp.json", commandPreview: "node", trustArgumentCount: 1,
        configuredEnabled: true, enabled: true, status: "disconnected", toolCount: 0,
      },
    ],
  });

  await openMcpTab(page);
  await page.getByRole("button", { name: "连接 MCP user-tools" }).click();
  await expect.poll(async () => (await previewCalls(page))
    .filter((call) => ["GrantMCPServerTrust", "ConnectMCPServer"].includes(call.name))
    .map((call) => call.name)).toEqual(["ConnectMCPServer"]);
  await expect(page.getByRole("dialog", { name: "信任工作区 MCP 配置" })).toHaveCount(0);

  await expect(page.getByText("已信任", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "撤销 MCP trusted-workspace 信任" }).click();
  await expect.poll(async () => (await previewCalls(page)).some((call) => call.name === "RevokeMCPServerTrust")).toBe(true);
});

test("工作区 MCP 配置在快照后变化时刷新当前定义并重新请求信任", async ({ page }) => {
  await seedWorkspaceRoot(page, { stored: "E:\\workspace" });
  await seedPreviewTestPlan(page, {
    recordCalls: true,
    connectMcpTrustRequiredOnce: true,
    connectMcpTrustRequiredServer: {
      name: "Changed workspace tools",
      identifier: "workspace-tools",
      transport: "stdio",
      source: "cursor",
      sourceLabel: "Cursor",
      scope: "workspace",
      runtimeScope: "workspace:e:/workspace",
      isWorkspace: true,
      trusted: false,
      trustRequired: true,
      sourcePath: "E:\\workspace\\.cursor\\mcp.json",
      commandPreview: "python",
      trustArgumentCount: 3,
      cwd: "E:\\workspace\\tools",
      configuredEnabled: true,
      enabled: true,
      status: "disconnected",
      toolCount: 0,
    },
    mcpServers: [{
      name: "Workspace tools",
      identifier: "workspace-tools",
      transport: "stdio",
      source: "cursor",
      sourceLabel: "Cursor",
      scope: "workspace",
      runtimeScope: "workspace:e:/workspace",
      isWorkspace: true,
      trusted: true,
      trustRequired: false,
      sourcePath: "E:\\workspace\\.cursor\\mcp.json",
      commandPreview: "node",
      trustArgumentCount: 1,
      cwd: "E:\\workspace",
      configuredEnabled: true,
      enabled: true,
      status: "disconnected",
      toolCount: 0,
    }],
  });

  await openMcpTab(page);
  await page.getByRole("button", { name: "连接 MCP workspace-tools" }).click();

  const dialog = page.getByRole("dialog", { name: "信任工作区 MCP 配置" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("python [3 个参数已隐藏]");
  await expect(dialog).toContainText("E:\\workspace\\tools");
  expect((await previewCalls(page)).filter((call) => call.name === "GrantMCPServerTrust")).toEqual([]);
});
