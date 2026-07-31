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
};
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
let previewMCPServers = [
  {
    name: "Preview filesystem",
    identifier: "preview:filesystem",
    transport: "stdio",
    source: "cursor",
    sourceLabel: "Cursor",
    configuredEnabled: true,
    enabled: true,
    hasTools: false,
    toolCount: 0,
    status: "disconnected",
    lastError: "",
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
export const SaveUserConfig = (value) => {
  const next = value && typeof value === "object" ? clone(value) : {};
  previewConfig.modelAdapters = Array.isArray(next.modelAdapters)
    ? next.modelAdapters.map((adapter) => ({ ...adapter, id: adapter.id || nextID() }))
    : [];
  Object.assign(previewConfig, next, { modelAdapters: previewConfig.modelAdapters });
  return Promise.resolve(clone(previewConfig));
};
export const AutoMatchContextWindows = () => {
  const total = previewConfig.modelAdapters.length;
  return Promise.resolve({
    enabled: true,
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
export const GetProviderSpendSummary = () => Promise.resolve([]);
export const GetLocalCacheStats = () => Promise.resolve({ hits: 0, misses: 0, savedInputTokens: 0, savedOutputTokens: 0 });
export const QueryProviderBalance = () => Promise.resolve({ supported: false, source: "", currency: "USD", total: null, used: null, remaining: null, message: "浏览器预览模式：未查询余额", transient: false });
export const ProbeModelAdapter = (adapter) => Promise.resolve({ id: adapter?.id || "", modelID: adapter?.modelID || "", ok: true, status: 200, message: "", rawResponse: "" });
export const GetPromptInjectionSettings = () => Promise.resolve({});
export const SavePromptInjectionSettings = (value) => Promise.resolve(value);
export const RefreshPromptInjection = () => Promise.resolve();
export const RefreshPromptInjectionCatalog = () => Promise.resolve();
export const GetSkillsMCPScanSnapshot = () => Promise.resolve({
  skills: [],
  mcpServers: clone(previewMCPServers),
  config: { enabled: true, disabledSkills: {}, disabledMcpServers: {} },
});
export const RefreshSkillsMCPScan = GetSkillsMCPScanSnapshot;
export const SaveSkillsMCPScanConfig = () => Promise.resolve();
export const GetDelegationTaskSnapshots = () => {
  const now = Date.now();
  return Promise.resolve(clone(previewDelegationTasks.map((item) => ({
    ...item,
    durationMs: item.status === "running" ? Math.max(0, now - item.startedAtUnixMs) : item.durationMs,
  }))));
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
