import {
  GetState, LoadUserConfig, SaveUserConfig, StartProxy, StopProxy, TestModelAdapter,
  GetModelAdapterTestResults, FetchModelCatalog, ProbeModelAdapter, QueryProviderBalance,
  GetPromptInjectionSettings,
  SavePromptInjectionSettings, RefreshPromptInjection, RefreshPromptInjectionCatalog,
  AutoMatchContextWindows,
} from "@bindings/cursor/internal/bridge/proxyservice.js";
import { GetAdRuntime, OpenExternalURL } from "@bindings/cursor/internal/bridge/adservice.js";
import {
  GetHomeMetricsSummary,
  GetLocalCacheStats,
  GetMetricsRangeSummary,
  GetMetricsTokenBuckets,
  GetProviderSpendSummary,
  GetRecentRequestMetrics,
  GetRecentRequestMetricsCount,
  ResetUsageMetrics,
} from "@bindings/cursor/internal/bridge/metricsservice.js";
import {
  CheckForUpdates, GetAppVersion, GetFooterAuthorInfo, InstallReadyUpdate,
  GetModelEditorContext, OpenConfigWindow, OpenFooterAuthorHome, OpenHistoryWindow,
  OpenMetricsDetailWindow, OpenRequestMetricsWindow, OpenStatsOverlayWindow, UpdateStatsOverlayWindow,
  CloseStatsOverlayWindow, OpenModelConfigWindow, OpenModelEditorWindow, ExportLogs,
} from "@bindings/cursor/internal/bridge/windowservice.js";
import { isBrowserPreview, browserPreviewMockMetrics, browserPreviewMockProxyState } from "@/services/runtimeAdapter";

const desktopMethods = {
  LoadUserConfig, SaveUserConfig, GetState, GetHomeMetricsSummary, GetAdRuntime, OpenExternalURL,
  StartProxy, StopProxy, OpenHistoryWindow, OpenConfigWindow, GetAppVersion, GetFooterAuthorInfo,
  CheckForUpdates, InstallReadyUpdate, OpenFooterAuthorHome, OpenModelConfigWindow, OpenModelEditorWindow,
  OpenMetricsDetailWindow, OpenRequestMetricsWindow, OpenStatsOverlayWindow, UpdateStatsOverlayWindow,
  CloseStatsOverlayWindow, GetModelEditorContext, TestModelAdapter, GetModelAdapterTestResults, GetRecentRequestMetrics,
  GetMetricsRangeSummary, GetMetricsTokenBuckets, GetProviderSpendSummary, GetLocalCacheStats,
  ResetUsageMetrics,
  FetchModelCatalog, ProbeModelAdapter, QueryProviderBalance, GetPromptInjectionSettings,
  SavePromptInjectionSettings, RefreshPromptInjection,
  RefreshPromptInjectionCatalog, ExportLogs, AutoMatchContextWindows,
};

const API_LOG_PREFIX = "[clientApi]";
const PROXY_SERVICE_NAME = "cursor/internal/bridge.ProxyService";

function logSuccess(name, payload, result) {
  console.log(`${API_LOG_PREFIX} ${name} response`, { payload, result });
}

function logError(name, payload, error) {
  console.error(`${API_LOG_PREFIX} ${name} error`, { payload, error });
}

function withApiLogging(name, payload, runner) {
  return Promise.resolve().then(runner).then((result) => {
    logSuccess(name, payload, result);
    return result;
  }).catch((error) => {
    logError(name, payload, error);
    throw error;
  });
}

function invoke(_modulePath, method, args = []) {
  const fn = desktopMethods[method];
  return typeof fn === "function" ? Promise.resolve(fn(...args)) : Promise.resolve(undefined);
}

function desktopOrMock(mock, modulePath, method, args = []) {
  return isBrowserPreview ? Promise.resolve(typeof mock === "function" ? mock() : mock) : invoke(modulePath, method, args);
}

export function loadUserConfig() {
  return withApiLogging("LoadUserConfig", undefined, () => desktopOrMock(() => LoadUserConfig(), "@bindings/cursor/internal/bridge/proxyservice.js", "LoadUserConfig"));
}

export function saveUserConfig(payload) {
  return withApiLogging("SaveUserConfig", payload, () => desktopOrMock(() => SaveUserConfig(payload), "@bindings/cursor/internal/bridge/proxyservice.js", "SaveUserConfig", [payload]));
}

