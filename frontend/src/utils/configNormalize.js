import { asArray, asBoolean, asPositiveInteger, asString } from "@/utils/valueCast";
import { dedupeModelAdapters } from "@/utils/modelAdapter";

const SUPPORTED_ROUTE_MODES = new Set(["local", "upstream"]);
export function normalizeRouteMode(value) {
  const normalized = asString(value).toLowerCase();
  return SUPPORTED_ROUTE_MODES.has(normalized) ? normalized : "local";
}

export function validateConfigPayload(payload) {
  const mode = payload?.routing?.mode;
  if (!SUPPORTED_ROUTE_MODES.has(mode)) {
    return "运行模式仅支持 local 或 upstream";
  }
  return "";
}

export function normalizeLocalResponseCache(source) {
  const raw = source && typeof source === "object" ? source : {};
  return {
    enabled: asBoolean(raw.enabled),
    ttlSeconds: asPositiveInteger(raw.ttlSeconds),
    maxEntries: asPositiveInteger(raw.maxEntries),
    persist: asBoolean(raw.persist, true),
  };
}

// normalizeGoal 归一化 goal 循环执行配置（与后端 GoalConfig 字段对齐）。
export function normalizeGoal(source) {
  const raw = source && typeof source === "object" ? source : {};
  return {
    enabled: asBoolean(raw.enabled),
    maxProviderPasses: asPositiveInteger(raw.maxProviderPasses),
    maxDurationSeconds: asPositiveInteger(raw.maxDurationSeconds),
    maxCostUsd: typeof raw.maxCostUsd === "number" && Number.isFinite(raw.maxCostUsd) ? raw.maxCostUsd : 0,
    selfCheckPasses: asPositiveInteger(raw.selfCheckPasses),
    verifyMaxRetries: asPositiveInteger(raw.verifyMaxRetries),
    errorMaxRetries: asPositiveInteger(raw.errorMaxRetries),
    progressInterval: asPositiveInteger(raw.progressInterval),
  };
}

export function normalizeDelegation(source) {
  const raw = source && typeof source === "object" ? source : {};
  const groups = asArray(raw.groups).map((item, index) => {
    const group = item && typeof item === "object" ? item : {};
    const modelIDs = [...new Set(asArray(group.modelIDs || group.modelIds).map((value) => asString(value)).filter(Boolean))];
    const defaultModelID = asString(group.defaultModelID || group.defaultModelId);
    return {
      id: asString(group.id) || `delegation-group-${index + 1}`,
      name: asString(group.name) || String(`委派模型组 ${index + 1}`),
      enabled: asBoolean(group.enabled, true),
      modelIDs,
      defaultModelID: modelIDs.includes(defaultModelID) ? defaultModelID : (modelIDs[0] || ""),
      executionMode: ["auto", "cursor", "local"].includes(asString(group.executionMode).toLowerCase())
        ? asString(group.executionMode).toLowerCase()
        : "auto",
      toolPermissions: group.toolPermissions && typeof group.toolPermissions === "object" ? { ...group.toolPermissions } : {},
    };
  });
  const maxConcurrency = asPositiveInteger(raw.maxConcurrency);
  const supervisionRaw = raw.supervision && typeof raw.supervision === "object" ? raw.supervision : {};
  const visionRaw = raw.visionDelegation && typeof raw.visionDelegation === "object" ? raw.visionDelegation : {};
  const subagentProfileRows = normalizeSubagentProfiles(raw.subagentProfiles);
  const positiveOrDefault = (value, fallback) => {
    const parsed = asPositiveInteger(value);
    return parsed > 0 ? parsed : fallback;
  };
  const visionMode = asString(visionRaw.mode).toLowerCase();
  const visionModelID = asString(visionRaw.visionModelID || visionRaw.visionModelId);
  return {
    enabled: asBoolean(raw.enabled, true),
    maxConcurrency: maxConcurrency > 0 ? maxConcurrency : 4,
    groups,
    supervision: {
      enabled: asBoolean(supervisionRaw.enabled),
      supervisorModelID: asString(supervisionRaw.supervisorModelID || supervisionRaw.supervisorModelId),
      reviewerModelID: asString(supervisionRaw.reviewerModelID || supervisionRaw.reviewerModelId),
      workerGroupID: asString(supervisionRaw.workerGroupID || supervisionRaw.workerGroupId),
      maxCorrections: positiveOrDefault(supervisionRaw.maxCorrections, 2),
      maxRetries: positiveOrDefault(supervisionRaw.maxRetries, 1),
      maxRounds: positiveOrDefault(supervisionRaw.maxRounds, 8),
      allowReassign: asBoolean(supervisionRaw.allowReassign),
      allowEscalate: asBoolean(supervisionRaw.allowEscalate),
      strictUnavailable: asBoolean(supervisionRaw.strictUnavailable),
    },
    visionDelegation: {
      enabled: visionModelID !== "" && asBoolean(visionRaw.enabled),
      visionModelID,
      mode: ["auto", "describe", "ocr"].includes(visionMode) ? visionMode : "auto",
    },
    subagentProfiles: subagentProfileRows,
  };
}

