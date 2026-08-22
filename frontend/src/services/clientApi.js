import {
  GetState, GetMirrorCaptureStatus, LoadUserConfig, SaveUserConfig, StartProxy, StopProxy, TestModelAdapter,
  GetModelAdapterTestResults, FetchModelCatalog, ProbeModelAdapter, QueryProviderBalance, GetProviderDiagnostics,
  GetPromptInjectionSettings,
  SavePromptInjectionSettings, RefreshPromptInjection, RefreshPromptInjectionCatalog,
  AutoMatchContextWindows, DiagnoseModelAdapters, ApplyDiagnosticFixes,
  GetSkillsMCPScanSnapshot, RefreshSkillsMCPScan, SaveSkillsMCPScanConfig,
  ReadSkillFile, SaveSkillFile, GenerateSkillSummary,
  GetRecentWorkspaceRoot,
  QueryAllProviderBalances,
  SyncProviderBalancesAfterAccountChange,
  RepairProxySettings,
  GetDelegationConfig, SaveDelegationConfig, GetDelegationExecutorSnapshots, RefreshDelegationExecutorProbes, InstallDelegationExecutor,
  GetCursorAccountStatus, StartCursorAccountLogin, DisconnectCursorAccount,
  GetControlCenterOverview, ListCursorAccounts, ImportCursorAccount,
  PrepareCursorAccountRecoveryExport, ExecuteCursorAccountRecoveryExport,
  SetCurrentCursorAccount, UpdateCursorAccountTags, DeleteCursorAccounts,
  PrepareCursorClientAccountSwitch, ExecuteCursorClientAccountSwitch,
  BeginCursorAccountLogin, GetCursorAccountLoginStatus, CancelCursorAccountLogin,
  ListRequestSources, BuildRequestComparison, ExportSanitizedRequestComparison,
  GetRoutingPolicy, SaveRoutingPolicy, PreviewRoutingDecision, GetRoutingDecisionHistory,
  GetAgentRuns, GetAgentRun, CancelAgentRun, PrepareAgentRunRetry, ExecuteAgentRunRetry, ExportSanitizedAgentRunReport,
  ListConfigProfiles, SaveCurrentConfigProfile, DeleteConfigProfile, PreviewConfigProfile,
  PrepareConfigProfileApply, ExecuteConfigProfileApply, ExportConfigProfile, ImportConfigProfile,
  RepairCACorruption, GetCARepairStatus,
  OfferDefenderExclusion, GetDefenderExclusionState, DismissDefenderExclusion,
  GetTerminalEnvironmentStatus, ApplyTerminalEnvironment, InstallTerminalDependency,
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
  GetRecentRequestMetricsAbnormalCount,
  GetRecentRequestMetricsDegradedCount,
  GetProviderEvents,
  ResetUsageMetrics,
} from "@bindings/cursor/internal/bridge/metricsservice.js";
import {
  CheckForUpdates, GetAppVersion, GetFooterAuthorInfo, InstallReadyUpdate,
  GetModelEditorContext, OpenConfigWindow, OpenFooterAuthorHome, OpenHistoryWindow,
  OpenMetricsDetailWindow, OpenRequestMetricsWindow, OpenStatsOverlayWindow, UpdateStatsOverlayWindow, SetStatsOverlayAlwaysOnTop,
  CloseStatsOverlayWindow, OpenModelConfigWindow, OpenModelEditorWindow, ExportLogs,
  SetMainWindowCloseAction, CloseApplication, DetectCursorPath, LaunchCursor, RestartCursor, IsCursorRunning, OpenMirrorCaptureDirectory,
} from "@bindings/cursor/internal/bridge/windowservice.js";
import { isBrowserPreview, browserPreviewMockMetrics } from "@/services/runtimeAdapter";
import {
  reportRuntimeOperationFailure,
  reportRuntimeOperationSuccess,
} from "@/services/runtimeHealth";
import { normalizeClientError, safeErrorLogAttributes, summarizePayload } from "@/utils/errorContract";

