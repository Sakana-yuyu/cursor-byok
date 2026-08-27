import { computed, reactive, watchSyncEffect } from "vue";
import { runtimeEvents } from "@/services/runtimeAdapter";
import dayjs from "dayjs";
import { contextWindowTokensForModel } from "@/utils/modelContext";
import { adapterMatchesSupplierIdentity } from "@/utils/supplierGrouping";
// 基础类型转换与协议元数据已归位 utils/valueCast.js / utils/protocolMeta.js：
// 内部继续使用的符号在此 import，对外暴露的常量/函数通过下方 export from 转发。
import {
  asArray,
  asBoolean,
  asNumber,
  asPositiveInteger,
  asPositiveIntegerString,
  asString,
} from "@/utils/valueCast";
import {
  ANTHROPIC_THINKING_EFFORT_DEFAULT,
  normalizeOpenAIEndpoint,
  normalizeOpenAIRequestGroup,
  normalizeProtocolGroup,
  normalizeProtocolMode,
} from "@/utils/protocolMeta";
import {
  BALANCE_QUERY_HEADERS_DEFAULT_JSON,
  balanceQueryHeadersToJSON,
  hasBalanceQueryHeadersJSON,
  normalizePricing,
  parseBalanceQueryHeaders,
  validateAnthropicExtraParamsJSON,
  validateBalanceQueryHeadersJSON,
  validateHeadersJSON,
  validateJSONObject,
  validateOpenAIExtraParamsJSON,
} from "@/utils/configValidators";
import {
  ANTHROPIC_AUTH_MODE_AUTO,
  ANTHROPIC_AUTH_MODE_LEGACY_DUAL,
  ANTHROPIC_AUTH_MODE_X_API_KEY,
  ANTHROPIC_AUTH_MODE_BEARER,
  SUPPORTED_ANTHROPIC_AUTH_MODES,
  normalizeAnthropicAuthMode,
  buildModelAdapterIdentityKey,
  buildModelAdapterTestRequestHash,
  dedupeModelAdapters,
  mergeDuplicateModelAdapter,
  normalizeBaseURL,
  normalizeModelAdapter,
  normalizeModelAdapterTestResult,
  normalizeModelAdapterTestResults,
  normalizeModelAdapters,
  validateModelAdapters,
} from "@/utils/modelAdapter";
// config 归一化域已归位 utils/configNormalize.js：
// normalizeConfig / normalizeDelegation / normalizeRouteMode / validateConfigPayload 等
// 纯函数在此 import，保持既有调用方零改动。
import {
  normalizeConfig,
  normalizeDelegation,
  normalizeDelegationForAdapters,
  normalizeGoal,
  normalizeHomeMetrics,
  normalizeLocalResponseCache,
  normalizeRouteMode,
  normalizeComputerUseMode,
  validateConfigPayload,
} from "@/utils/configNormalize";
// Cursor 手动路径偏好已归位 utils/cursorLaunch.js；命名生成域已归位
// utils/displayName.js，此处 import / export 转发保持既有调用方零改动。
import { buildNextDisplayName } from "@/utils/displayName";
export { getCursorManualPath, setCursorManualPath } from "@/utils/cursorLaunch";
export {
  OPENAI_EXTRA_PARAMS_DEFAULT_JSON,
  EXTRA_PARAMS_DEFAULT_JSON,
  ANTHROPIC_AUTH_MODE_AUTO,
  ANTHROPIC_AUTH_MODE_LEGACY_DUAL,
  ANTHROPIC_AUTH_MODE_X_API_KEY,
  ANTHROPIC_AUTH_MODE_BEARER,
  SUPPORTED_ANTHROPIC_AUTH_MODES,
  normalizeAnthropicAuthMode,
  CUSTOM_HEADERS_DEFAULT_JSON,
  CREDENTIAL_SCOPE_ADAPTER_API_KEY,
  CREDENTIAL_SCOPE_CURSOR_ACCOUNT,
  MODEL_SOURCE_CURSOR_ACCOUNT,
  MODEL_SOURCE_THIRD_PARTY,
  buildModelAdapterTestRequestHash,
  createEmptyModelAdapter,
  dedupeModelAdapters,
  formatDuration,
  formatModelAdapterTestSummary,
  normalizeModelAdapter,
  normalizeModelAdapters,
  resolveBalanceProfileForAdapter,
  validateModelAdapters,
} from "@/utils/modelAdapter";
// toUserError 定义已归位 utils/errorHumanizer.js，此处转发保持既有调用方零改动。
import { toUserError } from "@/utils/errorHumanizer";
export { toUserError } from "@/utils/errorHumanizer";
export {
  ANTHROPIC_THINKING_EFFORT_DEFAULT,
  OPENAI_ENDPOINT_CHAT_COMPLETIONS,
  OPENAI_ENDPOINT_CUSTOM,
  OPENAI_ENDPOINT_RESPONSES,
  OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS,
  OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS_COMPAT,
  OPENAI_REQUEST_GROUP_RESPONSES,
  PROTOCOL_GROUP_ANTHROPIC_MESSAGES,
  PROTOCOL_GROUP_GEMINI_NATIVE,
  PROTOCOL_MODE_AUTO,
  PROTOCOL_MODE_FIXED,
  classifyModelProtocol,
  inferProviderType,
  normalizeProtocolMode,
} from "@/utils/protocolMeta";
export { BALANCE_QUERY_HEADERS_DEFAULT_JSON } from "@/utils/configValidators";
import {
  checkForUpdates,
  getAppVersion,
  getHomeMetricsSummary,
  getModelAdapterTestResults,
  installReadyUpdate,
  getProxyState,
  openConfigWindow as openConfig,
  loadUserConfig,
  openLogsDirectory,
  exportLogs,
  openModelConfig,
  openModelEditor,
  openMetricsDetailWindow as openMetricsDetail,
  openRequestMetricsWindow as openRequestMetrics,
  openStatsOverlayWindow,
  setStatsOverlayAlwaysOnTop,
  closeStatsOverlayWindow,
  setMainWindowCloseAction,
  closeApplication as closeApplicationNative,
  saveUserConfig,
  startProxyService,
  stopProxyService,
  testModelAdapter,
  repairProxySettings,
  restartCursor,
  isCursorRunning,
  repairCACorruption,
  offerDefenderExclusion,
  getDefenderExclusionState,
  dismissDefenderExclusion,
} from "@/services/clientApi";
import { getHistoryDebugUsage } from "@/services/runtimeControlApi";
import { createServiceViewState } from "@/state/serviceViewState";
import { createUpdateViewState } from "@/state/updateViewState";
const APP_STATE_STORAGE_KEY = "cursor-client:runtime-state:v2";
export const ROUTE_MODE_OPTIONS = [
  { label: "本地服务模式", value: "local" },
  { label: "官方上游模式", value: "upstream" },
];

export const COMPUTER_USE_MODE_OPTIONS = [
  { label: "桌面模式", value: "desktop" },
  { label: "浏览器模式", value: "browser" },
];
const PROXY_STATE_EVENT = "proxy:state";
const USER_CONFIG_CHANGED_EVENT = "user-config:changed";
const UPDATE_STATE_EVENT = "update:state";
const UPDATE_PROGRESS_EVENT = "update:progress";
const UPDATE_READY_EVENT = "update:ready";
const UPDATE_ERROR_EVENT = "update:error";
const MODEL_ADAPTER_TEST_UPDATED_EVENT = "model-adapter-test:updated";
export const DEBUG_LOG_WARNING_BYTES = 100 * 1024 * 1024;


function canUseLocalStorage() {
  return typeof window !== "undefined" && typeof window.localStorage !== "undefined";
}


function createEmptyHomeMetrics() {
  return {
    turnsTotal: 0,
    validTurnsTotal: 0,
    invalidTurnsTotal: 0,
    requestTokensTotal: 0,
    promptTokensTotal: 0,
    cacheReadTokens: 0,
    cacheWriteTokens: 0,
    cacheHitRate: null,
  };
}