// normalizeSubagentProfiles 归一子代理角色覆盖（subagentType → 角色片段），丢弃空类型。
export function normalizeSubagentProfiles(input) {
  return asArray(input)
    .map((item) => ({
      subagentType: asString(item?.subagentType).trim(),
      promptFragment: asString(item?.promptFragment).trim(),
    }))
    .filter((item) => item.subagentType !== "");
}

export function normalizeDelegationForAdapters(source, adapters) {
  const delegation = normalizeDelegation(source);
  const availableModelIDs = new Set(
    asArray(adapters).map((adapter) => asString(adapter?.id)).filter(Boolean),
  );
  const groups = delegation.groups.map((group) => {
    const modelIDs = group.modelIDs.filter((modelID) => availableModelIDs.has(modelID));
    const defaultModelID = modelIDs.includes(group.defaultModelID)
      ? group.defaultModelID
      : (modelIDs[0] || "");
    return {
      ...group,
      enabled: modelIDs.length > 0 && group.enabled,
      modelIDs,
      defaultModelID,
    };
  });
  const availableGroupIDs = new Set(groups.map((group) => group.id));
  const supervision = { ...delegation.supervision };
  if (supervision.workerGroupID && !availableGroupIDs.has(supervision.workerGroupID)) {
    supervision.workerGroupID = "";
  }
  if (supervision.supervisorModelID && !availableModelIDs.has(supervision.supervisorModelID)) {
    supervision.supervisorModelID = "";
  }
  if (supervision.reviewerModelID && !availableModelIDs.has(supervision.reviewerModelID)) {
    supervision.reviewerModelID = "";
  }
  const visionDelegation = { ...delegation.visionDelegation };
  if (visionDelegation.visionModelID && !availableModelIDs.has(visionDelegation.visionModelID)) {
    visionDelegation.visionModelID = "";
    visionDelegation.enabled = false;
  }
  return {
    ...delegation,
    supervision,
    visionDelegation,
    groups,
  };
}

export function normalizeConfig(source) {
  const raw = source && typeof source === "object" ? source : {};
  const routing = raw.routing && typeof raw.routing === "object" ? raw.routing : {};
  const homeMetrics = raw.homeMetrics && typeof raw.homeMetrics === "object" ? raw.homeMetrics : {};
  return {
    log: asBoolean(raw.log),
    providerStreamIdleTimeout: asPositiveInteger(raw.providerStreamIdleTimeout),
    turnStaleTimeout: asPositiveInteger(raw.turnStaleTimeout),
    nativeDelegationProgressTimeout: asPositiveInteger(raw.nativeDelegationProgressTimeout),
    autoMatchContextWindow: asBoolean(raw.autoMatchContextWindow),
    backendListenAddr: asString(raw.configBackendListenAddr) || asString(raw.backendListenAddr),
    proxyListenAddr: asString(raw.configProxyListenAddr) || asString(raw.proxyListenAddr),
    modelAdapters: dedupeModelAdapters(raw.modelAdapters),
    routing: {
      mode: normalizeRouteMode(routing.mode ?? raw.routingMode),
    },
    homeMetrics: {
      includeCacheWriteInHitRate: asBoolean(homeMetrics.includeCacheWriteInHitRate),
    },
    // 本地响应缓存配置：保留在归一化白名单中，避免任何一次配置保存把它清空回默认值
    localResponseCache: normalizeLocalResponseCache(raw.localResponseCache),
    delegation: normalizeDelegation(raw.delegation),
    goal: normalizeGoal(raw.goal),
    lastAgentModelHash: asString(raw.lastAgentModelHash),
  };
}

export function asNullableRate(value) {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  return null;
}

export function normalizeHomeMetrics(source) {
  const raw = source && typeof source === "object" ? source : {};
  const turnsTotal = asPositiveInteger(raw.turnsTotal);
  const validTurnsTotal = asPositiveInteger(raw.validTurnsTotal);
  const invalidTurnsTotal = asPositiveInteger(raw.invalidTurnsTotal);
  return {
    turnsTotal,
    validTurnsTotal,
    invalidTurnsTotal,
    requestTokensTotal: asPositiveInteger(raw.requestTokensTotal),
    promptTokensTotal: asPositiveInteger(raw.promptTokensTotal),
    cacheReadTokens: asPositiveInteger(raw.cacheReadTokens),
    cacheWriteTokens: asPositiveInteger(raw.cacheWriteTokens),
    cacheHitRate: asNullableRate(raw.cacheHitRate),
  };
}