const desktopMethods = {
  LoadUserConfig, SaveUserConfig, GetState, GetMirrorCaptureStatus, GetHomeMetricsSummary, GetAdRuntime, OpenExternalURL,
  StartProxy, StopProxy, OpenHistoryWindow, OpenMirrorCaptureDirectory, OpenConfigWindow, GetAppVersion, GetFooterAuthorInfo,
  CheckForUpdates, InstallReadyUpdate, OpenFooterAuthorHome, OpenModelConfigWindow, OpenModelEditorWindow,
  OpenMetricsDetailWindow, OpenRequestMetricsWindow, OpenStatsOverlayWindow, UpdateStatsOverlayWindow, SetStatsOverlayAlwaysOnTop,
  CloseStatsOverlayWindow, SetMainWindowCloseAction, CloseApplication, GetModelEditorContext, TestModelAdapter, GetModelAdapterTestResults, GetRecentRequestMetrics,
  GetMetricsRangeSummary, GetMetricsTokenBuckets, GetProviderSpendSummary, GetLocalCacheStats,
  GetRecentRequestMetricsCount, GetRecentRequestMetricsAbnormalCount, GetRecentRequestMetricsDegradedCount,
  GetProviderEvents,
  ResetUsageMetrics,
  FetchModelCatalog, ProbeModelAdapter, QueryProviderBalance, GetProviderDiagnostics, GetPromptInjectionSettings,
  SavePromptInjectionSettings, RefreshPromptInjection,
  RefreshPromptInjectionCatalog, ExportLogs, AutoMatchContextWindows, DiagnoseModelAdapters, ApplyDiagnosticFixes,
  DetectCursorPath, LaunchCursor, RestartCursor, IsCursorRunning, GetSkillsMCPScanSnapshot, RefreshSkillsMCPScan, SaveSkillsMCPScanConfig,
  ReadSkillFile, SaveSkillFile, GenerateSkillSummary,
  GetRecentWorkspaceRoot,
  QueryAllProviderBalances,
  SyncProviderBalancesAfterAccountChange,
  RepairProxySettings,
  GetDelegationConfig, SaveDelegationConfig, GetDelegationExecutorSnapshots, RefreshDelegationExecutorProbes, InstallDelegationExecutor,
  GetCursorAccountStatus, StartCursorAccountLogin, DisconnectCursorAccount,
  GetControlCenterOverview, ListCursorAccounts, ImportCursorAccount,
  PrepareCursorAccountRecoveryExport, ExecuteCursorAccountRecoveryExport,
  SetCurrentCursorAccount, UpdateCursorAccountTags, DeleteCursorAccounts,
  PrepareCursorClientAccountSwitch, ExecuteCursorClientAccountSwitch,
  BeginCursorAccountLogin, GetCursorAccountLoginStatus, CancelCursorAccountLogin,
  ListRequestSources, BuildRequestComparison, ExportSanitizedRequestComparison,
  GetRoutingPolicy, SaveRoutingPolicy, PreviewRoutingDecision, GetRoutingDecisionHistory,
  GetAgentRuns, GetAgentRun, CancelAgentRun, PrepareAgentRunRetry, ExecuteAgentRunRetry, ExportSanitizedAgentRunReport,
  ListConfigProfiles, SaveCurrentConfigProfile, DeleteConfigProfile, PreviewConfigProfile,
  PrepareConfigProfileApply, ExecuteConfigProfileApply, ExportConfigProfile, ImportConfigProfile,
  RepairCACorruption, GetCARepairStatus,
  OfferDefenderExclusion, GetDefenderExclusionState, DismissDefenderExclusion,
  GetTerminalEnvironmentStatus, ApplyTerminalEnvironment, InstallTerminalDependency,
};

const API_LOG_PREFIX = "[clientApi]";
const PROXY_SERVICE_NAME = "cursor/internal/bridge.ProxyService";
const DEFAULT_API_TIMEOUT_MS = 30_000;
const MODEL_TEST_TIMEOUT_MS = 60_000;
const AUTO_MATCH_TIMEOUT_MS = 120_000;
const RUNTIME_HEALTH_OPERATIONS = new Set([
  "GetState",
  "LoadUserConfig",
  "SaveUserConfig",
  "StartProxy",
  "StopProxy",
]);

function operationError(normalized, cause) {
  const error = new Error(normalized.userMessage, cause ? { cause } : undefined);
  Object.assign(error, normalized);
  return error;
}