function loadCachedState() {
  if (!canUseLocalStorage()) {
    return {};
  }

  try {
    const raw = window.localStorage.getItem(APP_STATE_STORAGE_KEY);
    if (!raw) {
      return {};
    }
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object") {
      return {};
    }
    // Older builds cached the full adapter list. Drop credential-bearing fields
    // before they can enter reactive state; the first persistence effect below
    // rewrites the same key using the safe whitelist.
    delete parsed.modelAdapters;
    delete parsed.serviceLastError;
    delete parsed.netProxyHttp;
    delete parsed.netProxyHttps;
    delete parsed.netProxyDescription;
    return parsed;
  } catch (_error) {
    return {};
  }
}


function applyHomeMetrics(raw) {
  appState.homeMetrics = normalizeHomeMetrics(raw);
  appState.homeMetricsError = "";
}

function buildConfigPayload(source = appState) {
  const normalized = normalizeConfig(source);
  const delegation = normalizeDelegationForAdapters(normalized.delegation, normalized.modelAdapters);
  return {
    log: normalized.log,
    providerStreamIdleTimeout: normalized.providerStreamIdleTimeout,
    turnStaleTimeout: normalized.turnStaleTimeout,
    nativeDelegationProgressTimeout: normalized.nativeDelegationProgressTimeout,
    autoMatchContextWindow: normalized.autoMatchContextWindow,
    backendListenAddr: normalized.backendListenAddr,
    proxyListenAddr: normalized.proxyListenAddr,
    // balanceQueryHeadersJSON 仅前端编辑态使用，落盘只保留 map 形态的 balanceQueryHeaders。
    modelAdapters: normalized.modelAdapters.map(({ balanceQueryHeadersJSON, ...adapter }) => adapter),
    autoDisableFailedModels: normalized.autoDisableFailedModels,
    routing: normalized.routing,
    homeMetrics: normalized.homeMetrics,
    billingQuery: normalized.billingQuery,
    localResponseCache: normalized.localResponseCache,
    mirrorCapture: normalized.mirrorCapture,
    delegation,
    goal: normalized.goal,
    computerUse: normalized.computerUse,
    lastAgentModelHash: normalized.lastAgentModelHash,
  };
}

function buildCachedConfigPayload() {
  const payload = buildConfigPayload();
  return {
    log: payload.log,
    providerStreamIdleTimeout: payload.providerStreamIdleTimeout,
    turnStaleTimeout: payload.turnStaleTimeout,
    nativeDelegationProgressTimeout: payload.nativeDelegationProgressTimeout,
    autoMatchContextWindow: payload.autoMatchContextWindow,
    backendListenAddr: payload.backendListenAddr,
    proxyListenAddr: payload.proxyListenAddr,
    routing: payload.routing,
    homeMetrics: payload.homeMetrics,
    billingQuery: payload.billingQuery,
    localResponseCache: payload.localResponseCache,
    mirrorCapture: payload.mirrorCapture,
    delegation: payload.delegation,
    goal: payload.goal,
    computerUse: payload.computerUse,
    lastAgentModelHash: payload.lastAgentModelHash,
  };
}

function applyConfigToState(config, { modelAdaptersOnly = false } = {}) {
  const normalized = normalizeConfig(config);
  if (modelAdaptersOnly) {
    appState.modelAdapters = normalized.modelAdapters;
    return normalized;
  }
  appState.modelAdapters = normalized.modelAdapters;
  appState.autoDisableFailedModels = normalized.autoDisableFailedModels;
  appState.configBackendListenAddr = normalized.backendListenAddr;
  appState.configProxyListenAddr = normalized.proxyListenAddr;
  appState.routingMode = normalized.routing.mode;
  appState.computerUseMode = normalized.computerUse.mode;
  appState.computerUseBrowserStartURL = normalized.computerUse.browserStartURL;
  appState.includeCacheWriteInHitRate = normalized.homeMetrics.includeCacheWriteInHitRate;
  appState.billingQuery = normalized.billingQuery;
  appState.localResponseCache = normalized.localResponseCache;
  appState.mirrorCaptureEnabled = normalized.mirrorCapture.enabled;
  appState.mirrorCaptureProtocolFidelity = normalized.mirrorCapture.protocolFidelity;
  appState.delegation = normalized.delegation;
  appState.goal = normalized.goal;
  appState.turnStaleTimeout = normalized.turnStaleTimeout;
  appState.nativeDelegationProgressTimeout = normalized.nativeDelegationProgressTimeout;
  appState.autoMatchContextWindow = normalized.autoMatchContextWindow;
  appState.debugLogEnabled = normalized.log;
  return normalized;
}

async function loadPersistedUserConfig() {
  return normalizeConfig(await loadUserConfig());
}

let configPersistTail = Promise.resolve();

async function persistConfigPayload(config, options = {}) {
  const pendingSave = configPersistTail.catch(() => {}).then(() => (
    persistConfigPayloadNow(config, options)
  ));
  configPersistTail = pendingSave.catch(() => {});
  return pendingSave;
}

async function persistConfigPayloadNow(config, { modelAdaptersOnly = false } = {}) {
  const latestConfig = await loadPersistedUserConfig();
  const requestedConfig = normalizeConfig(config);
  const mergedConfig = {
    ...latestConfig,
    ...requestedConfig,
    modelAdapters: modelAdaptersOnly
      ? requestedConfig.modelAdapters
      : normalizeModelAdapters(appState.modelAdapters),
    delegation: modelAdaptersOnly
      ? latestConfig.delegation
      : normalizeDelegation(appState.delegation),
  };
  const normalizedForValidation = normalizeConfig(mergedConfig);
  const prePayloadValidationError = validateModelAdapters(normalizedForValidation.modelAdapters);
  if (prePayloadValidationError) {
    return { ok: false, error: prePayloadValidationError };
  }
  const payload = buildConfigPayload(normalizedForValidation);
  const configValidationError = validateConfigPayload(payload);
  if (configValidationError) {
    return {
      ok: false,
      error: configValidationError,
    };
  }
  const validationError = validateModelAdapters(payload.modelAdapters);
  if (validationError) {
    return {
      ok: false,
      error: validationError,
    };
  }

  appState.configSaving = true;
  try {
    await saveUserConfig(payload);
    const persisted = await loadPersistedUserConfig();
    applyConfigToState(persisted, { modelAdaptersOnly });
    return {
      ok: true,
      error: "",
    };
  } catch (error) {
    return {
      ok: false,
      error: toUserError(error),
    };
  } finally {
    appState.configSaving = false;
  }
}

function applyProxyState(raw) {
  const state = raw && typeof raw === "object" ? raw : {};
  appState.backendRunning = asBoolean(state.backendRunning);
  appState.proxyRunning = asBoolean(state.proxyRunning ?? state.running);
  // serviceRunning 表示「服务完整可用」＝ backend 与代理同时在跑。此前它只等于
  // proxyRunning，于是 backend 挂掉、代理还在时首页仍显示绿灯「服务运行中」，
  // 而用户的请求其实已经失败了。两者状态不一致时用 servicePartiallyRunning
  // 表达，界面据此给出黄灯而不是假的绿灯。
  appState.serviceRunning = appState.backendRunning && appState.proxyRunning;
  appState.servicePartiallyRunning = appState.backendRunning !== appState.proxyRunning;
  appState.serviceLastError = asString(state.lastError);
  appState.backendListenAddr = asString(state.backendListenAddr);
  appState.proxyListenAddr = asString(state.proxyListenAddr || state.listenAddr);
  appState.serviceListenAddr = appState.proxyListenAddr;
  appState.cursorSettingsApplied = asBoolean(state.cursorSettingsApplied);
  appState.netProxySource = asString(state.netProxySource);
  appState.netProxyActive = asBoolean(state.netProxyActive);
  appState.netProxyUsingSystem = asBoolean(state.netProxyUsingSystem);
  appState.netProxyUsingEnv = asBoolean(state.netProxyUsingEnv);
  appState.netProxyHttp = asString(state.netProxyHttp);
  appState.netProxyHttps = asString(state.netProxyHttps);
  appState.netProxyPacIgnored = asBoolean(state.netProxyPacIgnored);
  appState.netProxyDescription = asString(state.netProxyDescription);
  // CA 材料不完整（cert/key 仅存其一）时本地代理停用，首页展示「一键修复」入口。
  appState.caIncomplete = asBoolean(state.caIncomplete);
  appState.caError = asString(state.caError);
}

