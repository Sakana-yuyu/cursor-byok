import { browserPreviewMockMetrics, browserPreviewMockProxyState } from "@/services/runtimeAdapter";

const previewConfig = {
  modelAdapters: [
    {
      id: "preview-demo-openai",
      displayName: "Demo GPT",
      groupName: "浏览器预览示例",
      type: "openai",
      baseURL: "https://api.openai.com/v1",
      apiKey: "browser-preview-demo-key",
      tooltipData: "浏览器预览示例模型",
      modelID: "gpt-4.1-mini",
      reasoningEffort: "medium",
      openAIEndpoint: "/v1/responses",
      openAIExtraParamsEnabled: false,
      openAIExtraParamsJSON: "{\n}",
      customHeadersEnabled: false,
      customHeadersJSON: "{\n}",
      anthropicExtraParamsEnabled: false,
      anthropicExtraParamsJSON: "{\n}",
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
      protocolMode: "auto",
      protocolGroup: "gemini_native",
      baseURL: "https://generativelanguage.googleapis.com/v1beta",
      apiKey: "browser-preview-gemini-key",
      tooltipData: "浏览器预览 Gemini 示例模型",
      modelID: "gemini-2.5-pro",
      reasoningEffort: "medium",
      openAIEndpoint: "",
      openAIExtraParamsEnabled: false,
      openAIExtraParamsJSON: "",
      customHeadersEnabled: false,
      customHeadersJSON: "{\n}",
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
  ],
  backendListenAddr: "127.0.0.1:8787",
  proxyListenAddr: "127.0.0.1:8788",
  routing: { mode: "local" },
  homeMetrics: { includeCacheWriteInHitRate: false },
  delegation: {
    enabled: true,
    maxConcurrency: 4,
    groups: [],
    supervision: {
      enabled: false,
      supervisorModelID: "",
      reviewerModelID: "",
      workerGroupID: "",
      maxCorrections: 2,
      maxRetries: 1,
      maxRounds: 8,
      allowReassign: false,
      allowEscalate: false,
      strictUnavailable: false,
    },
    visionDelegation: {
      enabled: false,
      visionModelID: "",
      mode: "auto",
    },
  },
};

const PREVIEW_CONFIG_STORAGE_KEY = "cursor-byok.browser-preview.config";
// E2E 测试控制：测试通过 addInitScript 预置该 localStorage 键，mock 据此注入
// 确定性的余额/测试结果/保存失败响应，避免测试依赖默认随机状态。
const PREVIEW_TEST_PLAN_KEY = "cursor-byok.browser-preview.test-plan";

function readPreviewTestPlan() {
  try {
    const raw = localStorage.getItem(PREVIEW_TEST_PLAN_KEY);
    const parsed = raw ? JSON.parse(raw) : null;
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch {
    return {};
  }
}
const previewTestPlan = readPreviewTestPlan();

function previewTestBalance(adapter) {
  const override = readPreviewTestPlan()?.balance;
  if (override == null) return null;
  const value = typeof override === "function" ? override(adapter) : override;
  return value && typeof value === "object" ? value : null;
}

function previewTestResult(adapter) {
  const override = readPreviewTestPlan()?.testResult;
  if (override == null) return null;
  const value = typeof override === "function" ? override(adapter) : override;
  return value && typeof value === "object" ? value : null;
}

function previewTestFailSaveConfig() {
  return Boolean(readPreviewTestPlan()?.saveConfigFailure);
}

function previewTestFailDelegationSave() {
  return Boolean(readPreviewTestPlan()?.delegationSaveFailure);
}

function previewAdapterForStorage(adapter) {
  if (!adapter || typeof adapter !== "object") {
    return {};
  }
  const safeAdapter = { ...adapter };
  delete safeAdapter.apiKey;
  delete safeAdapter.balanceAccessToken;
  delete safeAdapter.customHeadersJSON;
  delete safeAdapter.balanceQueryHeadersJSON;
  delete safeAdapter.balanceQueryHeaders;
  return safeAdapter;
}

function previewConfigForStorage() {
  return {
    ...previewConfig,
    modelAdapters: Array.isArray(previewConfig.modelAdapters)
      ? previewConfig.modelAdapters.map((adapter) => previewAdapterForStorage(adapter))
      : [],
  };
}

try {
  const storedPreviewConfig = JSON.parse(localStorage.getItem(PREVIEW_CONFIG_STORAGE_KEY) || "null");
  if (storedPreviewConfig && typeof storedPreviewConfig === "object") {
    Object.assign(previewConfig, storedPreviewConfig);
    previewConfig.delegation = {
      ...previewConfig.delegation,
      ...(storedPreviewConfig.delegation && typeof storedPreviewConfig.delegation === "object"
        ? storedPreviewConfig.delegation
        : {}),
    };
    // Migrate older preview entries that may have stored demo credentials.
    persistPreviewConfig();
  }
} catch {
  // Browser preview remains usable when storage is disabled.
}

function persistPreviewConfig() {
  try {
    localStorage.setItem(PREVIEW_CONFIG_STORAGE_KEY, JSON.stringify(previewConfigForStorage()));
  } catch {
    // In-memory preview fallback is still valid without localStorage.
  }
}
let editorContext = { index: -1, adapterJSON: "{}" };
// E2E 测试可通过测试计划预置编辑器上下文（编辑既有模型 / 快速添加两种形态）。
if (previewTestPlan?.editorContext && typeof previewTestPlan.editorContext === "object") {
  const seeded = previewTestPlan.editorContext;
  editorContext = {
    index: Number.isInteger(seeded.index) ? seeded.index : -1,
    adapterJSON: typeof seeded.adapterJSON === "string" ? seeded.adapterJSON : "{}",
  };
}
let previewDelegationTasks = [
  {
    id: "preview-task-running",
    aggregateId: "preview-aggregate",
    description: "Review the active workspace changes",
    modelId: "preview-demo-openai",
    modelName: "Demo GPT",
    modelGroupId: "preview-group",
    executionMode: "local",
    status: "running",
    toolCallCount: 2,
    eventId: "delegation-event-2",
    sequence: 2,
    eventType: "running",
    parentRequestId: "preview-request",
    parentExecId: "preview-aggregate",
    groupId: "preview-aggregate",
    queuedAtUnixMs: Date.now() - 14000,
    startedAtUnixMs: Date.now() - 12000,
    finishedAtUnixMs: 0,
    updatedAtUnixMs: Date.now() - 12000,
    durationMs: 12000,
    cancelable: true,
    workerRole: "generalPurpose",
    supervisionPhase: "reviewing",
    reviewPending: true,
    supervisionRound: 2,
    correctionCount: 1,
    retryCount: 0,
    reassignCount: 0,
    escalateCount: 0,
    issueCategory: "missing_evidence",
    progressSummary: "Collected the changed files and summarized the main edits, pending the final evidence pass.",
  },
  {
    id: "preview-vision-running",
    aggregateId: "preview-vision-agg",
    description: "视觉委派：识别截图中的报错信息",
    modelId: "preview-demo-gemini",
    modelName: "Demo Gemini",
    modelGroupId: "preview-group",
    executionMode: "vision",
    status: "running",
    toolCallCount: 0,
    eventId: "vision-event-1",
    sequence: 4,
    eventType: "running",
    parentRequestId: "preview-vision-req",
    parentExecId: "preview-vision-agg",
    groupId: "preview-vision-agg",
    queuedAtUnixMs: Date.now() - 9000,
    startedAtUnixMs: Date.now() - 8000,
    finishedAtUnixMs: 0,
    updatedAtUnixMs: Date.now() - 3000,
    durationMs: 8000,
    cancelable: false,
    workerRole: "vision",
    supervisionPhase: "",
    reviewPending: false,
    supervisionRound: 0,
    correctionCount: 0,
    retryCount: 0,
    reassignCount: 0,
    escalateCount: 0,
    issueCategory: "",
    progressSummary: "识图中 2/3",
  },
  {
    id: "preview-vision-completed",
    aggregateId: "preview-vision-done",
    description: "视觉委派：检查配置页面截图布局",
    modelId: "preview-demo-gemini",
    modelName: "Demo Gemini",
    modelGroupId: "preview-group",
    executionMode: "vision",
    status: "completed",
    toolCallCount: 0,
    eventId: "vision-event-2",
    sequence: 5,
    eventType: "completed",
    parentRequestId: "preview-vision-done-req",
    parentExecId: "preview-vision-done",
    groupId: "preview-vision-done",
    queuedAtUnixMs: Date.now() - 60000,
    startedAtUnixMs: Date.now() - 58000,
    finishedAtUnixMs: Date.now() - 30000,
    updatedAtUnixMs: Date.now() - 30000,
    durationMs: 28000,
    cancelable: false,
    workerRole: "vision",
    supervisionPhase: "",
    reviewPending: false,
    supervisionRound: 0,
    correctionCount: 0,
    retryCount: 0,
    reassignCount: 0,
    escalateCount: 0,
    issueCategory: "",
    progressSummary: "识图完成 2/2",
  },
  {
    id: "preview-task-completed",
    aggregateId: "preview-aggregate",
    description: "Summarize implementation notes",
    modelId: "preview-demo-gemini",
    modelName: "Demo Gemini",
    modelGroupId: "preview-group",
    executionMode: "cursor",
    status: "completed",
    toolCallCount: 1,
    eventId: "delegation-event-3",
    sequence: 3,
    eventType: "completed",
    parentRequestId: "preview-request",
    parentExecId: "preview-aggregate",
    groupId: "preview-aggregate",
    queuedAtUnixMs: Date.now() - 30000,
    startedAtUnixMs: Date.now() - 28000,
    finishedAtUnixMs: Date.now() - 8000,
    updatedAtUnixMs: Date.now() - 8000,
    durationMs: 20000,
    cancelable: false,
    workerRole: "generalPurpose",
    supervisionPhase: "completed",
    reviewPending: false,
    supervisionRound: 1,
    correctionCount: 0,
    retryCount: 0,
    reassignCount: 0,
    escalateCount: 0,
    issueCategory: "",
    progressSummary: "Completed the implementation note summary and returned a concise result.",
  },
];
let previewSkills = [
  {
    name: "superpowers-systematic-debugging",
    source: "workspace",
    description: "在遇到任何 bug、测试失败或意外行为时使用，先建立证据再提出修复方案。",
    fullPath: "E:\\MyProject\\cursor-byok\\.agents\\skills\\superpowers-systematic-debugging\\SKILL.md",
  },
  {
    name: "superpowers-test-driven-development",
    source: "workspace",
    description: "在实现任何功能或修复之前，先编写测试并观察其失败。",
    fullPath: "E:\\MyProject\\cursor-byok\\.agents\\skills\\superpowers-test-driven-development\\SKILL.md",
  },
  {
    name: "cursor-client-e2e-debugging",
    source: "workspace",
    description: "排查 Cursor 客户端本地模式、工具调用、后端存储与 provider 回放故障。",
    fullPath: "E:\\MyProject\\cursor-byok\\.agents\\skills\\cursor-client-e2e-debugging\\SKILL.md",
  },
  {
    name: "uploadcursor",
    source: "user",
    description: "发布 cursor-byok 新版本：更新版本号、推送 tag 触发 GitHub Action 自动构建。",
    fullPath: "C:\\Users\\Administrator\\.cursor\\skills\\uploadcursor\\SKILL.md",
  },
  {
    name: "frontend-design",
    source: "user",
    description: "创建或修改网页、桌面界面、组件和交互时使用，关注信息架构与视觉层级。",
    fullPath: "C:\\Users\\Administrator\\.cursor\\skills\\frontend-design\\SKILL.md",
  },
];
let previewMCPServers = [
  {
    name: "Preview filesystem",
    identifier: "preview:filesystem",
    transport: "stdio",
    source: "cursor",
    sourceLabel: "Cursor",
    configuredEnabled: true,
    enabled: true,
    hasTools: true,
    toolCount: 12,
    status: "connected",
    lastError: "",
  },
  {
    name: "PostgreSQL",
    identifier: "preview:postgres",
    transport: "sse",
    source: "cursor",
    sourceLabel: "Cursor",
    configuredEnabled: true,
    enabled: true,
    hasTools: false,
    toolCount: 0,
    status: "disconnected",
    lastError: "",
  },
  {
    name: "Playwright",
    identifier: "preview:playwright",
    transport: "stdio",
    source: "project",
    sourceLabel: "项目",
    configuredEnabled: true,
    enabled: false,
    hasTools: false,
    toolCount: 0,
    status: "disconnected",
    lastError: "",
  },
  {
    name: "GitHub",
    identifier: "preview:github",
    transport: "http",
    source: "user",
    sourceLabel: "用户",
    configuredEnabled: true,
    enabled: true,
    hasTools: false,
    toolCount: 0,
    status: "disconnected",
    lastError: "Failed to connect: EADDRINUSE 127.0.0.1:38837",
  },
];

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

function nextID() {
  return `preview-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

export const IsWindows = () => Promise.resolve(false);
export const GetState = () => Promise.resolve(previewProxyState());
export const LoadUserConfig = () => Promise.resolve(clone(previewConfig));
export const GetDelegationConfig = () => Promise.resolve(clone(previewConfig.delegation));
export const SaveDelegationConfig = (value) => {
  if (previewTestFailDelegationSave()) {
    return Promise.reject(new Error("E2E 注入：委派配置保存失败"));
  }
  previewConfig.delegation = clone(value || {});
  persistPreviewConfig();
  return Promise.resolve(clone(previewConfig.delegation));
};
export const GetGoals = () => Promise.resolve([]);
export const StartGoal = (_goalText, _modelID) => Promise.resolve("mock-goal-" + Date.now());
export const StopGoal = (_conversationID) => Promise.resolve(null);
export const SaveUserConfig = (value) => {
  if (previewTestFailSaveConfig()) {
    return Promise.reject(new Error("E2E 注入：配置保存失败"));
  }
  const next = value && typeof value === "object" ? clone(value) : {};
  previewConfig.modelAdapters = Array.isArray(next.modelAdapters)
    ? next.modelAdapters.map((adapter) => ({ ...adapter, id: adapter.id || nextID() }))
    : [];
  Object.assign(previewConfig, next, { modelAdapters: previewConfig.modelAdapters });
  persistPreviewConfig();
  return Promise.resolve(clone(previewConfig));
};
export const AutoMatchContextWindows = (force = false) => {
  const total = previewConfig.modelAdapters.length;
  void force;
  return Promise.resolve({
    enabled: true,
    switchEnabled: true,
    changed: false,
    total,
    fromCatalog: 0,
    fromProbe: 0,
    unchanged: total,
    details: [],
  });
};
export const DiagnoseModelAdapters = () => Promise.resolve({ total: previewConfig.modelAdapters.length, issues: [] });
export const ApplyDiagnosticFixes = () => Promise.resolve({ total: previewConfig.modelAdapters.length, issues: [] });
// previewProxyRunning 让预览模式的启停真的改变状态。此前 StartProxy/StopProxy
// 都返回同一份「未运行」快照，界面点了启动却始终显示未运行，看起来像启动失败。
let previewProxyRunning = false;

function previewProxyState() {
  const state = browserPreviewMockProxyState();
  return {
    ...state,
    serviceRunning: previewProxyRunning,
    backendRunning: previewProxyRunning,
    proxyRunning: previewProxyRunning,
  };
}

export const StartProxy = () => {
  previewProxyRunning = true;
  return Promise.resolve(previewProxyState());
};
export const StopProxy = () => {
  previewProxyRunning = false;
  return Promise.resolve(previewProxyState());
};
export const RepairProxySettings = () => Promise.resolve({ settingsApplied: true, settingsPath: "", proxyURL: "http://127.0.0.1:18080", cursorRunning: false, needsCursorRestart: false, details: ["浏览器预览模式：模拟修复成功"] });
export const GetAdRuntime = () => Promise.resolve({ available: false, slots: [], window: {} });
export const OpenExternalURL = () => Promise.resolve();
export const GetHomeMetricsSummary = () => Promise.resolve(browserPreviewMockMetrics());
export const CheckForUpdates = () => Promise.resolve();
export const GetAppVersion = () => Promise.resolve("Browser Preview");
export const GetFooterAuthorInfo = () => Promise.resolve(null);
export const InstallReadyUpdate = () => Promise.resolve();
export const GetModelEditorContext = () => Promise.resolve(clone(editorContext));
export const OpenConfigWindow = () => Promise.resolve();
export const OpenFooterAuthorHome = () => Promise.resolve();
export const OpenHistoryWindow = () => Promise.resolve();
// 浏览器预览没有本地文件系统，无法真的打包日志。返回空路径会被上层当成
// 「导出成功但路径为空」，这里显式失败，界面才会给出可理解的提示。
export const ExportLogs = () => Promise.reject(new Error("浏览器预览模式不支持导出日志 ZIP，请在桌面客户端中使用"));
export const OpenModelConfigWindow = () => Promise.resolve();
export const OpenMetricsDetailWindow = () => Promise.resolve();
export const OpenRequestMetricsWindow = () => Promise.resolve();
export const OpenStatsOverlayWindow = () => Promise.resolve();
export const UpdateStatsOverlayWindow = (_style, _alwaysOnTop) => Promise.resolve();
export const SetStatsOverlayAlwaysOnTop = (_alwaysOnTop) => Promise.resolve();
export const CloseStatsOverlayWindow = () => Promise.resolve();
export const SetMainWindowCloseAction = () => Promise.resolve();
export const CloseApplication = () => Promise.resolve();
export const DetectCursorPath = (manualPath = "") => Promise.resolve(manualPath || "C:\\Program Files\\Cursor\\Cursor.exe");
export const LaunchCursor = () => Promise.resolve();
export const RestartCursor = () => Promise.resolve({ wasRunning: false, killed: false, relaunched: true, cursorPath: "C:\\Program Files\\Cursor\\Cursor.exe", details: ["浏览器预览模式：模拟重启"] });
export const IsCursorRunning = () => Promise.resolve(false);
export const OpenModelEditorWindow = (index, adapterJSON) => {
  editorContext = { index: Number.isInteger(index) ? index : -1, adapterJSON: String(adapterJSON || "{}") };
  return Promise.resolve();
};
export const EnableReaderMCP = (_url, _apiKey, _model) =>
  Promise.resolve({ identifier: "vision-reader", scriptPath: "", wasAdded: true });
export const RepairCACorruption = () =>
  Promise.resolve({ repaired: true, backupPath: "", detail: "浏览器预览模式：模拟修复" });
export const GetCARepairStatus = () =>
  Promise.resolve({ repaired: false, repairedAt: "", detail: "" });
export const TestModelAdapter = (adapter) => {
  const override = previewTestResult(adapter);
  if (override) return Promise.resolve(clone(override));
  return Promise.resolve({ status: "success", adapterID: adapter?.id || "preview", summaryText: "浏览器预览模式：未发起请求" });
};
export const GetModelAdapterTestResults = () => Promise.resolve([]);
export const FetchModelCatalog = (request) => {
  const type = String(request?.type || "").toLowerCase();
  if (type === "gemini") {
    return Promise.resolve({
      models: [
        { id: "gemini-2.5-pro", contextWindowTokens: 1048576 },
        { id: "gemini-2.5-flash", contextWindowTokens: 1048576 },
      ],
    });
  }
  if (type === "anthropic") {
    return Promise.resolve({
      models: [
        { id: "claude-sonnet-4-5", contextWindowTokens: 200000 },
        { id: "claude-haiku-4-5", contextWindowTokens: 200000 },
      ],
    });
  }
  return Promise.resolve({
    models: [
      { id: "gpt-4.1-mini", contextWindowTokens: 1047576 },
      { id: "gpt-5-mini", contextWindowTokens: 400000 },
    ],
  });
};
export const GetRecentRequestMetrics = () => Promise.resolve([]);
export const GetProviderEvents = () => Promise.resolve([]);
export const GetRecentRequestMetricsCount = () => Promise.resolve(0);
export const GetRecentRequestMetricsAbnormalCount = () => Promise.resolve(0);
export const GetRecentRequestMetricsDegradedCount = () => Promise.resolve(0);
export const ResetUsageMetrics = () => Promise.resolve();
export const GetMetricsRangeSummary = () => Promise.resolve({
  requestCount: 0,
  inputTokens: 0,
  outputTokens: 0,
  cacheReadTokens: 0,
  cacheWriteTokens: 0,
  totalTokens: 0,
  cacheRate: null,
});
export const GetMetricsTokenBuckets = () => Promise.resolve([]);
export const GetProviderSpendSummary = () => Promise.resolve([
  { station: "https://api.openai.com", provider: "gpt-4.1-mini", providerCalls: 128, inputTokens: 8421300, outputTokens: 512300, cacheReadTokens: 3210000, cacheWriteTokens: 890000, totalTokens: 13033400, estimatedCostUsd: 12.8412, currency: "USD", pricingSource: "official" },
  { station: "https://generativelanguage.googleapis.com", provider: "gemini-2.5-pro", providerCalls: 64, inputTokens: 3510000, outputTokens: 288400, cacheReadTokens: 1200000, cacheWriteTokens: 420000, totalTokens: 5418400, estimatedCostUsd: 6.23, currency: "USD", pricingSource: "catalog" },
  { station: "https://api.deepseek.com", provider: "deepseek-chat", providerCalls: 210, inputTokens: 15320000, outputTokens: 2100000, cacheReadTokens: 8900000, cacheWriteTokens: 1500000, totalTokens: 27820000, estimatedCostUsd: 5.871, currency: "USD", pricingSource: "official" },
  { station: "https://api.moonshot.cn", provider: "kimi-k2.5", providerCalls: 86, inputTokens: 5120000, outputTokens: 624000, cacheReadTokens: 2100000, cacheWriteTokens: 640000, totalTokens: 8484000, estimatedCostUsd: 3.42, currency: "USD", pricingSource: "configured" },
  { station: "https://api.minimax.io", provider: "MiniMax-M3", providerCalls: 45, inputTokens: 1980000, outputTokens: 312000, cacheReadTokens: 660000, cacheWriteTokens: 210000, totalTokens: 3162000, estimatedCostUsd: 1.86, currency: "USD", pricingSource: "average" },
  { station: "https://api.anthropic.com", provider: "claude-sonnet-4-5", providerCalls: 22, inputTokens: 864000, outputTokens: 142000, cacheReadTokens: 420000, cacheWriteTokens: 96000, totalTokens: 1522000, estimatedCostUsd: 2.94, currency: "USD", pricingSource: "official" },
  { station: "https://api.zhipuai.cn", provider: "glm-4.6", providerCalls: 150, inputTokens: 9800000, outputTokens: 1240000, cacheReadTokens: 4300000, cacheWriteTokens: 900000, totalTokens: 16240000, estimatedCostUsd: null, currency: "", pricingSource: "" },
  { station: "https://api.volces.com", provider: "doubao-seed-1.6", providerCalls: 38, inputTokens: 1450000, outputTokens: 218000, cacheReadTokens: 420000, cacheWriteTokens: 110000, totalTokens: 2198000, estimatedCostUsd: 0.74, currency: "USD", pricingSource: "catalog" },
]);
export const GetLocalCacheStats = () => Promise.resolve({ hits: 0, misses: 0, savedInputTokens: 0, savedOutputTokens: 0 });
export const QueryProviderBalance = (adapter) => {
  const override = previewTestBalance(adapter);
  if (override) return Promise.resolve(clone(override));
  return Promise.resolve({ supported: false, source: "", currency: "USD", total: null, used: null, remaining: null, message: "浏览器预览模式：未查询余额", transient: false });
};
export const QueryAllProviderBalances = () => Promise.resolve([
  {
    adapterId: "preview-demo-openai",
    displayName: "Demo GPT",
    groupName: "OpenAI 官方",
    baseURL: "https://api.openai.com/v1",
    modelID: "gpt-4.1-mini",
    balance: { supported: true, source: "newapi", currency: "CNY", total: 100, used: 23.5, remaining: 76.5, planName: "", message: "" },
  },
  {
    adapterId: "preview-demo-gemini",
    displayName: "Demo Gemini",
    groupName: "Gemini 官方",
    baseURL: "https://generativelanguage.googleapis.com/v1beta",
    modelID: "gemini-2.5-pro",
    balance: { supported: true, source: "token_plan", currency: "%", total: 100, used: 32, remaining: 68, planName: "2H 使用窗口", message: "" },
  },
  {
    adapterId: "preview-demo-claude",
    displayName: "Demo Claude",
    groupName: "Anthropic 官方",
    baseURL: "https://api.anthropic.com",
    modelID: "claude-sonnet-4-5",
    balance: { supported: true, source: "openai_billing", currency: "USD", total: 20, used: null, remaining: 20, planName: "", message: "" },
  },
  {
    adapterId: "preview-demo-deepseek",
    displayName: "Demo DeepSeek",
    groupName: "DeepSeek 官方",
    baseURL: "https://api.deepseek.com",
    modelID: "deepseek-chat",
    balance: { supported: true, source: "token_plan", currency: "%", total: null, used: null, remaining: null, unlimited: true, planName: "不限额度套餐", message: "" },
  },
  {
    adapterId: "preview-demo-minimax",
    displayName: "Demo MiniMax",
    groupName: "MiniMax 官方",
    baseURL: "https://api.minimax.io",
    modelID: "MiniMax-M3",
    balance: { supported: false, source: "", currency: "USD", total: null, used: null, remaining: null, message: "鉴权失败：Invalid API key", transient: false },
  },
  {
    adapterId: "preview-demo-kimi",
    displayName: "Demo Kimi",
    groupName: "Moonshot 官方",
    baseURL: "https://api.moonshot.cn",
    modelID: "kimi-k2.5",
    balance: { supported: true, source: "newapi", currency: "CNY", total: 50, used: null, remaining: null, planName: "", message: "" },
  },
]);
export const ProbeModelAdapter = (adapter) => Promise.resolve({ id: adapter?.id || "", modelID: adapter?.modelID || "", ok: true, status: 200, message: "", rawResponse: "" });
export const GetPromptInjectionSettings = () => Promise.resolve({});
export const SavePromptInjectionSettings = (value) => Promise.resolve(value);
export const RefreshPromptInjection = () => Promise.resolve();
export const RefreshPromptInjectionCatalog = () => Promise.resolve();
let previewScanConfig = {
  enabled: true,
  disabledSkills: { "superpowers-test-driven-development": true },
  disabledMcpServers: { "preview:playwright": true },
  skillSummaries: {
    "superpowers-systematic-debugging": "遇到 bug、测试失败或意外行为时，先建立证据链再提出修复方案，避免盲改代码。",
    "uploadcursor": "负责发布 cursor-byok 新版本：更新版本号、推送 tag 触发 GitHub Action 自动构建。",
  },
  mcpSummaries: {
    "preview:filesystem": "基于文件系统的 MCP 服务，提供 12 个文件读写与目录操作工具。",
  },
};
export const GetSkillsMCPScanSnapshot = () => Promise.resolve({
  skills: clone(previewSkills),
  mcpServers: clone(previewMCPServers),
  config: clone(previewScanConfig),
});
export const RefreshSkillsMCPScan = GetSkillsMCPScanSnapshot;
export const SaveSkillsMCPScanConfig = (config) => {
  previewScanConfig = { ...previewScanConfig, ...(config || {}) };
  return Promise.resolve();
};
export const ReadSkillFile = (_workspaceRoot, name) => {
  const skill = previewSkills.find((item) => String(item.name).toLowerCase() === String(name || "").toLowerCase());
  if (!skill) return Promise.reject(new Error(`未找到技能 ${name}`));
  return Promise.resolve({
    name: skill.name,
    fullPath: skill.fullPath,
    content: `---\nname: ${skill.name}\ndescription: ${skill.description}\n---\n\n# ${skill.name}\n\n${skill.description}\n\n## 使用场景\n\n- 调试与排查\n- 编写测试\n- 回归验证\n`,
  });
};
export const SaveSkillFile = () => Promise.resolve(true);
export const GenerateSkillSummary = (_workspaceRoot, kind, key) => {
  const normalizedKey = String(key || "").toLowerCase();
  if (kind === "skill") {
    const skill = previewSkills.find((item) => String(item.name).toLowerCase() === normalizedKey);
    if (!skill) return Promise.reject(new Error(`未找到技能 ${key}`));
    const summary = `「${skill.name}」在${skill.source === "workspace" ? "项目" : "用户"}技能库中提供${skill.description}`;
    previewScanConfig.skillSummaries = { ...(previewScanConfig.skillSummaries || {}), [normalizedKey]: summary };
    return Promise.resolve(summary);
  }
  if (kind === "mcp") {
    const server = previewMCPServers.find((item) => String(item.identifier).toLowerCase() === normalizedKey);
    if (!server) return Promise.reject(new Error(`未找到 MCP server ${key}`));
    const summary = `「${server.name}」基于 ${server.transport} 传输接入${server.toolCount > 0 ? `，提供 ${server.toolCount} 个工具` : ""}${server.status === "connected" ? "，当前已连接" : "，当前未连接"}。`;
    previewScanConfig.mcpSummaries = { ...(previewScanConfig.mcpSummaries || {}), [normalizedKey]: summary };
    return Promise.resolve(summary);
  }
  return Promise.reject(new Error(`未知的生成目标类型 ${kind}`));
};
export const GetDelegationTaskSnapshots = () => {
  const now = Date.now();
  return Promise.resolve(clone(previewDelegationTasks.map((item) => ({
    ...item,
    durationMs: item.status === "running" ? Math.max(0, now - item.startedAtUnixMs) : item.durationMs,
  }))));
};
// previewHistorySessions 是可变的预览数据。此前删除/清理都返回成功但数据不变，
// 占用统计还是写死的常量，界面上「清理成功」之后占用一点没少，等于假成功。
const previewHistorySessions = [
  {
    id: "preview-session-2026-07-31-001",
    title: "优化站点消耗卡片布局",
    createdAtUnixMs: Date.UTC(2026, 6, 31, 9, 12, 0),
    updatedAtUnixMs: Date.UTC(2026, 6, 31, 9, 40, 0),
    sizeBytes: 128 * 1024,
    debugSizeBytes: 0,
    subagentType: "",
    mode: "agent",
    hasDebug: false,
    status: "completed",
    requestId: "",
  },
  {
    id: "preview-session-2026-07-31-002",
    title: "调试视觉委派任务条轮换逻辑",
    createdAtUnixMs: Date.UTC(2026, 6, 31, 14, 5, 0),
    updatedAtUnixMs: Date.UTC(2026, 6, 31, 15, 2, 0),
    sizeBytes: 356 * 1024,
    debugSizeBytes: 52 * 1024 * 1024,
    subagentType: "debug",
    mode: "debug",
    hasDebug: true,
    status: "provider_error",
    requestId: "a1b2c3d4-5678-90ab-cdef-1234567890ab",
  },
  {
    id: "preview-session-2026-07-30-001",
    title: "浮窗外观配置与持久化",
    createdAtUnixMs: Date.UTC(2026, 6, 30, 10, 30, 0),
    updatedAtUnixMs: Date.UTC(2026, 6, 30, 11, 15, 0),
    sizeBytes: 96 * 1024,
    debugSizeBytes: 0,
    subagentType: "",
    mode: "agent",
    hasDebug: false,
    status: "completed",
    requestId: "",
  },
  {
    id: "preview-session-2026-07-30-002",
    title: "排查 5173 端口连接失败",
    createdAtUnixMs: Date.UTC(2026, 6, 30, 16, 20, 0),
    updatedAtUnixMs: Date.UTC(2026, 6, 30, 16, 55, 0),
    sizeBytes: 210 * 1024,
    debugSizeBytes: 18 * 1024 * 1024,
    subagentType: "generalPurpose",
    mode: "agent",
    hasDebug: true,
    status: "failed",
    requestId: "b2c3d4e5-6789-01ab-cdef-234567890abc",
  },
  {
    id: "preview-session-2026-07-29-001",
    title: "模型适配器测试被用户取消",
    createdAtUnixMs: Date.UTC(2026, 6, 29, 11, 0, 0),
    updatedAtUnixMs: Date.UTC(2026, 6, 29, 11, 20, 0),
    sizeBytes: 64 * 1024,
    debugSizeBytes: 0,
    subagentType: "",
    mode: "agent",
    hasDebug: false,
    status: "canceled",
    requestId: "",
  },
  {
    id: "preview-session-2026-06-15-001",
    title: "供应商分组模式重构",
    createdAtUnixMs: Date.UTC(2026, 5, 15, 9, 0, 0),
    updatedAtUnixMs: Date.UTC(2026, 5, 15, 12, 45, 0),
    sizeBytes: 812 * 1024,
    debugSizeBytes: 0,
    subagentType: "generalPurpose",
    mode: "multitask",
    hasDebug: false,
    status: "completed",
    requestId: "",
  },
  {
    id: "preview-session-2026-06-15-002",
    title: "多模型配置与余额查询",
    createdAtUnixMs: Date.UTC(2026, 5, 15, 14, 30, 0),
    updatedAtUnixMs: Date.UTC(2026, 5, 15, 16, 10, 0),
    sizeBytes: 460 * 1024,
    debugSizeBytes: 0,
    subagentType: "",
    mode: "agent",
    hasDebug: false,
    status: "completed",
    requestId: "",
  },
  {
    id: "preview-session-2025-12-03-001",
    title: "响应缓存磁盘持久化调研",
    createdAtUnixMs: Date.UTC(2025, 11, 3, 10, 0, 0),
    updatedAtUnixMs: Date.UTC(2025, 11, 3, 10, 50, 0),
    sizeBytes: 1_024 * 1024,
    debugSizeBytes: 0,
    subagentType: "generalPurpose",
    mode: "ask",
    hasDebug: false,
    status: "completed",
    requestId: "",
  },
];