// 浏览器预览测试计划：E2E 通过 localStorage 注入确定性 mock 响应。
// 仅浏览器预览模式读取；桌面模式始终走真实绑定。
function readBrowserPreviewTestPlan() {
  if (!isBrowserPreview) return {};
  try {
    const raw = localStorage.getItem("cursor-byok.browser-preview.test-plan");
    const parsed = raw ? JSON.parse(raw) : null;
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch {
    return {};
  }
}

function logSuccess(name, payload, result, traceId) {
  if (import.meta.env.DEV) {
    console.debug(`${API_LOG_PREFIX} ${name} response`, { payload: summarizePayload(payload), traceId, result: summarizePayload(result) });
  }
}

function logError(name, payload, _error, normalized) {
  console.error(`${API_LOG_PREFIX} ${name} error`, {
    payload: summarizePayload(payload),
    ...safeErrorLogAttributes(normalized, { operation: name, traceId: normalized.traceId }),
  });
}

function withTimeout(promise, timeoutMs, operation, signal) {
  if ((!timeoutMs || timeoutMs <= 0) && !signal) return promise;
  let timer;
  let removeAbortListener = () => {};
  const control = new Promise((_, reject) => {
    if (timeoutMs > 0) {
      timer = setTimeout(() => reject(new Error(`${operation} timeout`)), timeoutMs);
    }
    if (signal) {
      const abort = () => reject(new Error(`${operation} canceled`));
      if (signal.aborted) abort();
      else {
        signal.addEventListener("abort", abort, { once: true });
        removeAbortListener = () => signal.removeEventListener("abort", abort);
      }
    }
  });
  return Promise.race([promise, control]).finally(() => {
    if (timer) clearTimeout(timer);
    removeAbortListener();
  });
}

export function invokeOperation(name, payload, runner, options = {}) {
  const traceId = String(options.traceId || globalThis.crypto?.randomUUID?.() || `ui-${Date.now().toString(36)}`).trim();
  const timeoutMs = options.timeoutMs ?? DEFAULT_API_TIMEOUT_MS;
  const affectsRuntimeHealth = options.affectsRuntimeHealth ?? RUNTIME_HEALTH_OPERATIONS.has(name);
  return withTimeout(Promise.resolve().then(runner), timeoutMs, name, options.signal).then((result) => {
    logSuccess(name, payload, result, traceId);
    if (affectsRuntimeHealth) reportRuntimeOperationSuccess();
    return result;
  }).catch((error) => {
    const normalized = normalizeClientError(error, { operation: name, traceId });
    logError(name, payload, error, normalized);
    if (affectsRuntimeHealth && normalized.disposition !== "canceled") {
      reportRuntimeOperationFailure(normalized);
    }
    throw operationError(normalized, error);
  });
}

function withApiLogging(name, payload, runner, options) {
  return invokeOperation(name, payload, runner, options);
}

function invoke(_modulePath, method, args = []) {
  const fn = desktopMethods[method];
  if (typeof fn !== "function") {
    return Promise.reject(new Error(`${API_LOG_PREFIX} 未注册的绑定方法: ${method}`));
  }
  return Promise.resolve(fn(...args));
}

function desktopOrMockRaw(mock, modulePath, method, args = []) {
  return isBrowserPreview ? Promise.resolve(typeof mock === "function" ? mock() : mock) : invoke(modulePath, method, args);
}

function desktopOrMock(mock, modulePath, method, args = [], options = {}) {
  return invokeOperation(method, args, () => desktopOrMockRaw(mock, modulePath, method, args), options);
}

export function loadUserConfig() {
  return withApiLogging("LoadUserConfig", undefined, () => desktopOrMockRaw(() => LoadUserConfig(), "@bindings/cursor/internal/bridge/proxyservice.js", "LoadUserConfig"));
}

export function saveUserConfig(payload) {
  return withApiLogging("SaveUserConfig", payload, () => desktopOrMockRaw(() => SaveUserConfig(payload), "@bindings/cursor/internal/bridge/proxyservice.js", "SaveUserConfig", [payload]));
}

const CURSOR_ACCOUNT_SIGNED_OUT_MOCK = { state: "signed_out", authId: "", email: "", error: "" };

export function getCursorAccountStatus() {
  return withApiLogging("GetCursorAccountStatus", undefined, () => desktopOrMockRaw(CURSOR_ACCOUNT_SIGNED_OUT_MOCK, "@bindings/cursor/internal/bridge/proxyservice.js", "GetCursorAccountStatus"));
}

export function startCursorAccountLogin() {
  return withApiLogging("StartCursorAccountLogin", undefined, () => desktopOrMockRaw(CURSOR_ACCOUNT_SIGNED_OUT_MOCK, "@bindings/cursor/internal/bridge/proxyservice.js", "StartCursorAccountLogin"));
}

export function disconnectCursorAccount() {
  return withApiLogging("DisconnectCursorAccount", undefined, () => desktopOrMockRaw(CURSOR_ACCOUNT_SIGNED_OUT_MOCK, "@bindings/cursor/internal/bridge/proxyservice.js", "DisconnectCursorAccount"));
}

export function getControlCenterOverview() {
  return withApiLogging("GetControlCenterOverview", undefined, () => desktopOrMockRaw(() => GetControlCenterOverview(), "@bindings/cursor/internal/bridge/proxyservice.js", "GetControlCenterOverview"));
}

export function listCursorAccounts() {
  return withApiLogging("ListCursorAccounts", undefined, () => desktopOrMockRaw(() => ListCursorAccounts(), "@bindings/cursor/internal/bridge/proxyservice.js", "ListCursorAccounts"));
}

export function importCursorAccount(request) {
  return withApiLogging("ImportCursorAccount", { mode: request?.mode }, () => desktopOrMockRaw(() => ImportCursorAccount(request), "@bindings/cursor/internal/bridge/proxyservice.js", "ImportCursorAccount", [request]));
}

export function prepareCursorAccountRecoveryExport(request) {
  return withApiLogging("PrepareCursorAccountRecoveryExport", { count: request?.accountIds?.length ?? 0 }, () => desktopOrMockRaw(() => PrepareCursorAccountRecoveryExport(request), "@bindings/cursor/internal/bridge/proxyservice.js", "PrepareCursorAccountRecoveryExport", [request]));
}

export function executeCursorAccountRecoveryExport(confirmationToken) {
  return withApiLogging("ExecuteCursorAccountRecoveryExport", undefined, () => desktopOrMockRaw(() => ExecuteCursorAccountRecoveryExport(confirmationToken, ""), "@bindings/cursor/internal/bridge/proxyservice.js", "ExecuteCursorAccountRecoveryExport", [confirmationToken, ""]));
}

export function setCurrentCursorAccount(accountID) {
  return withApiLogging("SetCurrentCursorAccount", { accountID }, () => desktopOrMockRaw(() => SetCurrentCursorAccount(accountID), "@bindings/cursor/internal/bridge/proxyservice.js", "SetCurrentCursorAccount", [accountID]));
}

export function updateCursorAccountTags(accountID, tags) {
  return withApiLogging("UpdateCursorAccountTags", { accountID }, () => desktopOrMockRaw(() => UpdateCursorAccountTags(accountID, tags), "@bindings/cursor/internal/bridge/proxyservice.js", "UpdateCursorAccountTags", [accountID, tags]));
}

export function deleteCursorAccounts(request) {
  return withApiLogging("DeleteCursorAccounts", { count: request?.accountIds?.length ?? 0 }, () => desktopOrMockRaw(() => DeleteCursorAccounts(request), "@bindings/cursor/internal/bridge/proxyservice.js", "DeleteCursorAccounts", [request]));
}

export function prepareCursorClientAccountSwitch(accountID) {
  return withApiLogging("PrepareCursorClientAccountSwitch", { accountID }, () => desktopOrMockRaw(() => PrepareCursorClientAccountSwitch(accountID), "@bindings/cursor/internal/bridge/proxyservice.js", "PrepareCursorClientAccountSwitch", [accountID]));
}

export function executeCursorClientAccountSwitch(confirmationToken) {
  return withApiLogging("ExecuteCursorClientAccountSwitch", undefined, () => desktopOrMockRaw(() => ExecuteCursorClientAccountSwitch(confirmationToken), "@bindings/cursor/internal/bridge/proxyservice.js", "ExecuteCursorClientAccountSwitch", [confirmationToken]));
}

export function beginCursorAccountLogin() {
  return withApiLogging("BeginCursorAccountLogin", undefined, () => desktopOrMockRaw(() => BeginCursorAccountLogin(), "@bindings/cursor/internal/bridge/proxyservice.js", "BeginCursorAccountLogin"));
}

export function getCursorAccountLoginStatus(sessionID) {
  return withApiLogging("GetCursorAccountLoginStatus", undefined, () => desktopOrMockRaw(() => GetCursorAccountLoginStatus(sessionID), "@bindings/cursor/internal/bridge/proxyservice.js", "GetCursorAccountLoginStatus", [sessionID]));
}

export function cancelCursorAccountLogin(sessionID) {
  return withApiLogging("CancelCursorAccountLogin", undefined, () => desktopOrMockRaw(() => CancelCursorAccountLogin(sessionID), "@bindings/cursor/internal/bridge/proxyservice.js", "CancelCursorAccountLogin", [sessionID]));
}

export function listRequestSources(query) {
  return withApiLogging("ListRequestSources", { kind: query?.kind }, () => desktopOrMockRaw(() => ListRequestSources(query), "@bindings/cursor/internal/bridge/proxyservice.js", "ListRequestSources", [query]));
}

export function buildRequestComparison(request) {
  return withApiLogging("BuildRequestComparison", undefined, () => desktopOrMockRaw(() => BuildRequestComparison(request), "@bindings/cursor/internal/bridge/proxyservice.js", "BuildRequestComparison", [request]));
}

export function exportSanitizedRequestComparison(comparisonID) {
  return withApiLogging("ExportSanitizedRequestComparison", undefined, () => desktopOrMockRaw(() => ExportSanitizedRequestComparison(comparisonID), "@bindings/cursor/internal/bridge/proxyservice.js", "ExportSanitizedRequestComparison", [comparisonID]));
}

export function getRoutingPolicy() {
  return withApiLogging("GetRoutingPolicy", undefined, () => desktopOrMockRaw(() => GetRoutingPolicy(), "@bindings/cursor/internal/bridge/proxyservice.js", "GetRoutingPolicy"));
}

export function saveRoutingPolicy(policy) {
  return withApiLogging("SaveRoutingPolicy", { strategy: policy?.strategy }, () => desktopOrMockRaw(() => SaveRoutingPolicy(policy), "@bindings/cursor/internal/bridge/proxyservice.js", "SaveRoutingPolicy", [policy]));
}

export function previewRoutingDecision(request) {
  return withApiLogging("PreviewRoutingDecision", { modelId: request?.modelId }, () => desktopOrMockRaw(() => PreviewRoutingDecision(request), "@bindings/cursor/internal/bridge/proxyservice.js", "PreviewRoutingDecision", [request]));
}

export function getRoutingDecisionHistory(query) {
  return withApiLogging("GetRoutingDecisionHistory", undefined, () => desktopOrMockRaw(() => GetRoutingDecisionHistory(query), "@bindings/cursor/internal/bridge/proxyservice.js", "GetRoutingDecisionHistory", [query]));
}

export function getAgentRuns(query) {
  return withApiLogging("GetAgentRuns", undefined, () => desktopOrMockRaw(() => GetAgentRuns(query), "@bindings/cursor/internal/bridge/proxyservice.js", "GetAgentRuns", [query]));
}

export function getAgentRun(runID) {
  return withApiLogging("GetAgentRun", undefined, () => desktopOrMockRaw(() => GetAgentRun(runID), "@bindings/cursor/internal/bridge/proxyservice.js", "GetAgentRun", [runID]));
}

export function cancelAgentRun(runID) {
  return withApiLogging("CancelAgentRun", undefined, () => desktopOrMockRaw(() => CancelAgentRun(runID), "@bindings/cursor/internal/bridge/proxyservice.js", "CancelAgentRun", [runID]));
}

export function prepareAgentRunRetry(runID) {
  return withApiLogging("PrepareAgentRunRetry", undefined, () => desktopOrMockRaw(() => PrepareAgentRunRetry(runID), "@bindings/cursor/internal/bridge/proxyservice.js", "PrepareAgentRunRetry", [runID]));
}

export function executeAgentRunRetry(confirmationToken) {
  return withApiLogging("ExecuteAgentRunRetry", undefined, () => desktopOrMockRaw(() => ExecuteAgentRunRetry(confirmationToken), "@bindings/cursor/internal/bridge/proxyservice.js", "ExecuteAgentRunRetry", [confirmationToken]));
}

export function exportSanitizedAgentRunReport(runID) {
  return withApiLogging("ExportSanitizedAgentRunReport", undefined, () => desktopOrMockRaw(() => ExportSanitizedAgentRunReport(runID), "@bindings/cursor/internal/bridge/proxyservice.js", "ExportSanitizedAgentRunReport", [runID]));
}

export function listConfigProfiles() {
  return withApiLogging("ListConfigProfiles", undefined, () => desktopOrMockRaw(() => ListConfigProfiles(), "@bindings/cursor/internal/bridge/proxyservice.js", "ListConfigProfiles"));
}

export function saveCurrentConfigProfile(request) {
  return withApiLogging("SaveCurrentConfigProfile", { name: request?.name }, () => desktopOrMockRaw(() => SaveCurrentConfigProfile(request), "@bindings/cursor/internal/bridge/proxyservice.js", "SaveCurrentConfigProfile", [request]));
}

export function deleteConfigProfile(profileID) {
  return withApiLogging("DeleteConfigProfile", undefined, () => desktopOrMockRaw(() => DeleteConfigProfile(profileID), "@bindings/cursor/internal/bridge/proxyservice.js", "DeleteConfigProfile", [profileID]));
}

export function previewConfigProfile(profileID) {
  return withApiLogging("PreviewConfigProfile", undefined, () => desktopOrMockRaw(() => PreviewConfigProfile(profileID), "@bindings/cursor/internal/bridge/proxyservice.js", "PreviewConfigProfile", [profileID]));
}

export function prepareConfigProfileApply(profileID) {
  return withApiLogging("PrepareConfigProfileApply", undefined, () => desktopOrMockRaw(() => PrepareConfigProfileApply(profileID), "@bindings/cursor/internal/bridge/proxyservice.js", "PrepareConfigProfileApply", [profileID]));
}

export function executeConfigProfileApply(confirmationToken) {
  return withApiLogging("ExecuteConfigProfileApply", undefined, () => desktopOrMockRaw(() => ExecuteConfigProfileApply(confirmationToken), "@bindings/cursor/internal/bridge/proxyservice.js", "ExecuteConfigProfileApply", [confirmationToken]));
}

export function exportConfigProfile(profileID) {
  return withApiLogging("ExportConfigProfile", undefined, () => desktopOrMockRaw(() => ExportConfigProfile(profileID), "@bindings/cursor/internal/bridge/proxyservice.js", "ExportConfigProfile", [profileID]));
}

export function importConfigProfile(content) {
  return withApiLogging("ImportConfigProfile", undefined, () => desktopOrMockRaw(() => ImportConfigProfile(content), "@bindings/cursor/internal/bridge/proxyservice.js", "ImportConfigProfile", [content]));
}

export function getProxyState() {
  return withApiLogging("GetState", undefined, () => desktopOrMockRaw(() => GetState(), "@bindings/cursor/internal/bridge/proxyservice.js", "GetState"));
}

export function getMirrorCaptureStatus() {
  return withApiLogging("GetMirrorCaptureStatus", undefined, () => desktopOrMockRaw(() => GetMirrorCaptureStatus(), "@bindings/cursor/internal/bridge/proxyservice.js", "GetMirrorCaptureStatus"));
}

const BROWSER_TERMINAL_ENVIRONMENT = {
  platform: "browser-preview",
  shellPath: "/bin/zsh",
  shellName: "zsh",
  shellVersion: "",
  pythonPath: "/usr/bin/python3",
  pythonVersion: "Python 3",
  upgradeRecommended: false,
  upgradeMessage: "",
  configurationNotice: "浏览器预览模式：使用模拟环境。",
};

export function getTerminalEnvironmentStatus() {
  return withApiLogging("GetTerminalEnvironmentStatus", undefined, () => desktopOrMockRaw(BROWSER_TERMINAL_ENVIRONMENT, "@bindings/cursor/internal/bridge/proxyservice.js", "GetTerminalEnvironmentStatus"));
}

export function applyTerminalEnvironment() {
  return withApiLogging("ApplyTerminalEnvironment", undefined, () => desktopOrMockRaw(BROWSER_TERMINAL_ENVIRONMENT, "@bindings/cursor/internal/bridge/proxyservice.js", "ApplyTerminalEnvironment"));
}

// 通过 winget 异步安装 PowerShell 7 / Python 3；立即返回，进度走事件。
export function installTerminalDependency(target) {
  return withApiLogging("InstallTerminalDependency", undefined, () => desktopOrMockRaw(undefined, "@bindings/cursor/internal/bridge/proxyservice.js", "InstallTerminalDependency", [target]));
}

// 安装进度事件名（与后端 terminalenv.EventInstallProgress 一致）。
export const TERMINAL_INSTALL_PROGRESS_EVENT = "terminalenv:install-progress";

// 余额同步完成事件名（与后端 Wails 事件 provider-balances-synced 一致）。
export const PROVIDER_BALANCES_SYNCED_EVENT = "provider-balances-synced";

export function getHomeMetricsSummary() {
  return withApiLogging("GetHomeMetricsSummary", undefined, () => desktopOrMockRaw(browserPreviewMockMetrics(), "@bindings/cursor/internal/bridge/metricsservice.js", "GetHomeMetricsSummary"));
}

export function resetUsageMetrics() {
  return withApiLogging("ResetUsageMetrics", undefined, () => desktopOrMockRaw(undefined, "@bindings/cursor/internal/bridge/metricsservice.js", "ResetUsageMetrics"));
}

export function getAdRuntime() {
  return desktopOrMock({ available: false, slots: [], window: {} }, "@bindings/cursor/internal/bridge/adservice.js", "GetAdRuntime");
}

export function openAdExternalURL(url) {
  return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/adservice.js", "OpenExternalURL", [url]);
}

export function startProxyService() {
  return withApiLogging("StartProxy", undefined, () => desktopOrMockRaw(() => StartProxy(), "@bindings/cursor/internal/bridge/proxyservice.js", "StartProxy"));
}

export function stopProxyService() {
  return withApiLogging("StopProxy", undefined, () => desktopOrMockRaw(() => StopProxy(), "@bindings/cursor/internal/bridge/proxyservice.js", "StopProxy"));
}

export function openLogsDirectory() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "OpenHistoryWindow"); }
export function openMirrorCaptureDirectory() { return desktopOrMock(() => OpenMirrorCaptureDirectory(), "@bindings/cursor/internal/bridge/windowservice.js", "OpenMirrorCaptureDirectory"); }
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
export function openStatsOverlayWindow(x, y, hasPosition = false) {
  return desktopOrMock([x || 0, y || 0, Boolean(hasPosition)], "@bindings/cursor/internal/bridge/windowservice.js", "OpenStatsOverlayWindow", [x || 0, y || 0, Boolean(hasPosition)]);
}
export function updateStatsOverlayWindow(style, alwaysOnTop) {
  return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "UpdateStatsOverlayWindow", [style, alwaysOnTop]);
}
export function updateStatsOverlayLayout(layout) {
  return updateStatsOverlayWindow(layout, false);
}
export function setStatsOverlayAlwaysOnTop(alwaysOnTop) {
  return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "SetStatsOverlayAlwaysOnTop", [alwaysOnTop]);
}
export function closeStatsOverlayWindow() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "CloseStatsOverlayWindow"); }
export function setMainWindowCloseAction(action) {
  return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "SetMainWindowCloseAction", [action]);
}
export function closeApplication() {
  return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "CloseApplication");
}
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
  if (isBrowserPreview) {
    // 浏览器预览：优先使用测试计划注入的测试结果，保证测试结果展示/失败分支可回归。
    const plan = readBrowserPreviewTestPlan();
    const override = plan?.testResult;
    const value = typeof override === "function" ? override(adapter) : override;
    if (value && typeof value === "object") {
      // 注入结果归属当前被测模型（adapterID 优先采用注入值，缺省跟随 adapter.id）
      return Promise.resolve({ ...value, adapterID: value.adapterID || adapter?.id || "" });
    }
    return Promise.resolve({ status: "success", adapterID: adapter?.id || "preview", summaryText: "浏览器预览模式：未发起请求" });
  }
  return withApiLogging("TestModelAdapter", adapter, () => invoke("@bindings/cursor/internal/bridge/proxyservice.js", "TestModelAdapter", [adapter]), { timeoutMs: MODEL_TEST_TIMEOUT_MS });
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