function handleProxyStateEvent(event) {
  if (event?.data && typeof event.data === "object") {
    applyProxyState(event.data);
    return;
  }
  void syncServiceState().catch(() => {});
}

function handleUserConfigChangedEvent(event) {
  if (event?.data && typeof event.data === "object") {
    applyConfigToState(event.data);
    return;
  }
  void reloadUserConfig().catch(() => {});
}

function applyModelAdapterTestResults(source) {
  const next = {};
  for (const result of normalizeModelAdapterTestResults(source)) {
    next[result.adapterID] = result;
  }
  appState.modelAdapterTestResults = next;
  return next;
}

function handleModelAdapterTestUpdatedEvent(event) {
  if (event?.data) {
    applyModelAdapterTestResults(event.data);
    return;
  }
  void refreshModelAdapterTestResults().catch(() => {});
}

function normalizeUpdateState(value) {
  const text = asString(value).toLowerCase();
  if (["idle", "checking", "downloading", "ready", "installing", "error"].includes(text)) {
    return text;
  }
  return "idle";
}

function applyUpdateSnapshot(raw) {
  const data = raw && typeof raw === "object" ? raw : {};
  const nextState = normalizeUpdateState(data.state ?? appState.updateState);
  appState.updateState = nextState;

  const version = asString(data.version);
  if (version) {
    appState.updateVersion = version;
  } else if (nextState === "idle") {
    appState.updateVersion = "";
  }

  const releaseDate = asString(data.releaseDate);
  if (releaseDate) {
    appState.updateReleaseDate = releaseDate;
  } else if (nextState === "idle") {
    appState.updateReleaseDate = "";
  }

  if (typeof data.releaseNotes === "string") {
    appState.updateReleaseNotes = data.releaseNotes.replace(/\r\n/g, "\n");
  } else if (nextState === "idle") {
    appState.updateReleaseNotes = "";
  }

  if (typeof data.error === "string") {
    appState.updateError = data.error.trim();
  } else if (nextState !== "error") {
    appState.updateError = "";
  }

  if (typeof data.message === "string") {
    appState.updateMessage = data.message.trim();
  } else if (!data.prompt) {
    appState.updateMessage = "";
  }

  if (typeof data.downloaded === "number") {
    appState.updateProgressDownloaded = data.downloaded;
  } else if (nextState !== "downloading") {
    appState.updateProgressDownloaded = 0;
  }

  if (typeof data.total === "number") {
    appState.updateProgressTotal = data.total;
  } else if (nextState !== "downloading") {
    appState.updateProgressTotal = 0;
  }

  if (typeof data.percentage === "number") {
    appState.updateProgressPercent = Math.max(0, Math.min(100, data.percentage));
  } else if (nextState !== "downloading") {
    appState.updateProgressPercent = 0;
  }
}

function openUpdatePrompt(kind, payload = {}) {
  appState.updatePromptKind = asString(kind) || "idle";
  appState.updatePromptVisible = true;
  appState.updatePromptBusy = false;
  if (typeof payload.message === "string") {
    appState.updateMessage = payload.message.trim();
  }
  if (typeof payload.error === "string") {
    appState.updateError = payload.error.trim();
  }
}

function handleUpdateStateEvent(event) {
  const data = event?.data && typeof event.data === "object" ? event.data : {};
  applyUpdateSnapshot(data);
  if (asBoolean(data.prompt)) {
    openUpdatePrompt(asString(data.promptKind) || "idle", data);
  }
}

function handleUpdateProgressEvent(event) {
  const data = event?.data && typeof event.data === "object" ? event.data : {};
  applyUpdateSnapshot({
    ...data,
    state: "downloading",
  });
}

function handleUpdateReadyEvent(event) {
  const data = event?.data && typeof event.data === "object" ? event.data : {};
  applyUpdateSnapshot({
    ...data,
    state: "ready",
  });
  if (data.prompt !== false) {
    openUpdatePrompt("ready", data);
  }
}

function handleUpdateErrorEvent(event) {
  const data = event?.data && typeof event.data === "object" ? event.data : {};
  applyUpdateSnapshot({
    ...data,
    state: "error",
  });
  if (asBoolean(data.prompt)) {
    openUpdatePrompt("error", data);
  }
}

const cachedState = loadCachedState();
const cachedConfig = normalizeConfig(cachedState);

export const appState = reactive({
  appVersion: "",
  modelAdapters: cachedConfig.modelAdapters,
  autoDisableFailedModels: cachedConfig.autoDisableFailedModels,
  modelAdapterTestResults: {},
  configBackendListenAddr: cachedConfig.backendListenAddr,
  configProxyListenAddr: cachedConfig.proxyListenAddr,
  routingMode: cachedConfig.routing.mode,
  computerUseMode: cachedConfig.computerUse.mode,
  computerUseBrowserStartURL: cachedConfig.computerUse.browserStartURL,
  includeCacheWriteInHitRate: cachedConfig.homeMetrics.includeCacheWriteInHitRate,
  // 计费查询全局开关：控制是否向上游查询余额/计费接口（本地成本估算不受影响）。
  billingQuery: cachedConfig.billingQuery,
  localResponseCache: cachedConfig.localResponseCache,
  delegation: cachedConfig.delegation,
  goal: cachedConfig.goal,
  // 浮窗偏好是纯前端 UX 状态：localStorage 持久化 + 跨窗口 storage 事件广播。
  // 不进后端 config（后端 config 不含 overlay 字段）。初始给默认值，真实值由
  // getStatsOverlayPreferences() 在首次读取时从 localStorage 填充，避免模块求值顺序依赖。
  statsOverlayPreferences: { style: "card", alwaysOnTop: true, visible: false, snapCollapse: true, dockLocked: false, closeAction: "tray", opacity: 0.85, frosted: true, frostBlur: 18, accent: "mint", accentCustom: "" },
  turnStaleTimeout: cachedConfig.turnStaleTimeout,
  nativeDelegationProgressTimeout: cachedConfig.nativeDelegationProgressTimeout,
  autoMatchContextWindow: cachedConfig.autoMatchContextWindow,
  // 调试日志开关（log 配置）：控制 forwarder 是否把对话级 debug jsonl 写入磁盘。
  debugLogEnabled: asBoolean(cachedConfig.log),
  mirrorCaptureEnabled: asBoolean(cachedConfig.mirrorCapture?.enabled),
  // 协议保真记录：镜像记录的子开关，开启后完整协议帧原始字节落盘并产出协议时间线。
  mirrorCaptureProtocolFidelity: asBoolean(cachedConfig.mirrorCapture?.protocolFidelity),

  serviceRunning: asBoolean(cachedState.serviceRunning),
  backendRunning: asBoolean(cachedState.backendRunning),
  proxyRunning: asBoolean(cachedState.proxyRunning),
  // servicePartiallyRunning 表示 backend 与代理只起来了一个，属于不可用的中间态。
  servicePartiallyRunning: false,
  serviceBusy: false,
  serviceLastError: asString(cachedState.serviceLastError),
  // 调试日志总占用字节数；超阈值时首页显示清理提醒（0 表示未统计/无占用）。
  debugLogBytes: asNumber(cachedState.debugLogBytes),
  // 调试日志占用统计失败原因。非空表示占用数字不可信（可能过期），首页据此提示读取失败。
  // 不做持久化：进程重启后应重新统计，而不是沿用上次的错误。
  debugLogUsageError: "",
  serviceListenAddr: asString(cachedState.serviceListenAddr),
  backendListenAddr: asString(cachedState.backendListenAddr),
  proxyListenAddr: asString(cachedState.proxyListenAddr),
  cursorSettingsApplied: asBoolean(cachedState.cursorSettingsApplied),
  netProxySource: asString(cachedState.netProxySource),
  netProxyActive: asBoolean(cachedState.netProxyActive),
  netProxyUsingSystem: asBoolean(cachedState.netProxyUsingSystem),
  netProxyUsingEnv: asBoolean(cachedState.netProxyUsingEnv),
  netProxyHttp: asString(cachedState.netProxyHttp),
  netProxyHttps: asString(cachedState.netProxyHttps),
  netProxyPacIgnored: asBoolean(cachedState.netProxyPacIgnored),
  netProxyDescription: asString(cachedState.netProxyDescription),
  caIncomplete: asBoolean(cachedState.caIncomplete),
  caError: asString(cachedState.caError),

  configSaving: false,
  configReady: false,
  homeMetrics: createEmptyHomeMetrics(),
  homeMetricsLoading: false,
  homeMetricsError: "",

  updateState: "idle",
  updateVersion: "",
  updateReleaseDate: "",
  updateReleaseNotes: "",
  updateProgressDownloaded: 0,
  updateProgressTotal: 0,
  updateProgressPercent: 0,
  updateError: "",
  updateMessage: "",
  updatePromptVisible: false,
  updatePromptKind: "idle",
  updatePromptBusy: false,
});

