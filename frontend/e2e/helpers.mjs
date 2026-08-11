// E2E 公共辅助：为每个用例预置浏览器 mock 的确定性测试计划，
// 并提供页面常用入口。测试计划通过 localStorage 注入，
// 由 frontend/src/services/browserBindings.js 在模块加载时读取。

export const CONFIG_STORAGE_KEY = "cursor-byok.browser-preview.config";
export const TEST_PLAN_KEY = "cursor-byok.browser-preview.test-plan";

export const PREVIEW_ADAPTERS = [
  {
    id: "preview-demo-openai",
    displayName: "Demo GPT",
    groupName: "浏览器预览示例",
    type: "openai",
    supplierID: "custom",
    baseURL: "https://api.openai.com/v1",
    apiKey: "browser-preview-demo-key",
    tooltipData: "浏览器预览示例模型",
    modelID: "gpt-4.1-mini",
    reasoningEffort: "medium",
    openAIEndpoint: "/v1/responses",
    openAIRequestGroup: "responses",
    protocolMode: "auto",
    protocolGroup: "responses",
    openAIExtraParamsEnabled: false,
    openAIExtraParamsJSON: "{ }",
    customHeadersEnabled: false,
    customHeadersJSON: "{ }",
    anthropicExtraParamsEnabled: false,
    anthropicExtraParamsJSON: "{ }",
    contextWindowTokens: 0,
    maxCompletionTokens: 0,
    anthropicMaxTokens: 0,
    anthropicThinkingEffort: "xhigh",
    thinkingBudgetTokens: 0,
    pricing: null,
    fastMode: false,
    openAIServiceTier: "",
  },
  {
    id: "preview-demo-gemini",
    displayName: "Demo Gemini",
    groupName: "浏览器预览示例",
    type: "gemini",
    supplierID: "gemini",
    baseURL: "https://generativelanguage.googleapis.com/v1beta",
    apiKey: "browser-preview-gemini-key",
    tooltipData: "浏览器预览 Gemini 示例模型",
    modelID: "gemini-2.5-pro",
    reasoningEffort: "medium",
    openAIEndpoint: "",
    protocolMode: "auto",
    protocolGroup: "gemini_native",
    openAIExtraParamsEnabled: false,
    openAIExtraParamsJSON: "",
    customHeadersEnabled: false,
    customHeadersJSON: "{ }",
    anthropicExtraParamsEnabled: false,
    anthropicExtraParamsJSON: "",
    contextWindowTokens: 1048576,
    maxCompletionTokens: 0,
    anthropicMaxTokens: 0,
    anthropicThinkingEffort: "",
    thinkingBudgetTokens: 0,
    pricing: null,
    fastMode: false,
    openAIServiceTier: "",
  },
];

export function basePreviewConfig({ delegation } = {}) {
  return {
    modelAdapters: structuredClone(PREVIEW_ADAPTERS),
    backendListenAddr: "127.0.0.1:8787",
    proxyListenAddr: "127.0.0.1:8788",
    routing: { mode: "local" },
    homeMetrics: { includeCacheWriteInHitRate: false },
    delegation: delegation ?? {
      enabled: true,
      maxConcurrency: 4,
      groups: [],
      supervision: { enabled: false, supervisorModelID: "", reviewerModelID: "", workerGroupID: "", maxCorrections: 2, maxRetries: 1, maxRounds: 8, allowReassign: false, allowEscalate: false, strictUnavailable: false },
      visionDelegation: { enabled: false, visionModelID: "", mode: "auto" },
    },
  };
}

export function supportedBalance(overrides = {}) {
  return { supported: true, source: "newapi", currency: "USD", total: 100, used: 23.5, remaining: 76.5, planName: "", message: "", ...overrides };
}

export function transientBalanceFailure(overrides = {}) {
  return { supported: false, source: "", currency: "USD", total: null, used: null, remaining: null, message: "E2E 注入：余额查询瞬时失败", transient: true, ...overrides };
}

export function testResultSuccess(overrides = {}) {
  return {
    status: "success",
    summaryText: "E2E 注入：测试通过",
    testedAt: "2026-01-01T00:00:00.000Z",
    tokensPerSecond: 60.5,
    visibleTokensPerSecond: 22.4,
    firstResponseMS: 24_616,
    firstTextTokenMS: 27_214,
    totalDurationMS: 30_000,
    outputTokens: 197,
    visibleOutputTokens: 70,
    reasoningTokens: 127,
    effectiveThinkingEffort: "medium",
    ...overrides,
  };
}

export async function seedPreviewTestPlan(page, plan, config = null) {
  await page.addInitScript(
    ({ key, value, configKey, configValue }) => {
      try {
        localStorage.setItem(key, JSON.stringify(value));
        if (configValue) localStorage.setItem(configKey, JSON.stringify(configValue));
        else localStorage.removeItem(configKey);
      } catch {
        // 无存储时不阻塞用例
      }
    },
    { key: TEST_PLAN_KEY, value: plan ?? {}, configKey: CONFIG_STORAGE_KEY, configValue: config },
  );
}

export async function openSupplierPage(page, { plan, config } = {}) {
  await seedPreviewTestPlan(page, plan, config);
  await page.goto("/supplier?mode=name&groupName=%E6%B5%8F%E8%A7%88%E5%99%A8%E9%A2%84%E8%A7%88%E7%A4%BA%E4%BE%8B");
}

export async function openModelEditorPage(page, { plan, config } = {}) {
  await seedPreviewTestPlan(page, plan, config);
  await page.goto("/model-editor");
}

export async function openDelegationSettingsPage(page, { plan, config } = {}) {
  await seedPreviewTestPlan(page, plan, config);
  await page.goto("/settings?category=delegation");
}

export async function readStoredPreviewConfig(page) {
  return page.evaluate((key) => {
    const raw = localStorage.getItem(key);
    return raw ? JSON.parse(raw) : null;
  }, CONFIG_STORAGE_KEY);
}