export function fetchProviderEvents(startUnixMs = 0, endUnixMs = 0, model = "") {
  return desktopOrMock([], "@bindings/cursor/internal/bridge/metricsservice.js", "GetProviderEvents", [startUnixMs, endUnixMs, model]);
}

export function fetchRecentRequestMetricsCount() {
  return desktopOrMock(0, "@bindings/cursor/internal/bridge/metricsservice.js", "GetRecentRequestMetricsCount");
}

// 全量异常请求数（跨分页），用于「请求明细」页异常总数展示。
export function fetchRecentRequestMetricsAbnormalCount() {
  return desktopOrMock(0, "@bindings/cursor/internal/bridge/metricsservice.js", "GetRecentRequestMetricsAbnormalCount");
}

// 全量降级请求数（跨分页），用于「请求明细」页降级总数展示（与异常计数同口径）。
export function fetchRecentRequestMetricsDegradedCount() {
  return desktopOrMock(0, "@bindings/cursor/internal/bridge/metricsservice.js", "GetRecentRequestMetricsDegradedCount");
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

// 一键自动配对所有模型适配器的上下文窗口：目录命中仅下调（保留用户手动设置的更小窗口），目录未命中则探测 provider /models 回填。
// force=true 时无视 autoMatchContextWindow 开关强制执行（「一键诊断优化」手动触发用）。
export function autoMatchContextWindows(force = false) {
  if (isBrowserPreview) {
    return Promise.resolve({ enabled: false, switchEnabled: true, changed: false, total: 0, fromCatalog: 0, fromProbe: 0, unchanged: 0 });
  }
  return withApiLogging("AutoMatchContextWindows", [force], () => invoke("@bindings/cursor/internal/bridge/proxyservice.js", "AutoMatchContextWindows", [force]), { timeoutMs: AUTO_MATCH_TIMEOUT_MS });
}

export function diagnoseModelAdapters() {
  if (isBrowserPreview) return Promise.resolve({ total: 0, issues: [] });
  return withApiLogging("DiagnoseModelAdapters", undefined, () => invoke("@bindings/cursor/internal/bridge/proxyservice.js", "DiagnoseModelAdapters", []));
}

export function applyDiagnosticFixes(channelIDs) {
  if (isBrowserPreview) return Promise.resolve({ total: 0, issues: [] });
  return withApiLogging("ApplyDiagnosticFixes", channelIDs, () => invoke("@bindings/cursor/internal/bridge/proxyservice.js", "ApplyDiagnosticFixes", [channelIDs]));
}

// 查询中转站余额/额度；失败时后端返回结构化的 { supported:false, message } 结果。
export function queryProviderBalance(request) {
  if (isBrowserPreview) {
    // 浏览器预览：优先使用测试计划注入的余额响应，保证余额展示/过期分支可回归。
    const plan = readBrowserPreviewTestPlan();
    const override = plan?.balance;
    const value = typeof override === "function" ? override(request) : override;
    if (value && typeof value === "object") return Promise.resolve(value);
    return Promise.resolve({ supported: false, source: "", currency: "USD", total: null, used: null, remaining: null, message: "浏览器预览模式：未查询余额", transient: false });
  }
  return withApiLogging("QueryProviderBalance", request, () => invoke("@bindings/cursor/internal/bridge/proxyservice.js", "QueryProviderBalance", [request]));
}

export function getProviderDiagnostics() {
  return desktopOrMock(() => GetProviderDiagnostics(), "@bindings/cursor/internal/bridge/proxyservice.js", "GetProviderDiagnostics");
}

export function queryAllProviderBalances() {
  return desktopOrMock(() => QueryAllProviderBalances(), "@bindings/cursor/internal/bridge/proxyservice.js", "QueryAllProviderBalances");
}

export function syncProviderBalancesAfterAccountChange() {
  return desktopOrMock(() => SyncProviderBalancesAfterAccountChange(), "@bindings/cursor/internal/bridge/proxyservice.js", "SyncProviderBalancesAfterAccountChange");
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

export function detectCursorPath(manualPath = "") { return desktopOrMock("", "@bindings/cursor/internal/bridge/windowservice.js", "DetectCursorPath", [manualPath || ""]); }
export function launchCursor(workspaceDir, manualPath = "") { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "LaunchCursor", [workspaceDir || "", manualPath || ""]); }
export function restartCursor(workspaceDir, manualPath = "") { return desktopOrMock({}, "@bindings/cursor/internal/bridge/windowservice.js", "RestartCursor", [workspaceDir || "", manualPath || ""]); }
export function isCursorRunning() { return desktopOrMock(false, "@bindings/cursor/internal/bridge/windowservice.js", "IsCursorRunning"); }

// Skills & MCP 跨工具扫描：快照 / 重新扫描 / 保存开关配置。
// 注意：这些 binding 由 wails 工具链自动生成；新增方法需在下次 wails dev/build 时重新生成 bindings。
export function getSkillsMCPScanSnapshot(workspaceRoot = "") {
  return desktopOrMock(() => GetSkillsMCPScanSnapshot(workspaceRoot), "@bindings/cursor/internal/bridge/proxyservice.js", "GetSkillsMCPScanSnapshot", [workspaceRoot]);
}
export function refreshSkillsMCPScan(workspaceRoot = "") {
  return desktopOrMock(() => RefreshSkillsMCPScan(workspaceRoot), "@bindings/cursor/internal/bridge/proxyservice.js", "RefreshSkillsMCPScan", [workspaceRoot]);
}
export function saveSkillsMCPScanConfig(config) {
  return desktopOrMock(true, "@bindings/cursor/internal/bridge/proxyservice.js", "SaveSkillsMCPScanConfig", [config]);
}
export function readSkillFile(name, workspaceRoot = "") {
  return desktopOrMock({ name, fullPath: "", content: "" }, "@bindings/cursor/internal/bridge/proxyservice.js", "ReadSkillFile", [workspaceRoot, name]);
}
export function saveSkillFile(name, content, workspaceRoot = "") {
  return desktopOrMock(true, "@bindings/cursor/internal/bridge/proxyservice.js", "SaveSkillFile", [workspaceRoot, name, content]);
}
export function generateSkillSummary(kind, key, workspaceRoot = "") {
  return desktopOrMock("", "@bindings/cursor/internal/bridge/proxyservice.js", "GenerateSkillSummary", [workspaceRoot, kind, key]);
}
export function repairCACorruption() {
  return desktopOrMock({ repaired: true, backupPath: "", detail: "浏览器预览模式：模拟修复" }, "@bindings/cursor/internal/bridge/proxyservice.js", "RepairCACorruption");
}
export function getCARepairStatus() {
  return desktopOrMock({ repaired: false, repairedAt: "", detail: "" }, "@bindings/cursor/internal/bridge/proxyservice.js", "GetCARepairStatus");
}

export function offerDefenderExclusion() {
  return desktopOrMock({ added: false, alreadyExcluded: false, cancelled: false, error: "" }, "@bindings/cursor/internal/bridge/proxyservice.js", "OfferDefenderExclusion");
}

export function getDefenderExclusionState() {
  return desktopOrMock({ supported: false, defenderActive: false, alreadyExcluded: false, offered: false, path: "" }, "@bindings/cursor/internal/bridge/proxyservice.js", "GetDefenderExclusionState");
}

export function dismissDefenderExclusion() {
  return desktopOrMock(true, "@bindings/cursor/internal/bridge/proxyservice.js", "DismissDefenderExclusion");
}

export function repairProxySettings() {
  return withApiLogging("RepairProxySettings", undefined, () => desktopOrMockRaw(undefined, "@bindings/cursor/internal/bridge/proxyservice.js", "RepairProxySettings"));
}

export function getDelegationConfig() {
  return desktopOrMock(() => GetDelegationConfig(), "@bindings/cursor/internal/bridge/proxyservice.js", "GetDelegationConfig");
}
export function getRecentWorkspaceRoot() {
  return desktopOrMock(() => GetRecentWorkspaceRoot(), "@bindings/cursor/internal/bridge/proxyservice.js", "GetRecentWorkspaceRoot");
}

export function saveDelegationConfig(config) {
  return desktopOrMock(() => SaveDelegationConfig(config), "@bindings/cursor/internal/bridge/proxyservice.js", "SaveDelegationConfig", [config]);
}

export function getDelegationExecutorSnapshots() {
  return desktopOrMock(() => GetDelegationExecutorSnapshots(), "@bindings/cursor/internal/bridge/proxyservice.js", "GetDelegationExecutorSnapshots");
}

export function refreshDelegationExecutorProbes() {
  return desktopOrMock(() => RefreshDelegationExecutorProbes(), "@bindings/cursor/internal/bridge/proxyservice.js", "RefreshDelegationExecutorProbes");
}

export function installDelegationExecutor(id) {
  return desktopOrMock(() => InstallDelegationExecutor(id), "@bindings/cursor/internal/bridge/proxyservice.js", "InstallDelegationExecutor", [id]);
}
