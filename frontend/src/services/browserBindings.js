import { browserPreviewMockMetrics, browserPreviewMockProxyState } from "@/services/runtimeAdapter";

const previewConfig = {
  modelAdapters: [
    {
      id: "preview-demo-openai",
      displayName: "Demo GPT",
      groupName: "浏览器预览示例",
      type: "openai",
      baseURL: "https://api.openai.com/v1",
      apiKey: "sk-browser-preview-demo",
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
      apiKey: "AIza-browser-preview-demo",
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
export const GetState = () => Promise.resolve(browserPreviewMockProxyState());
export const LoadUserConfig = () => Promise.resolve(clone(previewConfig));
export const GetDelegationConfig = () => Promise.resolve(clone(previewConfig.delegation));
export const SaveDelegationConfig = (value) => {
  previewConfig.delegation = clone(value || {});
  persistPreviewConfig();
  return Promise.resolve(clone(previewConfig.delegation));
};
export const SaveUserConfig = (value) => {
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
export const StartProxy = () => Promise.resolve(browserPreviewMockProxyState());
export const StopProxy = () => Promise.resolve(browserPreviewMockProxyState());
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
export const ExportLogs = () => Promise.resolve("");
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
export const OpenModelEditorWindow = (index, adapterJSON) => {
  editorContext = { index: Number.isInteger(index) ? index : -1, adapterJSON: String(adapterJSON || "{}") };
  return Promise.resolve();
};
export const TestModelAdapter = (adapter) => Promise.resolve({ status: "success", adapterID: adapter?.id || "preview", summaryText: "浏览器预览模式：未发起请求" });
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
export const GetRecentRequestMetricsCount = () => Promise.resolve(0);
export const GetRecentRequestMetricsAbnormalCount = () => Promise.resolve(0);
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
export const QueryProviderBalance = () => Promise.resolve({ supported: false, source: "", currency: "USD", total: null, used: null, remaining: null, message: "浏览器预览模式：未查询余额", transient: false });
export const QueryAllProviderBalances = () => Promise.resolve([
  {
    adapterId: "preview-demo-openai",
    displayName: "Demo GPT",
    groupName: "浏览器预览示例",
    modelID: "gpt-4.1-mini",
    balance: { supported: true, source: "newapi", currency: "CNY", total: 100, used: 23.5, remaining: 76.5, planName: "", message: "" },
  },
  {
    adapterId: "preview-demo-gemini",
    displayName: "Demo Gemini",
    groupName: "浏览器预览示例",
    modelID: "gemini-2.5-pro",
    balance: { supported: true, source: "token_plan", currency: "%", total: 100, used: 32, remaining: 68, planName: "2H 使用窗口", message: "" },
  },
  {
    adapterId: "preview-demo-claude",
    displayName: "Demo Claude",
    groupName: "浏览器预览示例",
    modelID: "claude-sonnet-4-5",
    balance: { supported: true, source: "openai_billing", currency: "USD", total: 20, used: null, remaining: 20, planName: "", message: "" },
  },
  {
    adapterId: "preview-demo-deepseek",
    displayName: "Demo DeepSeek",
    groupName: "浏览器预览示例",
    modelID: "deepseek-chat",
    balance: { supported: true, source: "token_plan", currency: "%", total: null, used: null, remaining: null, unlimited: true, planName: "不限额度套餐", message: "" },
  },
  {
    adapterId: "preview-demo-minimax",
    displayName: "Demo MiniMax",
    groupName: "浏览器预览示例",
    modelID: "MiniMax-M3",
    balance: { supported: false, source: "", currency: "USD", total: null, used: null, remaining: null, message: "鉴权失败：Invalid API key", transient: false },
  },
  {
    adapterId: "preview-demo-kimi",
    displayName: "Demo Kimi",
    groupName: "浏览器预览示例",
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
export const GetHistorySessions = () => Promise.resolve([]);
export const DeleteHistorySessions = () => Promise.resolve();
export const ClearHistory = () => Promise.resolve(0);
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
