import {
  GetState, LoadUserConfig, SaveUserConfig, StartProxy, StopProxy, TestModelAdapter,
  GetModelAdapterTestResults, FetchModelCatalog, GetPromptInjectionSettings,
  SavePromptInjectionSettings, RefreshPromptInjection, RefreshPromptInjectionCatalog,
} from "@bindings/cursor/internal/bridge/proxyservice.js";
import { GetAdRuntime, OpenExternalURL } from "@bindings/cursor/internal/bridge/adservice.js";
import { GetHomeMetricsSummary, GetRecentRequestMetrics } from "@bindings/cursor/internal/bridge/metricsservice.js";
import {
  CheckForUpdates, GetAppVersion, GetFooterAuthorInfo, InstallReadyUpdate,
  GetModelEditorContext, OpenConfigWindow, OpenFooterAuthorHome, OpenHistoryWindow,
  OpenModelConfigWindow, OpenModelEditorWindow,
} from "@bindings/cursor/internal/bridge/windowservice.js";
import { isBrowserPreview, browserPreviewMockMetrics, browserPreviewMockProxyState } from "@/services/runtimeAdapter";

const desktopMethods = {
  LoadUserConfig, SaveUserConfig, GetState, GetHomeMetricsSummary, GetAdRuntime, OpenExternalURL,
  StartProxy, StopProxy, OpenHistoryWindow, OpenConfigWindow, GetAppVersion, GetFooterAuthorInfo,
  CheckForUpdates, InstallReadyUpdate, OpenFooterAuthorHome, OpenModelConfigWindow, OpenModelEditorWindow,
  GetModelEditorContext, TestModelAdapter, GetModelAdapterTestResults, GetRecentRequestMetrics,
  FetchModelCatalog, GetPromptInjectionSettings, SavePromptInjectionSettings, RefreshPromptInjection,
  RefreshPromptInjectionCatalog,
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
export function openConfigWindow() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "OpenConfigWindow"); }
export function getAppVersion() { return desktopOrMock("Browser Preview", "@bindings/cursor/internal/bridge/windowservice.js", "GetAppVersion"); }
export function getFooterAuthorInfo() { return desktopOrMock(null, "@bindings/cursor/internal/bridge/windowservice.js", "GetFooterAuthorInfo"); }
export function checkForUpdates() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "CheckForUpdates"); }
export function installReadyUpdate() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "InstallReadyUpdate"); }
export function openFooterAuthorHome() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "OpenFooterAuthorHome"); }
export function openModelConfig() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/windowservice.js", "OpenModelConfigWindow"); }
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

export function fetchRecentRequestMetrics(limit = 200) {
  return desktopOrMock([], "@bindings/cursor/internal/bridge/metricsservice.js", "GetRecentRequestMetrics", [limit]);
}

export function fetchModelCatalog(request) {
  if (isBrowserPreview) return Promise.resolve({ models: [] });
  return withApiLogging("FetchModelCatalog", request, () => invoke("@bindings/cursor/internal/bridge/proxyservice.js", "FetchModelCatalog", [request]));
}

export function getPromptInjectionSettings() {
  return desktopOrMock({ enabled: false, softwareChineseEnabled: false, mode: "replace", repo: "", ref: "", selectedTemplate: "" }, "@bindings/cursor/internal/bridge/proxyservice.js", "GetPromptInjectionSettings");
}
export function savePromptInjectionSettings(config) { return desktopOrMock(config, "@bindings/cursor/internal/bridge/proxyservice.js", "SavePromptInjectionSettings", [config]); }
export function refreshPromptInjection() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/proxyservice.js", "RefreshPromptInjection"); }
export function refreshPromptInjectionCatalog() { return desktopOrMock(undefined, "@bindings/cursor/internal/bridge/proxyservice.js", "RefreshPromptInjectionCatalog"); }