// 持久化快照拆成两层 computed：normalizeConfig 这类深遍历只在「配置」字段真正变化时重跑，
// 运行状态字段（每 5–10s 被轮询刷新）命中缓存，不再反复触发全量序列化。
const cachedConfigPayloadComputed = computed(() => buildCachedConfigPayload());
const cachedStatusPayloadComputed = computed(() => ({
  serviceRunning: appState.serviceRunning,
  backendRunning: appState.backendRunning,
  proxyRunning: appState.proxyRunning,
  serviceListenAddr: appState.serviceListenAddr,
  configBackendListenAddr: appState.configBackendListenAddr,
  configProxyListenAddr: appState.configProxyListenAddr,
  backendListenAddr: appState.backendListenAddr,
  proxyListenAddr: appState.proxyListenAddr,
  cursorSettingsApplied: appState.cursorSettingsApplied,
  netProxySource: appState.netProxySource,
  netProxyActive: appState.netProxyActive,
  netProxyUsingSystem: appState.netProxyUsingSystem,
  netProxyUsingEnv: appState.netProxyUsingEnv,
  netProxyPacIgnored: appState.netProxyPacIgnored,
}));

let lastPersistedStateRaw = "";
watchSyncEffect((onCleanup) => {
  if (!canUseLocalStorage()) {
    return;
  }
  const configPart = cachedConfigPayloadComputed.value;
  const statusPart = cachedStatusPayloadComputed.value;
  const timer = setTimeout(() => {
    try {
      const raw = JSON.stringify({ ...statusPart, ...configPart });
      // 内容未变化时跳过同步 setItem（轮询期间两次快照往往完全一致）
      if (raw === lastPersistedStateRaw) {
        return;
      }
      window.localStorage.setItem(APP_STATE_STORAGE_KEY, raw);
      lastPersistedStateRaw = raw;
    } catch (_error) {
      // ignore local persistence failures
    }
  }, 250);
  onCleanup(() => clearTimeout(timer));
});

watchSyncEffect((onCleanup) => {
  if (typeof window === "undefined") {
    return;
  }
  const unsubscribe = runtimeEvents.On(PROXY_STATE_EVENT, handleProxyStateEvent);
  onCleanup(() => {
    unsubscribe();
  });
});

watchSyncEffect((onCleanup) => {
  if (typeof window === "undefined") {
    return;
  }
  const unsubscribe = runtimeEvents.On(USER_CONFIG_CHANGED_EVENT, handleUserConfigChangedEvent);
  onCleanup(() => {
    unsubscribe();
  });
});

watchSyncEffect((onCleanup) => {
  if (typeof window === "undefined") {
    return;
  }
  const unsubscribe = runtimeEvents.On(MODEL_ADAPTER_TEST_UPDATED_EVENT, handleModelAdapterTestUpdatedEvent);
  onCleanup(() => {
    unsubscribe();
  });
});

watchSyncEffect((onCleanup) => {
  if (typeof window === "undefined") {
    return;
  }
  const unsubscribe = runtimeEvents.On(UPDATE_STATE_EVENT, handleUpdateStateEvent);
  onCleanup(() => {
    unsubscribe();
  });
});

watchSyncEffect((onCleanup) => {
  if (typeof window === "undefined") {
    return;
  }
  const unsubscribe = runtimeEvents.On(UPDATE_PROGRESS_EVENT, handleUpdateProgressEvent);
  onCleanup(() => {
    unsubscribe();
  });
});

watchSyncEffect((onCleanup) => {
  if (typeof window === "undefined") {
    return;
  }
  const unsubscribe = runtimeEvents.On(UPDATE_READY_EVENT, handleUpdateReadyEvent);
  onCleanup(() => {
    unsubscribe();
  });
});

watchSyncEffect((onCleanup) => {
  if (typeof window === "undefined") {
    return;
  }
  const unsubscribe = runtimeEvents.On(UPDATE_ERROR_EVENT, handleUpdateErrorEvent);
  onCleanup(() => {
    unsubscribe();
  });
});

export const appViewState = createServiceViewState(appState);
export const updateViewState = createUpdateViewState(appState);

export function getModelAdapterTestResultByID(adapterID) {
  const id = asString(adapterID);
  if (!id) {
    return null;
  }
  return appState.modelAdapterTestResults[id] ?? null;
}

export function getModelAdapterTestResult(adapter) {
  const normalized = normalizeModelAdapter(adapter);
  if (normalized.id && appState.modelAdapterTestResults[normalized.id]) {
    return appState.modelAdapterTestResults[normalized.id];
  }
  const requestHash = buildModelAdapterTestRequestHash(normalized);
  return Object.values(appState.modelAdapterTestResults).find((result) => result.requestHash === requestHash) ?? null;
}
export function isModelAdapterTestResultStale(adapter, result) {
  if (!result || !result.requestHash) {
    return false;
  }
  return result.requestHash !== buildModelAdapterTestRequestHash(adapter);
}

export async function refreshModelAdapterTestResults() {
  const results = await getModelAdapterTestResults();
  applyModelAdapterTestResults(results);
  return Object.values(appState.modelAdapterTestResults);
}

export function startModelAdapterTest(adapter) {
  const normalized = normalizeModelAdapter(adapter);
  return testModelAdapter(normalized).then(async (rawResult) => {
    const result = normalizeModelAdapterTestResult(rawResult);
    if (result.adapterID) {
      const existing = appState.modelAdapterTestResults[result.adapterID];
      // "running" 状态由后端事件负责推送；Promise 回调只合并终态结果，
      // 避免去重路径返回的陈旧 "running" 覆盖事件已送达的 success/error。
      if (result.status !== "running" || !existing || existing.status === "running") {
        appState.modelAdapterTestResults = {
          ...appState.modelAdapterTestResults,
          [result.adapterID]: result,
        };
      }
      // 测试结果联动启用状态：失败自动停用（不再进入 Cursor 模型列表），
      // 成功自动恢复。批量测试并发时在持久化队列内读-改-写，避免互相覆盖。
      if (result.status === "success" || (result.status === "error" && appState.autoDisableFailedModels)) {
        await setModelAdapterDisabledFlag(result.adapterID, result.status === "error");
      }
    }
    return result;
  });
}

// 把「测试失败 → 停用 / 成功 → 恢复」落到配置。目标状态已达成时跳过保存。
// 排队进 configPersistTail 执行，保证读-改-写原子性。
export function setModelAdapterDisabledFlag(adapterID, disabled) {
  const pending = configPersistTail.catch(() => {}).then(async () => {
    const currentConfig = await loadPersistedUserConfig();
    let changed = false;
    const adapters = (currentConfig.modelAdapters || []).map((item) => {
      if (item.id !== adapterID) return item;
      if (Boolean(item.disabled) === disabled) return item;
      changed = true;
      return normalizeModelAdapter({ ...item, disabled });
    });
    if (!changed) {
      return { ok: true, error: "" };
    }
    appState.modelAdapters = adapters;
    return persistConfigPayload({
      ...currentConfig,
      modelAdapters: adapters,
    }, { modelAdaptersOnly: true });
  });
  configPersistTail = pending.catch(() => {});
  return pending;
}

export async function runModelAdapterTest(adapter) {
  return startModelAdapterTest(adapter);
}

export async function setAutoDisableFailedModels(enabled) {
  const currentConfig = await loadPersistedUserConfig();
  const nextValue = Boolean(enabled);
  if (currentConfig.autoDisableFailedModels === nextValue) {
    appState.autoDisableFailedModels = nextValue;
    return { ok: true, error: "" };
  }
  appState.autoDisableFailedModels = nextValue;
  return persistConfigPayload({
    ...currentConfig,
    autoDisableFailedModels: nextValue,
  });
}

