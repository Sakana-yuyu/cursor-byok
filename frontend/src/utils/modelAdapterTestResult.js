const SUPPORTED_MODEL_ADAPTER_TEST_STATUSES = new Set(["idle", "running", "success", "error"]);

function asString(value) {
  return typeof value === "string" ? value.trim() : "";
}

function asNumber(value) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

function asBoolean(value) {
  return value === true || value === 1 || value === "1" || value === "true";
}

function normalizeModelAdapterTestStatus(value) {
  const text = asString(value).toLowerCase();
  return SUPPORTED_MODEL_ADAPTER_TEST_STATUSES.has(text) ? text : "idle";
}

export function formatDuration(value) {
  const durationMS = Math.max(0, Math.round(asNumber(value)));
  if (durationMS < 1000) {
    return durationMS + " ms";
  }
  return (durationMS / 1000).toFixed(1) + " s";
}

export function formatModelAdapterTestSummary(source) {
  const result = source && typeof source === "object" ? source : {};
  const status = normalizeModelAdapterTestStatus(result.status);
  if (status === "running") {
    return "测试中...";
  }
  if (status === "error") {
    return asString(result.error) || "模型测试失败";
  }
  if (status !== "success") {
    return "";
  }
  const totalTPS = Math.max(0, Math.round(asNumber(result.tokensPerSecond)));
  const visibleTPS = Math.max(0, Math.round(asNumber(result.visibleTokensPerSecond)));
  return `总生成 ${totalTPS} t/s | 正文 ${visibleTPS} t/s | 首响应 ${formatDuration(result.firstResponseMS)} | 首字 ${formatDuration(result.firstTextTokenMS)}`;
}

export function normalizeModelAdapterTestResult(source) {
  const raw = source && typeof source === "object" ? source : {};
  const status = normalizeModelAdapterTestStatus(raw.status);
  const normalized = {
    adapterID: asString(raw.adapterID),
    requestHash: asString(raw.requestHash),
    status,
    tokensPerSecond: Math.max(0, asNumber(raw.tokensPerSecond)),
    visibleTokensPerSecond: Math.max(0, asNumber(raw.visibleTokensPerSecond)),
    firstResponseMS: Math.max(0, Math.round(asNumber(raw.firstResponseMS))),
    firstTextTokenMS: Math.max(0, Math.round(asNumber(raw.firstTextTokenMS))),
    totalDurationMS: Math.max(0, Math.round(asNumber(raw.totalDurationMS))),
    outputTokens: Math.max(0, Math.round(asNumber(raw.outputTokens))),
    visibleOutputTokens: Math.max(0, Math.round(asNumber(raw.visibleOutputTokens))),
    reasoningTokens: Math.max(0, Math.round(asNumber(raw.reasoningTokens))),
    effectiveThinkingEffort: asString(raw.effectiveThinkingEffort).toLowerCase(),
    tokensEstimated: asBoolean(raw.tokensEstimated),
    summaryText: asString(raw.summaryText),
    error: asString(raw.error),
    rawResponse: asString(raw.rawResponse),
    testedAt: asString(raw.testedAt),
  };
  if (status === "success" || !normalized.summaryText) {
    normalized.summaryText = formatModelAdapterTestSummary(normalized);
  }
  if (status === "error" && !normalized.summaryText) {
    normalized.summaryText = normalized.error || "模型测试失败";
  }
  return normalized;
}