export function getProxyState() {
  return withApiLogging("GetState", undefined, () => desktopOrMock(browserPreviewMockProxyState(), "@bindings/cursor/internal/bridge/proxyservice.js", "GetState"));
}

export function getHomeMetricsSummary() {
  return withApiLogging("GetHomeMetricsSummary", undefined, () => desktopOrMock(browserPreviewMockMetrics(), "@bindings/cursor/internal/bridge/metricsservice.js", "GetHomeMetricsSummary"));
}

export function resetUsageMetrics() {
  return withApiLogging("ResetUsageMetrics", undefined, () => desktopOrMock(undefined, "@bindings/cursor/internal/bridge/metricsservice.js", "ResetUsageMetrics"));
}

export function getAdRuntime() {
  return desktopOrMock({ available: false, slots: [], window: {} }, "@bindings/cursor/internal/bridge/adservice.js", "GetAdRuntime");
}

export function openAdExternalURL(url) {
  return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/adservice.js", "OpenExternalURL", [url]);
}

export function startProxyService() {
  return withApiLogging("StartProxy", undefined, () => desktopOrMock(browserPreviewMockProxyState(), "@bindings/cursor/internal/bridge/proxyservice.js", "StartProxy"));
}

export function stopProxyService() {
  return withApiLogging("StopProxy", undefined, () => desktopOrMock(browserPreviewMockProxyState(), "@bindings/cursor/internal/bridge/proxyservice.js", "StopProxy"));
}

export function openLogsDirectory() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "OpenHistoryWindow"); }
export function exportLogs() { return desktopOrMock("", "@bindings/cursor/internal/bridge/windowservice.js", "ExportLogs"); }
export function openConfigWindow() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "OpenConfigWindow"); }
export function getAppVersion() { return desktopOrMock("Browser Preview", "@bindings/cursor/internal/bridge/windowservice.js", "GetAppVersion"); }
export function getFooterAuthorInfo() { return desktopOrMock(null, "@bindings/cursor/internal/bridge/windowservice.js", "GetFooterAuthorInfo"); }
export function checkForUpdates() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "CheckForUpdates"); }
export function installReadyUpdate() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "InstallReadyUpdate"); }
export function openFooterAuthorHome() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "OpenFooterAuthorHome"); }
export function openModelConfig() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "OpenModelConfigWindow"); }
export function openMetricsDetailWindow() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "OpenMetricsDetailWindow"); }
export function openRequestMetricsWindow() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "OpenRequestMetricsWindow"); }
export function openStatsOverlayWindow() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "OpenStatsOverlayWindow"); }
export function updateStatsOverlayWindow(style, alwaysOnTop) {
  return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "UpdateStatsOverlayWindow", [style, alwaysOnTop]);
}
export function closeStatsOverlayWindow() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "CloseStatsOverlayWindow"); }
export function openModelEditor(index, adapterJSON) {
  return desktopOrMock(
    () => OpenModelEditorWindow(index, adapterJSON),
    "@bindings/cursor/internal/bridge/windowservice.js",
    "OpenModelEditorWindow",
    [index, adapterJSON],
  ).then(async () => {
    if (!isBrowserPreview) return undefined;
    const { default: router } = await import("@/router");
    return router.push({ path: "/model-editor", query: { index: String(index ?? -1) } });
  });
}
export function getModelEditorContext() {
  return desktopOrMock(() => GetModelEditorContext(), "@bindings/cursor/internal/bridge/windowservice.js", "GetModelEditorContext");
}

export function testModelAdapter(adapter) {
  if (isBrowserPreview) return Promise.resolve({ status: "success", adapterID: adapter?.id || "preview", summaryText: "浏览器预览模式：未发起请求" });
  return withApiLogging("TestModelAdapter", adapter, () => invoke("@bindings/cursor/internal/bridge/proxyservice.js", "TestModelAdapter", [adapter]));
}

export function getModelAdapterTestResults() {
  return desktopOrMock([], "@bindings/cursor/internal/bridge/proxyservice.js", "GetModelAdapterTestResults");
}

export function probeModelAdapter(adapter) {
  if (isBrowserPreview) {
    return Promise.resolve({ id: adapter?.id || adapter?.modelID || "", modelID: adapter?.modelID || "", ok: true, status: 200, message: "", rawResponse: "" });
  }
  return withApiLogging("ProbeModelAdapter", adapter, () => invoke("@bindings/cursor/internal/bridge/proxyservice.js", "ProbeModelAdapter", [adapter]));
}