export async function persistUserConfig() {
  const currentConfig = await loadPersistedUserConfig();
  return persistConfigPayload({
    ...currentConfig,
    modelAdapters: normalizeModelAdapters(appState.modelAdapters),
    routing: {
      mode: appState.routingMode,
    },
    autoMatchContextWindow: appState.autoMatchContextWindow,
    homeMetrics: {
      ...currentConfig.homeMetrics,
      includeCacheWriteInHitRate: appState.includeCacheWriteInHitRate,
    },
    delegation: appState.delegation,
  });
}

export async function saveIncludeCacheWriteInHitRate(value) {
  const currentConfig = await loadPersistedUserConfig();
  const previousValue = appState.includeCacheWriteInHitRate;
  const nextValue = asBoolean(value);
  appState.includeCacheWriteInHitRate = nextValue;
  const result = await persistConfigPayload({
    ...currentConfig,
    homeMetrics: {
      ...currentConfig.homeMetrics,
      includeCacheWriteInHitRate: nextValue,
    },
  });
  if (!result.ok) {
    appState.includeCacheWriteInHitRate = previousValue;
  }
  return result;
}

export async function saveLocalResponseCacheEnabled(enabled) {
  const currentConfig = await loadPersistedUserConfig();
  const nextValue = asBoolean(enabled);
  const previous = appState.localResponseCache;
  appState.localResponseCache = { ...previous, enabled: nextValue };
  const result = await persistConfigPayload({
    ...currentConfig,
    localResponseCache: {
      ...currentConfig.localResponseCache,
      enabled: nextValue,
    },
  });
  if (!result.ok) {
    appState.localResponseCache = previous;
  }
  return result;
}

export async function saveLocalResponseCacheSettings(partial) {
  const currentConfig = await loadPersistedUserConfig();
  const previous = appState.localResponseCache;
  const nextCache = normalizeLocalResponseCache({
    ...currentConfig.localResponseCache,
    ...previous,
    ...(partial && typeof partial === "object" ? partial : {}),
  });
  appState.localResponseCache = nextCache;
  const result = await persistConfigPayload({
    ...currentConfig,
    localResponseCache: {
      ...currentConfig.localResponseCache,
      ...nextCache,
    },
  });
  if (!result.ok) {
    appState.localResponseCache = previous;
  }
  return result;
}

// saveGoalSettings 增量保存 goal 配置字段（合并当前配置，失败回滚）。
export async function saveGoalSettings(partial) {
  const currentConfig = await loadPersistedUserConfig();
  const previous = appState.goal;
  const nextGoal = normalizeGoal({
    ...currentConfig.goal,
    ...previous,
    ...(partial && typeof partial === "object" ? partial : {}),
  });
  appState.goal = nextGoal;
  const result = await persistConfigPayload({
    ...currentConfig,
    goal: {
      ...currentConfig.goal,
      ...nextGoal,
    },
  });
  if (!result.ok) {
    appState.goal = previous;
  }
  return result;
}

// ─── 浮窗偏好（stats overlay）──────────────────────────────────────────────
// 偏好对象：{ style: "card"|"engine"|"orb", alwaysOnTop: boolean, visible: boolean }
// 仅前端 localStorage 持久化；浮窗是独立 webview 窗口，靠 storage 事件 + 自定义事件
// 跨窗口同步。后端 WindowService 已提供 open/update/close 三个 binding（window.go）。

const STATS_OVERLAY_PREFERENCES_KEY = "cursor-byok.stats-overlay.preferences";
const STATS_OVERLAY_CHANGED_EVENT = "stats-overlay-preferences-changed";
const STATS_OVERLAY_SHOW_REQUESTED_EVENT = "stats-overlay-show-requested";
const STATS_OVERLAY_STYLES = new Set(["card", "engine", "orb"]);
// 浮窗主题色：每项含主色（强调）与发光色（glow，用于辉光/光晕类效果）。
// custom 为自定义 RGB 色（存于 accentCustom），rainbow 为流动炫彩动画。
const STATS_OVERLAY_ACCENTS = new Set(["mint", "cyan", "amber", "violet", "rose", "blue", "custom", "rainbow"]);
let statsOverlayPreferenceSyncBound = false;
let statsOverlayShowRequestBound = false;

// 校验自定义主题色：接受 #RGB / #RRGGBB / rgb(...) 形式，返回标准 #RRGGBB。
function normalizeAccentCustom(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  const match = text.match(/^#?([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/);
  if (match) {
    const hex = match[1];
    return `#${hex.length === 3 ? hex.split("").map((ch) => ch + ch).join("") : hex}`.toLowerCase();
  }
  const rgb = text.match(/^rgb\(\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})\s*\)$/);
  if (rgb) {
    const toHex = (value) => Math.min(255, Math.max(0, Number(value))).toString(16).padStart(2, "0");
    return `#${toHex(rgb[1])}${toHex(rgb[2])}${toHex(rgb[3])}`;
  }
  return "";
}

function normalizeStatsOverlayPreferences(input) {
  const raw = input && typeof input === "object" ? input : {};
  const style = STATS_OVERLAY_STYLES.has(raw.style) ? raw.style : "card";
  const closeAction = raw.closeAction === "quit" ? "quit" : "tray";
  const normalizeCoordinate = (value) => typeof value === "number" && Number.isFinite(value) ? Math.round(value) : null;
  const normalizeOpacity = (value) => {
    const opacity = Number(value);
    if (!Number.isFinite(opacity)) return 0.85;
    return Math.min(1, Math.max(0.3, opacity));
  };
  // 磨砂模糊程度（px）：0 表示关闭磨砂；其余范围 2-30。
  const normalizeFrostBlur = (value) => {
    const blur = Number(value);
    if (!Number.isFinite(blur)) return raw.frosted === false ? 0 : 18;
    return Math.min(30, Math.max(0, blur));
  };
  return {
    style,
    alwaysOnTop: asBoolean(raw.alwaysOnTop ?? true),
    visible: asBoolean(raw.visible ?? false),
    x: normalizeCoordinate(raw.x),
    y: normalizeCoordinate(raw.y),
    snapCollapse: asBoolean(raw.snapCollapse ?? true),
    // dockLocked：锁定为收缩胶囊（悬停不展开）且窗口不可拖动。由设置开关与浮窗内锁按钮共同控制。
    dockLocked: asBoolean(raw.dockLocked ?? false),
    closeAction,
    // 外观：背景不透明度（0.3-1）、磨砂模糊程度（0-30，0=关闭）、主题色。
    opacity: normalizeOpacity(raw.opacity ?? 0.85),
    frosted: asBoolean(raw.frosted ?? true),
    frostBlur: normalizeFrostBlur(raw.frostBlur),
    accent: STATS_OVERLAY_ACCENTS.has(raw.accent) ? raw.accent : "mint",
    accentCustom: normalizeAccentCustom(raw.accentCustom),
  };
}

function loadStatsOverlayPreferences() {
  try {
    const stored = localStorage.getItem(STATS_OVERLAY_PREFERENCES_KEY);
    return normalizeStatsOverlayPreferences(stored ? JSON.parse(stored) : {});
  } catch {
    return normalizeStatsOverlayPreferences({});
  }
}

function syncStatsOverlayPreferencesFromStorage() {
  appState.statsOverlayPreferences = loadStatsOverlayPreferences();
}

function bindStatsOverlayPreferenceSync() {
  if (statsOverlayPreferenceSyncBound || typeof window === "undefined") return;
  window.addEventListener("storage", (event) => {
    if (event.key === STATS_OVERLAY_PREFERENCES_KEY) syncStatsOverlayPreferencesFromStorage();
  });
  window.addEventListener(STATS_OVERLAY_CHANGED_EVENT, syncStatsOverlayPreferencesFromStorage);
  statsOverlayPreferenceSyncBound = true;
}

bindStatsOverlayPreferenceSync();

