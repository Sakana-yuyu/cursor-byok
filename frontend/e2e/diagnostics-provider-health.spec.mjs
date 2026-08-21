import { expect, test } from "@playwright/test";
import { basePreviewConfig, seedPreviewTestPlan } from "./helpers.mjs";

const CALLS_KEY = "cursor-byok.browser-preview.calls";

async function providerDiagnosticsCalls(page) {
  return page.evaluate((key) => JSON.parse(localStorage.getItem(key) || "[]")
    .filter((entry) => entry?.name === "GetProviderDiagnostics"), CALLS_KEY);
}

test("供应商诊断只读展示冷却状态且不泄漏秘密", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 500 });
  const now = Date.now();
  await seedPreviewTestPlan(page, {
    recordCalls: true,
    providerDiagnostics: {
      generatedAtUnixMs: now,
      state: "ready",
      routerAvailable: true,
      channels: [
        {
          channelId: "safe-ready",
          displayName: "Safe Ready",
          groupName: "Primary",
          provider: "openai",
          protocolMode: "auto",
          protocolGroup: "responses",
          modelId: "model-ready",
          endpointScheme: "https",
          endpointHost: "provider.example:8443",
          contextWindowTokens: 200000,
          maxCompletionTokens: 65536,
          credentialConfigured: true,
          customHeadersConfigured: true,
          healthState: "ready",
          apiKey: "sk-dom-secret",
          baseURL: "https://user:password@example.invalid?access_token=query-secret",
        },
        {
          channelId: "safe-cooling",
          displayName: "Safe Cooling",
          provider: "anthropic",
          protocolMode: "fixed",
          protocolGroup: "messages",
          modelId: "model-cooling",
          endpointScheme: "https",
          endpointHost: "cooling.example",
          credentialConfigured: true,
          healthState: "cooldown",
          cooldownUntilUnixMs: now + 5 * 60_000,
          customHeadersJSON: "header-secret",
        },
      ],
      modelCatalogCache: {
        entryCount: 3,
        ttlSeconds: 300,
        oldestStoredAtUnixMs: now - 60_000,
        nextExpiryAtUnixMs: now + 240_000,
        cacheKey: "cache-secret",
      },
    },
  }, basePreviewConfig());

  await page.goto("/diagnostics");

  const panel = page.getByTestId("provider-diagnostics-panel");
  await expect(panel).toBeVisible();
  await expect(panel).toHaveAttribute("aria-busy", "false");
  await expect(panel.locator("[aria-live]")).toHaveCount(1);
  await expect(panel.getByTestId("provider-diagnostic-channel")).toHaveCount(2);
  await expect(panel).toContainText("Safe Ready");
  await expect(panel).toContainText("https://provider.example:8443");
  await expect(panel).toContainText("Safe Cooling");
  await expect(panel).toContainText("冷却降权");
  await expect(panel).toContainText("5 分钟后恢复");
  await expect(panel).toContainText("模型目录缓存");
  await expect(panel).toContainText("3");

  const body = page.locator("body");
  for (const secret of ["sk-dom-secret", "user:password", "query-secret", "header-secret", "cache-secret", "access_token"]) {
    await expect(body).not.toContainText(secret);
  }

  await expect.poll(() => providerDiagnosticsCalls(page)).toHaveLength(1);
  await panel.getByRole("button", { name: "刷新状态" }).click();
  await expect.poll(() => providerDiagnosticsCalls(page)).toHaveLength(2);

  await page.evaluate((key) => {
    const plan = JSON.parse(localStorage.getItem(key) || "{}");
    plan.providerDiagnosticsError = true;
    localStorage.setItem(key, JSON.stringify(plan));
  }, "cursor-byok.browser-preview.test-plan");
  await panel.getByRole("button", { name: "刷新状态" }).click();
  await expect(panel).toContainText("显示上次快照");
  await expect(panel).toContainText("Safe Ready");
  await expect.poll(() => providerDiagnosticsCalls(page)).toHaveLength(3);

  const rescan = page.getByRole("button", { name: "重新扫描" });
  await rescan.scrollIntoViewIfNeeded();
  await expect(rescan).toBeVisible();
  await rescan.click();
});

test("供应商诊断按结构化错误码展示对应指引", async ({ page }) => {
  const now = Date.now();
  await seedPreviewTestPlan(page, {
    providerDiagnostics: {
      generatedAtUnixMs: now,
      state: "error",
      errorCode: "diagnostics_resolver_unavailable",
      routerAvailable: false,
      channels: [],
      modelCatalogCache: { entryCount: 0, ttlSeconds: 300 },
    },
  }, basePreviewConfig());
  await page.goto("/diagnostics");

  const panel = page.getByTestId("provider-diagnostics-panel");
  await expect(panel).toContainText("供应商诊断解析器不可用");

  await page.evaluate((key) => {
    const plan = JSON.parse(localStorage.getItem(key) || "{}");
    plan.providerDiagnostics = {
      generatedAtUnixMs: Date.now(),
      state: "unavailable",
      errorCode: "provider_module_unavailable",
      routerAvailable: false,
      channels: [],
      modelCatalogCache: { entryCount: 0, ttlSeconds: 300 },
    };
    localStorage.setItem(key, JSON.stringify(plan));
  }, "cursor-byok.browser-preview.test-plan");
  await panel.getByRole("button", { name: "刷新状态" }).click();
  await expect(panel).toContainText("供应商运行模块尚未就绪");
});

test("供应商诊断英文冷却时长使用无歧义缩写", async ({ page }) => {
  const now = Date.now();
  await page.addInitScript(() => {
    localStorage.setItem("cursor-client:locale:v1", "en-US");
    localStorage.setItem("cursor-client:locale-source:v1", "manual");
  });
  await seedPreviewTestPlan(page, {
    providerDiagnostics: {
      generatedAtUnixMs: now,
      state: "ready",
      routerAvailable: true,
      channels: [{
        channelId: "english-cooling",
        displayName: "English Cooling",
        provider: "openai",
        modelId: "model-en",
        endpointScheme: "https",
        endpointHost: "provider.example",
        credentialConfigured: true,
        healthState: "cooldown",
        cooldownUntilUnixMs: now + 61 * 60_000,
      }],
      modelCatalogCache: { entryCount: 0, ttlSeconds: 300 },
    },
  }, basePreviewConfig());
  await page.goto("/diagnostics");

  const panel = page.getByTestId("provider-diagnostics-panel");
  await expect(panel).toContainText("Recovers in 1 hr 1 min");
  await expect(panel).not.toContainText("1 hours");
  await expect(panel).not.toContainText("1 minutes");
});

test("供应商诊断首次请求失败时只展示错误与重试", async ({ page }) => {
  await seedPreviewTestPlan(page, {
    recordCalls: true,
    providerDiagnosticsError: true,
  }, basePreviewConfig());
  await page.goto("/diagnostics");

  const panel = page.getByTestId("provider-diagnostics-panel");
  await expect(panel.locator("[aria-live]")).toHaveCount(1);
  await expect(panel.getByRole("button", { name: "重试" })).toBeVisible();
  await expect(panel).not.toContainText("内置 Router 尚未就绪");
  await expect(panel).not.toContainText("模型目录缓存");
});
