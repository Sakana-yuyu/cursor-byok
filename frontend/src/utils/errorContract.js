const STATUS_CODES = new Map([
  [400, { code: "configuration_invalid", kind: "configuration", disposition: "blocked", message: "请求参数或配置不正确，请检查配置" }],
  [401, { code: "authentication_required", kind: "authentication", disposition: "blocked", message: "密钥无效或暂无可用额度，请检查供应商配置" }],
  [402, { code: "quota_exceeded", kind: "quota", disposition: "blocked", message: "当前供应商额度不足，请检查账户或切换供应商" }],
  [403, { code: "permission_denied", kind: "permission", disposition: "blocked", message: "当前密钥没有访问权限，请检查供应商配置" }],
  [404, { code: "resource_not_found", kind: "configuration", disposition: "blocked", message: "模型或接口地址不存在，请检查配置" }],
  [408, { code: "timeout", kind: "timeout", disposition: "retryable", message: "请求超时，正在准备恢复" }],
  [429, { code: "rate_limited", kind: "rate_limit", disposition: "retryable", message: "请求过于频繁，稍后会自动重试" }],
  [500, { code: "provider_unavailable", kind: "provider", disposition: "retryable", message: "供应商暂时不可用，正在准备恢复" }],
  [502, { code: "provider_unavailable", kind: "provider", disposition: "retryable", message: "供应商网关异常，正在准备恢复" }],
  [503, { code: "provider_unavailable", kind: "provider", disposition: "retryable", message: "供应商暂时不可用，正在准备恢复" }],
  [504, { code: "provider_unavailable", kind: "provider", disposition: "retryable", message: "供应商网关超时，正在准备恢复" }],
]);

export const MCP_WORKSPACE_TRUST_REQUIRED_CODE = "mcp_workspace_trust_required";

const DIAGNOSTIC_CODES = new Map([
  [MCP_WORKSPACE_TRUST_REQUIRED_CODE, {
    code: MCP_WORKSPACE_TRUST_REQUIRED_CODE,
    kind: "authorization",
    disposition: "user_action_required",
    message: "工作区 MCP 配置需要确认信任后才能连接",
  }],
]);

const SAFE_TRACE_ID = /^[A-Za-z0-9._:-]{1,128}$/;
const SENSITIVE_KEYS = /key|token|secret|password|authorization|cookie|credential|prompt|message|body|content|request|response|error|stack|cause/i;

function asText(value) {
  if (typeof value === "string") return value.trim();
  if (value instanceof Error) return value.message.trim();
  if (value && typeof value === "object") {
    return String(value.message || value.error || value.rawResponse || "").trim();
  }
  return value == null ? "" : String(value).trim();
}