function persistStatsOverlayPreferences(next) {
  const normalized = normalizeStatsOverlayPreferences(next);
  try {
    localStorage.setItem(STATS_OVERLAY_PREFERENCES_KEY, JSON.stringify(normalized));
  } catch { /* localStorage 不可用时仅内存生效 */ }
  appState.statsOverlayPreferences = normalized;
  // storage 事件只在其它窗口触发；同窗口内派发自定义事件补刀（StatsOverlay 已注册监听）。
  window.dispatchEvent(new Event(STATS_OVERLAY_CHANGED_EVENT));
  return normalized;
}

export function getStatsOverlayPreferences() {
  // 同步读取：StatsOverlay 的 onMounted 调用方未 await。
  const stored = loadStatsOverlayPreferences();
  appState.statsOverlayPreferences = stored;
  return stored;
}

export async function setStatsOverlayPreferences(partial) {
  const next = { ...loadStatsOverlayPreferences(), ...(partial || {}) };
  const persisted = persistStatsOverlayPreferences(next);
  // 样式尺寸与窗口层级都由浮窗自己的响应式偏好监听同步，避免多个窗口竞争原生状态。
  if (partial && "closeAction" in partial) {
    await setMainWindowCloseAction(persisted.closeAction);
  }
  // dockLocked 为纯前端 UX 状态（CSS 控制胶囊收缩与拖拽禁用），无需同步后端。
  return persisted;
}

export async function showStatsOverlay(position) {
  const current = loadStatsOverlayPreferences();
  const next = { ...current, visible: true };
  // 如果传入了位置参数，保存到偏好中
  if (position && typeof position === "object") {
    if (typeof position.x === "number") next.x = position.x;
    if (typeof position.y === "number") next.y = position.y;
  }
  const persisted = persistStatsOverlayPreferences(next);
  // 传入位置参数到后端
  const hasPosition = typeof persisted.x === "number" && typeof persisted.y === "number";
  await openStatsOverlayWindow(persisted.x || 0, persisted.y || 0, hasPosition);
  // 尺寸由浮窗挂载后的布局同步；置顶偏好由独立原生调用立即对齐。
  await setStatsOverlayAlwaysOnTop(persisted.alwaysOnTop);
  return loadStatsOverlayPreferences();
}

function bindStatsOverlayShowRequest() {
  if (statsOverlayShowRequestBound || typeof window === "undefined") return;
  window.addEventListener(STATS_OVERLAY_SHOW_REQUESTED_EVENT, () => {
    void showStatsOverlay();
  });
  statsOverlayShowRequestBound = true;
}

bindStatsOverlayShowRequest();

export async function hideStatsOverlay() {
  const persisted = persistStatsOverlayPreferences({ ...loadStatsOverlayPreferences(), visible: false });
  await closeStatsOverlayWindow();
  return persisted;
}

export async function closeApplication() {
  persistStatsOverlayPreferences({ ...loadStatsOverlayPreferences(), visible: false });
  await closeStatsOverlayWindow();
  await closeApplicationNative();
}

export async function openMetricsDetailWindow() {
  await openMetricsDetail();
}

export async function openRequestMetricsWindow() {
  await openRequestMetrics();
}

export async function saveRoutingMode(mode) {
  const currentConfig = await loadPersistedUserConfig();
  return persistConfigPayload({
    ...currentConfig,
    routing: {
      ...(currentConfig.routing ?? {}),
      mode: normalizeRouteMode(mode),
    },
  });
}

// saveComputerUse 增量保存 ComputerUse 执行模式配置（合并当前配置，失败回滚）。
export async function saveComputerUse(partial) {
  const currentConfig = await loadPersistedUserConfig();
  return persistConfigPayload({
    ...currentConfig,
    computerUse: {
      ...(currentConfig.computerUse ?? {}),
      ...partial,
    },
  });
}

export async function saveDebugLogEnabled(enabled) {
  const currentConfig = await loadPersistedUserConfig();
  return persistConfigPayload({
    ...currentConfig,
    log: !!enabled,
  });
}

// saveBillingQueryEnabled 增量保存计费查询全局开关（失败回滚由调用方按需处理，
// 与 saveIncludeCacheWriteInHitRate 一致在保存前乐观更新）。
export async function saveBillingQueryEnabled(enabled) {
  const currentConfig = await loadPersistedUserConfig();
  const previous = appState.billingQuery;
  const nextValue = asBoolean(enabled);
  appState.billingQuery = { ...(previous ?? {}), enabled: nextValue };
  const result = await persistConfigPayload({
    ...currentConfig,
    billingQuery: {
      ...(currentConfig.billingQuery ?? {}),
      enabled: nextValue,
    },
  });
  if (!result.ok) {
    appState.billingQuery = previous;
  }
  return result;
}

export async function saveMirrorCaptureEnabled(enabled) {
  const currentConfig = await loadPersistedUserConfig();
  const nextEnabled = !!enabled;
  return persistConfigPayload({
    ...currentConfig,
    mirrorCapture: {
      ...(currentConfig.mirrorCapture ?? {}),
      enabled: nextEnabled,
      // 关闭镜像记录时一并关闭保真子开关，避免留下一个不生效却仍显示为开启的状态。
      protocolFidelity: nextEnabled && !!currentConfig.mirrorCapture?.protocolFidelity,
    },
  });
}

export async function saveMirrorCaptureProtocolFidelity(enabled) {
  const currentConfig = await loadPersistedUserConfig();
  return persistConfigPayload({
    ...currentConfig,
    mirrorCapture: {
      ...(currentConfig.mirrorCapture ?? {}),
      protocolFidelity: !!enabled,
    },
  });
}

export async function reloadUserConfig(options = {}) {
  const config = await loadPersistedUserConfig();
  applyConfigToState(config, options);
  return config;
}

export async function saveModelAdapterAt(index, adapter) {
  const currentConfig = await loadPersistedUserConfig();
  const nextAdapters = dedupeModelAdapters(currentConfig.modelAdapters);
  const nextAdapter = normalizeModelAdapter(adapter);

  if (index >= 0 && index < nextAdapters.length) {
    nextAdapters.splice(index, 1, nextAdapter);
  } else {
    nextAdapters.push(nextAdapter);
  }

  const dedupedAdapters = dedupeModelAdapters(nextAdapters);
  const targetIdentity = buildModelAdapterIdentityKey(nextAdapter);
  const targetIndex = dedupedAdapters.findIndex(
    (item) => buildModelAdapterIdentityKey(item) === targetIdentity,
  );
  const result = await persistConfigPayload(
    {
      ...currentConfig,
      modelAdapters: dedupedAdapters,
    },
    { modelAdaptersOnly: true },
  );
  if (!result.ok) {
    return result;
  }
  return {
    ...result,
    index: targetIndex,
    adapter: appState.modelAdapters[targetIndex] ?? null,
  };
}

export async function saveModelAdaptersBatch(adapters) {
  const currentConfig = await loadPersistedUserConfig();
  const existingAdapters = normalizeModelAdapters(currentConfig.modelAdapters);
  const nextAdapters = existingAdapters.slice();
  const indexByIdentity = new Map(nextAdapters.map((adapter, index) => [buildModelAdapterIdentityKey(adapter), index]));
  let added = 0;
  let skipped = 0;
  let updated = 0;

  for (const source of Array.isArray(adapters) ? adapters : []) {
    const adapter = normalizeModelAdapter(source);
    const identity = buildModelAdapterIdentityKey(adapter);
    const existingIndex = indexByIdentity.get(identity);
    if (existingIndex == null) {
      indexByIdentity.set(identity, nextAdapters.length);
      nextAdapters.push(adapter);
      added += 1;
      continue;
    }

    const existing = nextAdapters[existingIndex];
    const merged = mergeDuplicateModelAdapter(existing, adapter);
    if (JSON.stringify(merged) !== JSON.stringify(existing)) {
      nextAdapters[existingIndex] = merged;
      updated += 1;
    } else {
      skipped += 1;
    }
  }

  if (added === 0 && updated === 0) {
    return { ok: true, error: "", added, skipped, updated, total: 0 };
  }
  const result = await persistConfigPayload(
    {
      ...currentConfig,
      modelAdapters: nextAdapters,
    },
    { modelAdaptersOnly: true },
  );
  return result.ok ? { ...result, added, skipped, updated, total: added + skipped + updated } : result;
}