// previewOrphanDebugBytes 模拟无会话归属的孤儿调试日志，只有「清理全部」才会释放它，
// 这样预览模式也能体现出后端统一遍历与逐会话清理的差异。
let previewOrphanDebugBytes = 24 * 1024 * 1024;

function clearPreviewSessionDebug(session) {
  const freed = Number(session.debugSizeBytes || 0);
  session.debugSizeBytes = 0;
  session.hasDebug = false;
  return freed;
}

export const GetHistorySessions = () => Promise.resolve(clone(previewHistorySessions));
export const DeleteHistorySessions = (sessionIDs) => {
  const ids = new Set(Array.isArray(sessionIDs) ? sessionIDs : []);
  for (let index = previewHistorySessions.length - 1; index >= 0; index -= 1) {
    if (ids.has(previewHistorySessions[index].id)) previewHistorySessions.splice(index, 1);
  }
  return Promise.resolve();
};
export const ClearHistory = () => {
  const removed = previewHistorySessions.length;
  previewHistorySessions.length = 0;
  previewOrphanDebugBytes = 0;
  return Promise.resolve(removed);
};
export const DeleteHistoryDebugLogs = (sessionIDs) => {
  const ids = new Set(Array.isArray(sessionIDs) ? sessionIDs : []);
  let freed = 0;
  for (const session of previewHistorySessions) {
    if (ids.has(session.id)) freed += clearPreviewSessionDebug(session);
  }
  return Promise.resolve(freed);
};
export const PurgeAllHistoryDebugLogs = () => {
  let freed = previewOrphanDebugBytes;
  previewOrphanDebugBytes = 0;
  for (const session of previewHistorySessions) freed += clearPreviewSessionDebug(session);
  return Promise.resolve(freed);
};
export const GetHistoryDebugUsage = () => Promise.resolve(
  previewHistorySessions.reduce((total, session) => total + Number(session.debugSizeBytes || 0), previewOrphanDebugBytes),
);