function makeTraceId() {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `ui-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

export function extractStatusCode(error) {
  const explicit = Number(error?.statusCode || error?.status || error?.code);
  if (Number.isInteger(explicit) && explicit >= 100 && explicit < 600) return explicit;
  const match = asText(error).match(/(?:status|HTTP)[=:\s]+(\d{3})/i) || asText(error).match(/\b(\d{3})\b/);
  return match ? Number(match[1]) : 0;
}

function technicalMessage(error) {
  const text = asText(error);
  if (!text) return "unknown error";
  return text
    .replace(/(authorization\s*[=:]\s*)(?:bearer\s+)?[^\s,;&?#]+/gi, "$1[redacted]")
    .replace(/([?&](?:api[_-]?key|token|secret|password)=)[^&\s]*/gi, "$1[redacted]")
    .replace(/((?:api[_-]?key|token|secret|password)\s*[=:]\s*)[^\s,;&?#]+/gi, "$1[redacted]")
    .slice(0, 1000);
}

function safeTraceId(value) {
  const text = String(value || "").trim();
  return SAFE_TRACE_ID.test(text) ? text : makeTraceId();
}

export function normalizeClientError(error, { operation = "ui.operation", traceId = "" } = {}) {
  const text = asText(error);
  const lower = text.toLowerCase();
  const status = extractStatusCode(error);
  const structuredCode = typeof error?.code === "string" ? error.code.trim() : "";
  const messageCode = lower.match(/\bmcp_workspace_trust_required\b/)?.[0] || "";
  const diagnosticCode = DIAGNOSTIC_CODES.has(structuredCode) ? structuredCode : messageCode;
  const mapped = DIAGNOSTIC_CODES.get(diagnosticCode) || STATUS_CODES.get(status);
  let result = mapped;

  if (!result && (lower.includes("cancel") || lower.includes("aborted") || lower.includes("已取消"))) {
    result = { code: "canceled", kind: "canceled", disposition: "canceled", message: "操作已取消" };
  } else if (!result && (lower.includes("timeout") || lower.includes("deadline") || lower.includes("超时"))) {
    result = { code: "timeout", kind: "timeout", disposition: "retryable", message: "请求超时，正在准备恢复" };
  } else if (!result && (lower.includes("network") || lower.includes("connection refused") || lower.includes("no such host") || lower.includes("failed to fetch"))) {
    result = { code: "network_error", kind: "network", disposition: "retryable", message: "暂时无法连接服务，正在准备恢复" };
  }

  result ||= { code: "internal_error", kind: "internal", disposition: "fatal", message: "服务发生异常，请重试或导出诊断信息" };
  const structuredKind = typeof error?.kind === "string" ? error.kind.trim() : "";
  const structuredDisposition = typeof error?.disposition === "string" ? error.disposition.trim() : "";
  const structuredUserMessage = typeof error?.userMessage === "string" ? error.userMessage.trim() : "";
  const structuredTechnicalMessage = typeof error?.technicalMessage === "string"
    ? error.technicalMessage
    : technicalMessage(error);
  const retryAfter = Number(error?.retryAfterMs || error?.retryAfter || 0);
  return {
    operation,
    code: diagnosticCode || structuredCode || result.code,
    kind: structuredKind || result.kind,
    disposition: structuredDisposition || result.disposition,
    retryAfterMs: Number.isFinite(retryAfter) && retryAfter > 0 ? retryAfter : 0,
    userMessage: structuredUserMessage || result.message,
    technicalMessage: technicalMessage(structuredTechnicalMessage),
    traceId: safeTraceId(traceId || error?.traceId),
    statusCode: status,
    cause: error,
  };
}

export function safeErrorLogAttributes(error, options = {}) {
  const normalized = normalizeClientError(error, options);
  return {
    operation: normalized.operation,
    code: normalized.code,
    kind: normalized.kind,
    disposition: normalized.disposition,
    statusCode: normalized.statusCode,
    traceId: normalized.traceId,
  };
}

export function isRetryableError(error) {
  return normalizeClientError(error).disposition === "retryable";
}

export function isBlockedError(error) {
  return normalizeClientError(error).disposition === "blocked";
}

export function toUserError(error) {
  return normalizeClientError(error).userMessage;
}

export function summarizePayload(payload, depth = 0) {
  if (payload == null) return payload;
  if (Array.isArray(payload)) return { type: "array", length: payload.length };
  if (typeof payload !== "object") {
    return typeof payload === "string" ? `${payload.slice(0, 80)}${payload.length > 80 ? "…" : ""}` : payload;
  }
  if (depth >= 3) return { type: "object", keys: Object.keys(payload).slice(0, 20) };
  return Object.fromEntries(Object.entries(payload).map(([key, value]) => [
    key,
    SENSITIVE_KEYS.test(key) ? "[redacted]" : summarizePayloadValue(value, depth + 1),
  ]));
}

function summarizePayloadValue(value, depth) {
  if (Array.isArray(value)) return { type: "array", length: value.length };
  if (value && typeof value === "object") return summarizePayload(value, depth);
  if (typeof value === "string") return value.length > 160 ? `${value.slice(0, 160)}…` : value;
  return value;
}
