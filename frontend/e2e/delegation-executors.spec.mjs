import { expect, test } from "@playwright/test";
import { basePreviewConfig, openDelegationSettingsPage } from "./helpers.mjs";

const CALLS_KEY = "cursor-byok.browser-preview.calls";

const executors = [
  { id: "claude-code", displayName: "Claude Code", installURL: "https://code.claude.com/docs/en/quickstart", enabled: true, priority: 10, state: "ready", installed: true, authState: "ready", version: "2.1.226", capabilities: ["read_workspace", "write_workspace"] },
  { id: "codex-cli", displayName: "Codex CLI", installURL: "https://developers.openai.com/codex/cli/", enabled: true, priority: 20, state: "ready", installed: true, authState: "ready", version: "0.147.0", capabilities: ["read_workspace", "write_workspace"] },
  { id: "gemini-cli", displayName: "Gemini CLI", installURL: "https://github.com/google-gemini/gemini-cli", enabled: false, priority: 30, state: "not_installed", installed: false, authState: "unknown", diagnosticCode: "not_installed", diagnosticText: "未检测到 Gemini CLI" },
  { id: "kiro-cli", displayName: "Kiro CLI", installURL: "https://cli.kiro.dev/install", enabled: false, priority: 35, state: "incompatible", installed: true, authState: "unknown", diagnosticCode: "kiro_incompatible", diagnosticText: "当前 Kiro CLI 未提供 Headless 参数" },
  { id: "grok-cli", displayName: "Grok CLI", enabled: false, priority: 40, state: "action_required", installed: false, authState: "required", diagnosticCode: "custom_not_configured", diagnosticText: "请配置可执行文件" },
  { id: "cursor-agent", displayName: "Cursor Agent", installURL: "https://www.cursor.com/downloads", enabled: true, priority: 50, state: "not_installed", installed: false, editorAvailable: false, agentExecutionAvailable: false, authState: "unknown", diagnosticCode: "cursor_editor_not_found", diagnosticText: "未检测到 Cursor 编辑器" },
];

const config = basePreviewConfig({
  delegation: {
    enabled: true, maxConcurrency: 4, groups: [],
    executorFailoverLimit: 3,
    executors: [
      ...executors.map((item) => ({ id: item.id, kind: item.id === "grok-cli" ? "custom" : "builtin", displayName: item.displayName, enabled: item.enabled, priority: item.priority })),
      { id: "backup-cli", kind: "custom", displayName: "备用 CLI", enabled: false, priority: 60 },
    ],
    supervision: { enabled: false }, visionDelegation: { enabled: false, visionModelID: "", mode: "auto" },
  },
});

test.beforeEach(async ({ page }) => {
  await openDelegationSettingsPage(page, {
    config,
    plan: {
      recordCalls: true,
      delegationExecutors: executors,
      delegationTasks: [{
        id: "failover-task", description: "审查工作区", modelName: "Demo GPT", executionMode: "auto", status: "running", cancelable: true,
        queuedAtUnixMs: Date.now() - 8000, startedAtUnixMs: Date.now() - 7000, durationMs: 7000, toolCallCount: 1, executorId: "codex-cli",
        attempts: [
          { executorId: "claude-code", attempt: 1, status: "failed", failureClass: "switchable", retrySafe: true, diagnosticCode: "rate_limited", error: "请求受限", startedAtUnixMs: Date.now() - 7000, finishedAtUnixMs: Date.now() - 6000 },
          { executorId: "codex-cli", attempt: 2, status: "running", startedAtUnixMs: Date.now() - 6000 },
        ],
      }],
    },
  });
});