/**
 * updateModelAdaptersBySupplier — 批量更新同一供应商下所有模型的共享（供应商级）配置。
 *
 * @param {object} supplierIdentity  — { mode, baseURL?, groupName? }，与路由 query 中的供应商标识一致。
 * @param {object} providerPatch     — 要覆盖的供应商级字段（见 PROVIDER_LEVEL_FIELDS）。
 * @returns {{ ok, error, updated, conflicts }}
 */

// 允许批量覆盖的供应商级字段；模型级字段不在此列表中，永远不会被覆盖。
const PROVIDER_LEVEL_FIELDS = new Set([
  "type", "supplierID", "baseURL", "apiKey", "groupName", "tooltipData",
  "protocolMode", "protocolGroup", "openAIEndpoint", "openAIRequestGroup",
  "customHeadersEnabled", "customHeadersJSON",
  "balanceQueryURL", "balanceQueryField", "balanceQueryHeadersJSON",
  "balanceProfile", "balanceAccessToken", "balanceUserID", "balanceCodingPlanProvider",
  "openAIExtraParamsEnabled", "openAIExtraParamsJSON",
  "anthropicExtraParamsEnabled", "anthropicExtraParamsJSON",
]);

export async function updateModelAdaptersBySupplier(supplierIdentity, providerPatch, options = {}) {
  const currentConfig = await loadPersistedUserConfig();
  const allAdapters = normalizeModelAdapters(currentConfig.modelAdapters);

  // 找出属于本供应商的 adapter 及其下标
  const targetIndices = [];
  for (let i = 0; i < allAdapters.length; i++) {
    if (adapterMatchesSupplierIdentity(allAdapters[i], supplierIdentity)) {
      targetIndices.push(i);
    }
  }
  if (targetIndices.length === 0) {
    return { ok: false, error: "未找到属于该供应商的模型配置", updated: 0, conflicts: [] };
  }

  // 只取允许覆盖的字段
  const patch = {};
  for (const key of Object.keys(providerPatch || {})) {
    if (PROVIDER_LEVEL_FIELDS.has(key)) {
      patch[key] = providerPatch[key];
    }
  }
  if (Object.keys(patch).length === 0) {
    return { ok: true, error: "", updated: 0, conflicts: [] };
  }

  // 应用 patch，保留模型级字段不变
  const nextAdapters = allAdapters.slice();
  for (const idx of targetIndices) {
    nextAdapters[idx] = normalizeModelAdapter({ ...allAdapters[idx], ...patch });
  }

  // 冲突检测：检查 patch 后的 adapter 是否与非目标 adapter 的 identity 碰撞
  const targetSet = new Set(targetIndices);
  const nonTargetIdentities = new Map();
  for (let i = 0; i < nextAdapters.length; i++) {
    if (!targetSet.has(i)) {
      nonTargetIdentities.set(buildModelAdapterIdentityKey(nextAdapters[i]), i);
    }
  }
  const conflicts = [];
  for (const idx of targetIndices) {
    const key = buildModelAdapterIdentityKey(nextAdapters[idx]);
    if (nonTargetIdentities.has(key)) {
      conflicts.push({
        targetIndex: idx,
        conflictIndex: nonTargetIdentities.get(key),
        modelID: nextAdapters[idx].modelID,
      });
    }
  }
  if (conflicts.length > 0 && !options.forceOverwrite) {
    return { ok: false, error: "存在重复模型冲突，请确认后重试", updated: 0, conflicts };
  }

  const result = await persistConfigPayload(
    { ...currentConfig, modelAdapters: nextAdapters },
    { modelAdaptersOnly: true },
  );
  return result.ok
    ? { ...result, updated: targetIndices.length, conflicts }
    : result;
}

// 余额查询可同步的字段（不含连接字段，保留各分组自身的 apiKey/groupName 等）。
const BALANCE_SYNC_FIELDS = new Set([
  "balanceQueryURL",
  "balanceQueryField",
  "balanceQueryHeadersJSON",
  "balanceProfile",
  "balanceAccessToken",
  "balanceUserID",
  "balanceCodingPlanProvider",
]);

/**
 * syncBalanceConfigToSameURL — 把余额查询配置同步到同一 baseURL 下的所有分组。
 *
 * 同一中转站下用户常有多个 key（各对应一个分组），但余额查询配置基本相同。
 * 本函数只覆盖余额字段，保留各分组自身的 apiKey / groupName / type / 连接配置，
 * 避免逐个分组手动配置余额查询。
 *
 * @param {string} baseURL              — 目标中转站 baseURL（按 normalizeBaseURL 归一化匹配）。
 * @param {object} balancePatch         — 余额查询配置草稿（只取 BALANCE_SYNC_FIELDS 中的字段）。
 * @returns {{ ok, error, updated }}
 */
export async function syncBalanceConfigToSameURL(baseURL, balancePatch) {
  const targetBase = normalizeBaseURL(baseURL);
  if (!targetBase) {
    return { ok: false, error: "缺少 baseURL，无法同步余额配置", updated: 0 };
  }
  const patch = {};
  for (const key of Object.keys(balancePatch || {})) {
    if (BALANCE_SYNC_FIELDS.has(key)) patch[key] = balancePatch[key];
  }
  if (Object.keys(patch).length === 0) {
    return { ok: true, error: "", updated: 0 };
  }

  const currentConfig = await loadPersistedUserConfig();
  const allAdapters = normalizeModelAdapters(currentConfig.modelAdapters);
  const targetIndices = [];
  for (let i = 0; i < allAdapters.length; i++) {
    if (normalizeBaseURL(allAdapters[i].baseURL) === targetBase) {
      targetIndices.push(i);
    }
  }
  if (targetIndices.length === 0) {
    return { ok: false, error: "未找到该 URL 下的模型配置", updated: 0 };
  }

  const nextAdapters = allAdapters.slice();
  for (const idx of targetIndices) {
    // 只覆盖余额字段；apiKey / groupName / 连接配置保持各 adapter 原值不变。
    nextAdapters[idx] = normalizeModelAdapter({ ...allAdapters[idx], ...patch });
  }

  const result = await persistConfigPayload(
    { ...currentConfig, modelAdapters: nextAdapters },
    { modelAdaptersOnly: true },
  );
  return result.ok
    ? { ...result, updated: targetIndices.length }
    : result;
}

export async function deleteModelAdapterAt(index) {
  const currentConfig = await loadPersistedUserConfig();
  const nextAdapters = normalizeModelAdapters(currentConfig.modelAdapters);

  if (index < 0 || index >= nextAdapters.length) {
    return {
      ok: false,
      error: "模型配置不存在，无法删除",
    };
  }

  nextAdapters.splice(index, 1);

  return persistConfigPayload(
    {
      ...currentConfig,
      modelAdapters: nextAdapters,
    },
    { modelAdaptersOnly: true },
  );
}

// 原子批量删除：一次性过滤掉目标渠道并只保存一次，避免逐个删除时多次落盘、
// 中途失败留下半删状态。targets 为待删除的适配器对象列表。
export async function deleteModelAdaptersBatch(targets) {
  const list = Array.isArray(targets) ? targets : [];
  if (list.length === 0) return { ok: true, error: "", removed: 0 };
  const currentConfig = await loadPersistedUserConfig();
  const existingAdapters = normalizeModelAdapters(currentConfig.modelAdapters);
  const removeKeys = new Set(
    list.map((adapter) => buildModelAdapterIdentityKey(normalizeModelAdapter(adapter))),
  );
  const nextAdapters = existingAdapters.filter(
    (adapter) => !removeKeys.has(buildModelAdapterIdentityKey(adapter)),
  );
  const removed = existingAdapters.length - nextAdapters.length;
  if (removed === 0) return { ok: true, error: "", removed: 0 };
  const result = await persistConfigPayload(
    {
      ...currentConfig,
      modelAdapters: nextAdapters,
    },
    { modelAdaptersOnly: true },
  );
  return result.ok ? { ...result, removed } : result;
}
/**
 * 按供应商身份删除模型。
 * - 旧签名：deleteModelAdaptersBySupplier(baseURL, groupName) → 等同 legacy（baseURL+分组名）
 * - 新签名：deleteModelAdaptersBySupplier({ mode, baseURL?, groupName? })
 * mode: 'name' | 'connection' | 'legacy'
 */