// previewSessionDebugFiles 为带调试日志的预览会话提供可读的 debug 文件清单与尾部内容，
// 让诊断页在浏览器预览模式下也能演示「列出文件 -> 查看尾部 -> 导出证据包」的完整链路。
// 文件内容内嵌示例 requestID，便于演示按 requestID 过滤。
const previewDebugFileTail = {
  "bidi.raw.jsonl": '{"request_id":"a1b2c3d4-5678-90ab-cdef-1234567890ab","dir":"recv","ts":"2026-07-31T15:01:42Z"}\n{"request_id":"a1b2c3d4-5678-90ab-cdef-1234567890ab","dir":"send","ts":"2026-07-31T15:01:43Z","status":500}\n',
  "bidi.decoded.jsonl": '{"request_id":"a1b2c3d4-5678-90ab-cdef-1234567890ab","role":"assistant","delta":"模型返回异常状态 404"}\n',
  "runtime.jsonl": '{"request_id":"a1b2c3d4-5678-90ab-cdef-1234567890ab","event":"provider_error","message":"upstream returned 404"}\n',
  "runsse.jsonl": '{"request_id":"a1b2c3d4-5678-90ab-cdef-1234567890ab","chunk":"data: {\\\"error\\\":\\\"model not found\\\"}\\n\\n"}\n',
  "provider.jsonl": '{"request_id":"a1b2c3d4-5678-90ab-cdef-1234567890ab","ok":false,"status":404,"body":"model not found"}\n',
};