test("紧凑展示执行器健康、版本与 Cursor 编辑器限定状态", async ({ page }) => {
  const section = page.getByRole("heading", { name: "Agent 执行器" }).locator("xpath=ancestor::section");
  await expect(section).toBeVisible();
  for (const name of ["Claude Code", "Codex CLI", "Gemini CLI", "Kiro CLI", "Grok CLI", "Cursor Agent"]) {
    await expect(section.getByText(name, { exact: true })).toBeVisible();
  }
  await expect(section.getByText("2.1.226", { exact: true })).toBeVisible();
  await expect(section.getByText("未安装", { exact: true })).toHaveCount(2);
  await expect(section.getByText("不兼容", { exact: true })).toBeVisible();
  await expect(section.getByText("未配置", { exact: true })).toBeVisible();
  await expect(section.getByText("仅编辑器", { exact: true })).toHaveCount(0);
  const installLinks = section.getByRole("link", { name: "官方下载" });
  await expect(installLinks).toHaveCount(2);
  await expect(installLinks.nth(0)).toHaveAttribute("href", "https://github.com/google-gemini/gemini-cli");
  await expect(installLinks.nth(1)).toHaveAttribute("href", "https://www.cursor.com/downloads");
  await expect(section.getByText("备用 CLI", { exact: true })).toBeVisible();
  await expect(section.getByText("未检查", { exact: true })).toBeVisible();
  await expect(section.getByRole("button", { name: "配置 备用 CLI" })).toBeVisible();
});

test("刷新探测、启用开关和优先级都走真实保存入口", async ({ page }) => {
  await page.getByRole("button", { name: "刷新执行器状态" }).click();
  await page.getByRole("switch", { name: "启用 Gemini CLI" }).click();
  const priority = page.getByRole("spinbutton", { name: "Codex CLI 优先级" });
  await priority.fill("5");
  await priority.blur();
  await expect.poll(() => page.evaluate((key) => JSON.parse(localStorage.getItem(key) || "[]").map((item) => item.name), CALLS_KEY)).toContain("RefreshDelegationExecutorProbes");
  await expect.poll(() => page.evaluate(() => JSON.parse(localStorage.getItem("cursor-byok.browser-preview.config") || "{}")?.delegation?.executors?.find((item) => item.id === "codex-cli")?.priority)).toBe(5);
  await expect.poll(() => page.evaluate(() => JSON.parse(localStorage.getItem("cursor-byok.browser-preview.config") || "{}")?.delegation?.executors?.find((item) => item.id === "gemini-cli")?.enabled)).toBe(true);
});

test("自定义执行器配置阻止空可执行文件", async ({ page }) => {
  await page.getByRole("button", { name: "配置 Grok CLI" }).click();
  await expect(page.getByRole("dialog", { name: "配置 Grok CLI" })).toBeVisible();
  await page.getByLabel("可执行文件").fill("");
  await page.getByRole("button", { name: "保存配置" }).click();
  await expect(page.getByText("请输入可执行文件")).toBeVisible();
});

test("运行态展示故障转移时间线并可取消任务", async ({ page }) => {
  await page.getByRole("button", { name: /运行时状态/ }).click();
  await expect(page.getByText("Claude Code → Codex CLI", { exact: true })).toBeVisible();
  await expect(page.getByText("请求受限", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "取消任务" }).click();
  await expect.poll(() => page.evaluate((key) => JSON.parse(localStorage.getItem(key) || "[]").some((item) => item.name === "CancelDelegationTask"), CALLS_KEY)).toBe(true);
});

test("桌面与窄屏布局无横向溢出或控件裁剪", async ({ page }) => {
  for (const viewport of [
    { name: "desktop", width: 1440, height: 1000 },
    { name: "narrow", width: 390, height: 844 },
  ]) {
    await page.setViewportSize({ width: viewport.width, height: viewport.height });
    const section = page.getByRole("heading", { name: "Agent 执行器" }).locator("xpath=ancestor::section");
    await expect(section).toBeVisible();
    const layout = await page.evaluate(() => {
      const root = document.documentElement;
      const heading = [...document.querySelectorAll("h2")].find((item) => item.textContent?.trim() === "Agent 执行器");
      const bounds = heading?.closest("section")?.getBoundingClientRect();
      return {
        clientWidth: root.clientWidth,
        scrollWidth: root.scrollWidth,
        sectionLeft: bounds?.left ?? -1,
        sectionRight: bounds?.right ?? -1,
      };
    });
    expect(layout.scrollWidth).toBeLessThanOrEqual(layout.clientWidth);
    expect(layout.sectionLeft).toBeGreaterThanOrEqual(0);
    expect(layout.sectionRight).toBeLessThanOrEqual(layout.clientWidth);
    await page.screenshot({ path: `test-results/delegation-executors-${viewport.name}.png`, fullPage: true });
  }
});