export async function deleteModelAdaptersBySupplier(baseURLOrIdentity, groupName) {
  const currentConfig = await loadPersistedUserConfig();
  let identity;
  if (baseURLOrIdentity && typeof baseURLOrIdentity === "object" && !Array.isArray(baseURLOrIdentity)) {
    identity = {
      mode: asString(baseURLOrIdentity.mode || "legacy").trim() || "legacy",
      source: baseURLOrIdentity.source,
      baseURL: baseURLOrIdentity.baseURL,
      groupName: baseURLOrIdentity.groupName,
    };
  } else {
    identity = {
      mode: "legacy",
      source: "third_party",
      baseURL: baseURLOrIdentity,
      groupName,
    };
  }

  const remaining = normalizeModelAdapters(currentConfig.modelAdapters).filter(
    (adapter) => !adapterMatchesSupplierIdentity(adapter, identity),
  );
  return persistConfigPayload(
    {
      ...currentConfig,
      modelAdapters: remaining,
    },
    { modelAdaptersOnly: true },
  );
}
export async function syncServiceState() {
  const [state] = await Promise.all([getProxyState(), refreshDebugLogUsage()]);
  applyProxyState(state);
  return state;
}

// 轻量版状态同步：只拉代理运行状态（有 proxy:state 推送兜底），不含磁盘遍历的调试日志统计。
export async function syncProxyState() {
  const state = await getProxyState();
  applyProxyState(state);
  return state;
}

// refreshDebugLogUsage 刷新调试日志占用统计。
// 读取失败时保留上一次的有效值并单独记录错误，绝不把「读不到」当成「占用为 0」——
// 后者会让首页的清理提醒凭空消失，用户以为磁盘已经干净了。
export async function refreshDebugLogUsage() {
  try {
    appState.debugLogBytes = Math.max(0, Number(await getHistoryDebugUsage()) || 0);
    appState.debugLogUsageError = "";
  } catch (error) {
    appState.debugLogUsageError = toUserError(error);
  }
  return appState.debugLogBytes;
}

export async function syncHomeMetrics() {
  appState.homeMetricsLoading = true;
  try {
    const summary = await getHomeMetricsSummary();
    applyHomeMetrics(summary);
    return {
      ok: true,
      error: "",
    };
  } catch (error) {
    appState.homeMetricsError = toUserError(error);
    return {
      ok: false,
      error: appState.homeMetricsError,
    };
  } finally {
    appState.homeMetricsLoading = false;
  }
}

export async function startService() {
  if (appState.serviceBusy) {
    return { ok: false, error: "服务状态更新中，请稍后再试" };
  }
  appState.serviceBusy = true;
  try {
    const saved = await persistUserConfig();
    if (!saved.ok) {
      return saved;
    }
    const state = await startProxyService();
    applyProxyState(state);
    return { ok: true, error: "" };
  } catch (error) {
    await syncServiceState().catch(() => {});
    return { ok: false, error: toUserError(error) };
  } finally {
    appState.serviceBusy = false;
  }
}

export async function stopService() {
  if (appState.serviceBusy) {
    return { ok: false, error: "服务状态更新中，请稍后再试" };
  }
  appState.serviceBusy = true;
  try {
    const state = await stopProxyService();
    applyProxyState(state);
    return { ok: true, error: "" };
  } catch (error) {
    await syncServiceState().catch(() => {});
    return { ok: false, error: toUserError(error) };
  } finally {
    appState.serviceBusy = false;
  }
}

export async function toggleService() {
  // 半启动态（backend 与代理只起来一个）也走关闭，先回到干净的未启动状态，
  // 否则对着一个不可用的中间态点「启动服务」不会有任何改善。
  if (appState.serviceRunning || appState.servicePartiallyRunning) {
    return stopService();
  }
  return startService();
}

export async function openLocalLogsDirectory() {
  await openLogsDirectory();
}

export async function repairProxyAction() {
  try {
    const result = await repairProxySettings();
    return { ok: true, result: result || {}, error: "" };
  } catch (error) {
    return { ok: false, result: null, error: toUserError(error) };
  }
}

export async function repairCACorruptionAction() {
  try {
    const result = await repairCACorruption();
    return { ok: true, result: result || {}, error: "" };
  } catch (error) {
    return { ok: false, result: null, error: toUserError(error) };
  }
}

// offerDefenderExclusionAction 一键把应用目录加入 Windows Defender 排除列表（触发 UAC）。
// 返回 { ok, result: { added, alreadyExcluded, cancelled, error }, error }。
export async function offerDefenderExclusionAction() {
  try {
    const result = await offerDefenderExclusion();
    return { ok: true, result: result || {}, error: "" };
  } catch (error) {
    return { ok: false, result: null, error: toUserError(error) };
  }
}

// getDefenderExclusionStateAction 查询 Defender 排除项引导状态（供前端判断是否弹窗）。
export async function getDefenderExclusionStateAction() {
  try {
    const result = await getDefenderExclusionState();
    return { ok: true, result: result || {}, error: "" };
  } catch (error) {
    return { ok: false, result: null, error: toUserError(error) };
  }
}

// dismissDefenderExclusionAction 用户主动跳过杀软排除项引导（标记不再弹窗）。
export async function dismissDefenderExclusionAction() {
  try {
    await dismissDefenderExclusion();
    return { ok: true, error: "" };
  } catch (error) {
    return { ok: false, error: toUserError(error) };
  }
}

export async function restartCursorAction(manualPath = "") {
  try {
    const result = await restartCursor("", manualPath);
    return { ok: true, result: result || {}, error: "" };
  } catch (error) {
    return { ok: false, result: null, error: toUserError(error) };
  }
}

export async function isCursorRunningAction() {
  try {
    const running = await isCursorRunning();
    return { ok: true, running: Boolean(running), error: "" };
  } catch (error) {
    return { ok: false, running: false, error: toUserError(error) };
  }
}

export async function exportLogsAction() {
  try {
    const path = await exportLogs();
    return { ok: true, path: String(path || ""), error: "" };
  } catch (error) {
    return { ok: false, path: "", error: toUserError(error) };
  }
}

export async function openConfigWindow() {
  await openConfig();
}

export async function openModelConfigWindow() {
  await openModelConfig();
}

export async function openModelEditorWindow(index, adapter) {
  const adapterJSON = JSON.stringify(normalizeModelAdapter(adapter));
  await openModelEditor(index, adapterJSON);
}

export async function checkForAppUpdates() {
  await checkForUpdates();
}

export function dismissUpdatePrompt() {
  appState.updatePromptVisible = false;
  appState.updatePromptBusy = false;
}

export async function confirmUpdatePrompt() {
  if (appState.updatePromptKind !== "ready") {
    dismissUpdatePrompt();
    return;
  }
  if (appState.updatePromptBusy) {
    return;
  }
  appState.updatePromptBusy = true;
  try {
    await installReadyUpdate();
  } catch (error) {
    appState.updatePromptBusy = false;
    const message = toUserError(error);
    appState.updateError = message;
    openUpdatePrompt("error", { error: message });
  }
}

export async function bootstrapAppState() {
  try {
    await reloadUserConfig();
  } catch (_error) {
    // keep cached config if loading fails
  } finally {
    // Settings pages mount before bootstrap completes. Expose a single readiness
    // gate so category components never race their own config fetch against this load.
    appState.configReady = true;
  }
  // 首屏数据就绪后，其余互不依赖的初始化并行执行，缩短启动时间
  await Promise.all([
    refreshModelAdapterTestResults().catch(() => {}),
    getAppVersion()
      .then((version) => {
        appState.appVersion = version;
      })
      .catch(() => {
        appState.appVersion = "";
      }),
    syncServiceState().catch(() => {}),
    syncHomeMetrics().catch(() => {}),
  ]);

  // 根据用户偏好自动打开悬浮窗
  const overlayPrefs = getStatsOverlayPreferences();
  await setMainWindowCloseAction(overlayPrefs.closeAction).catch(() => {});
  if (overlayPrefs.visible) {
    // 延迟打开，避免阻塞主窗口启动
    setTimeout(() => {
      showStatsOverlay({ x: overlayPrefs.x, y: overlayPrefs.y }).catch(() => {});
    }, 500);
  }
}