function previewSessionDebugFiles(sessionID) {
  const session = previewHistorySessions.find((item) => item.id === sessionID && item.hasDebug);
  if (!session) return [];
  const baseTime = session.updatedAtUnixMs || Date.now();
  const names = ["bidi.raw.jsonl", "bidi.decoded.jsonl", "runtime.jsonl", "runsse.jsonl", "provider.jsonl"];
  return names.map((name, index) => ({
    name,
    sizeBytes: 256 + index * 128,
    modTimeUnixMs: baseTime - index * 1000,
  }));
}

export const ListSessionDebugFiles = (sessionID) => Promise.resolve(clone(previewSessionDebugFiles(sessionID)));
export const ReadSessionDebugTail = (sessionID, filename) => {
  const session = previewHistorySessions.find((item) => item.id === sessionID && item.hasDebug);
  if (!session) return Promise.reject(new Error(`会话 ${sessionID} 没有调试日志`));
  const tail = previewDebugFileTail[filename];
  if (!tail) return Promise.reject(new Error(`debug 文件 ${filename} 不在白名单内`));
  return Promise.resolve(tail);
};
// 浏览器预览没有本地文件系统，无法真的打包证据包。诚实失败，避免空路径被当成成功。
export const ExportSessionDebugBundle = (sessionID) => {
  const session = previewHistorySessions.find((item) => item.id === sessionID);
  if (!session) return Promise.reject(new Error(`未找到会话 ${sessionID}`));
  if (!session.hasDebug) return Promise.reject(new Error(`会话 ${sessionID} 没有调试日志，无法导出证据包`));
  return Promise.reject(new Error("浏览器预览模式不支持导出证据包 ZIP，请在桌面客户端中使用"));
};
export const CancelDelegationTask = (taskID) => {
  const task = previewDelegationTasks.find((item) => item.id === taskID && item.cancelable);
  if (!task) return Promise.resolve(false);
  const sequence = previewDelegationTasks.reduce((highest, item) => Math.max(highest, Number(item.sequence) || 0), 0) + 1;
  task.status = "canceled";
  task.sequence = sequence;
  task.eventId = `delegation-event-${sequence}`;
  task.eventType = "canceled";
  task.cancelable = false;
  task.finishedAtUnixMs = Date.now();
  task.updatedAtUnixMs = task.finishedAtUnixMs;
  task.durationMs = Math.max(0, task.finishedAtUnixMs - task.startedAtUnixMs);
  return Promise.resolve(true);
};
export const ConnectMCPServer = (_workspaceRoot, identifier) => {
  const server = previewMCPServers.find((item) => item.identifier === identifier);
  if (!server) return Promise.reject(new Error("MCP server not found"));
  Object.assign(server, { status: "connected", hasTools: true, toolCount: 3, lastError: "" });
  return Promise.resolve(clone(server));
};
export const DisconnectMCPServer = (_workspaceRoot, identifier) => {
  const server = previewMCPServers.find((item) => item.identifier === identifier);
  if (!server) return Promise.reject(new Error("MCP server not found"));
  Object.assign(server, { status: "disconnected", hasTools: false, toolCount: 0, lastError: "" });
  return Promise.resolve(clone(server));
};
export const CancelMCPServerConnection = (_identifier, _attemptID) => Promise.resolve(true);
export const GetCursorAccountStatus = () =>
  Promise.resolve({ state: "signed_out", authId: "", email: "", error: "" });
export const StartCursorAccountLogin = () =>
  Promise.resolve({ state: "waiting", authId: "", email: "", error: "浏览器预览模式：模拟登录中" });
export const DisconnectCursorAccount = () =>
  Promise.resolve({ state: "signed_out", authId: "", email: "", error: "" });