export function fetchRecentRequestMetrics(limit = 0, offset = 0) {
  return desktopOrMock([], "@bindings/cursor/internal/bridge/metricsservice.js", "GetRecentRequestMetrics", [limit, offset]);
}

export function fetchRecentRequestMetricsCount() {
  return desktopOrMock(0, "@bindings/cursor/internal/bridge/metricsservice.js", "GetRecentRequestMetricsCount");
}

// 全量异常请求数（跨分页），用于「请求明细」页异常总数展示。
export function fetchRecentRequestMetricsAbnormalCount() {
  return desktopOrMock(0, "@bindings/cursor/internal/bridge/metricsservice.js", "GetRecentRequestMetricsAbnormalCount");
}

export function fetchMetricsRangeSummary(startUnixMs = 0, endUnixMs = 0, model = "") {
  return desktopOrMock(
    {
      requestCount: 0,
      inputTokens: 0,
      outputTokens: 0,
      cacheReadTokens: 0,
      cacheWriteTokens: 0,
      totalTokens: 0,
      cacheRate: null,
    },
    "@bindings/cursor/internal/bridge/metricsservice.js",
    "GetMetricsRangeSummary",
    [startUnixMs, endUnixMs, model],
  );
}

export function fetchMetricsTokenBuckets(startUnixMs = 0, endUnixMs = 0, model = "", bucketHours = 1) {
  return desktopOrMock(
    [],
    "@bindings/cursor/internal/bridge/metricsservice.js",
    "GetMetricsTokenBuckets",
    [startUnixMs, endUnixMs, model, bucketHours],
  );
}

export function fetchModelCatalog(request) {
  if (isBrowserPreview) return Promise.resolve({ models: [] });
  return withApiLogging("FetchModelCatalog", request, () => invoke("@bindings/cursor/internal/bridge/proxyservice.js", "FetchModelCatalog", [request]));
}

// 一键自动配对所有模型适配器的上下文窗口：目录命中则覆盖，目录未命中则探测 provider /models 回填。
export function autoMatchContextWindows() {
  if (isBrowserPreview) {
    return Promise.resolve({ enabled: false, changed: false, total: 0, fromCatalog: 0, fromProbe: 0, unchanged: 0 });
  }
  return withApiLogging("AutoMatchContextWindows", undefined, () => invoke("@bindings/cursor/internal/bridge/proxyservice.js", "AutoMatchContextWindows", []));
}

// 查询中转站余额/额度；失败时后端返回结构化的 { supported:false, message } 结果。
export function queryProviderBalance(request) {
  if (isBrowserPreview) {
    return Promise.resolve({ supported: false, source: "", currency: "USD", total: null, used: null, remaining: null, message: "浏览器预览模式：未查询余额", transient: false });
  }
  return withApiLogging("QueryProviderBalance", request, () => invoke("@bindings/cursor/internal/bridge/proxyservice.js", "QueryProviderBalance", [request]));
}

// 按中转站聚合区间内的用量与美元花费（GroupName → baseURL host → provider 类型）。
export function fetchProviderSpendSummary(startUnixMs = 0, endUnixMs = 0) {
  return desktopOrMock(
    [],
    "@bindings/cursor/internal/bridge/metricsservice.js",
    "GetProviderSpendSummary",
    [startUnixMs, endUnixMs],
  );
}

// 本地（进程内）响应缓存命中统计，与 provider prompt-cache 分开展示。
export function fetchLocalCacheStats() {
  return desktopOrMock(
    { hits: 0, misses: 0, savedInputTokens: 0, savedOutputTokens: 0 },
    "@bindings/cursor/internal/bridge/metricsservice.js",
    "GetLocalCacheStats",
  );
}

export function getPromptInjectionSettings() {
  return desktopOrMock({ enabled: false, softwareChineseEnabled: false, mode: "replace", repo: "", ref: "", selectedTemplate: "" }, "@bindings/cursor/internal/bridge/proxyservice.js", "GetPromptInjectionSettings");
}
export function savePromptInjectionSettings(config) { return desktopOrMock(config, "@bindings/cursor/internal/bridge/proxyservice.js", "SavePromptInjectionSettings", [config]); }
export function refreshPromptInjection() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/proxyservice.js", "RefreshPromptInjection"); }
export function refreshPromptInjectionCatalog() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/proxyservice.js", "RefreshPromptInjectionCatalog"); }