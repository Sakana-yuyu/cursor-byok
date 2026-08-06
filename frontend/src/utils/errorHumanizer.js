// errorHumanizer.js 把 provider/适配器返回的原始错误翻译成人类可读的中文提示。
//
// 适配器错误常形如：`anthropic adapter status=404 body={...}`，
// 直接展示对用户不友好。这里按 HTTP 状态码 + 关键词归纳成一句话，
// 同时保留原始错误供排查（在 UI 上折叠到 Tooltip / 原始返回）。

const STATUS_MESSAGES = {
  400: "请求被拒绝（可能是该模型不支持当前参数）",
  401: "密钥无效或无权限访问该模型",
  403: "密钥无效或无权限访问该模型",
  404: "该模型在此供应商不存在或未开通",
  408: "请求超时，供应商无响应",
  429: "触发限流，请稍后重试",
  500: "供应商服务异常，请稍后重试",
  502: "供应商网关异常，请稍后重试",
  503: "供应商暂时不可用，请稍后重试",
  504: "供应商网关超时，请稍后重试",
};

function rawText(error) {
  if (error == null) return "";
  if (typeof error === "string") return error.trim();
  if (typeof error === "object") {
    return String(error.message || error.error || error.rawResponse || "").trim();
  }
  return String(error).trim();
}

/** 从原始错误信息中提取上游 HTTP 状态码，无法解析时返回 0。 */
export function extractStatusCode(error) {
  const text = rawText(error);
  const match = text.match(/status[=:\s]+(\d{3})/i) || text.match(/\b(\d{3})\b/);
  if (!match) return 0;
  const code = Number(match[1]);
  return code >= 100 && code < 600 ? code : 0;
}

/**
 * 把原始错误翻译成友好中文。无法归类时回退到原始信息（截断）。
 * @param {unknown} error 原始错误（字符串 / Error / {message,error}）
 * @returns {string}
 */
export function humanizeProviderError(error) {
  const text = rawText(error);
  if (!text) return "请求失败";

  const lower = text.toLowerCase();
  if (lower.includes("timeout") || lower.includes("deadline") || lower.includes("超时")) {
    return "请求超时，供应商无响应";
  }
  if (lower.includes("context canceled") || lower.includes("已取消")) {
    return "请求已取消";
  }
  if (lower.includes("api key") || lower.includes("apikey") || lower.includes("unauthorized")) {
    return "密钥无效或无权限访问该模型";
  }
  if (lower.includes("no such host") || lower.includes("connection refused") || lower.includes("dial tcp")) {
    return "无法连接到接口地址，请检查网络或地址是否正确";
  }

  const status = extractStatusCode(text);
  if (status && STATUS_MESSAGES[status]) {
    return STATUS_MESSAGES[status];
  }
  if (status >= 500) return "供应商服务异常，请稍后重试";
  if (status >= 400) return "请求被拒绝，请检查模型与参数配置";

  // 无法归类：回退原始信息，避免丢失排查线索（限制长度）。
  return text.length > 160 ? `${text.slice(0, 160)}…` : text;
}

// ---- toUserError：由 appState.js 逐字归位（依赖 asString/extractErrorMessage 一并搬移）----

const GENERIC_SERVICE_ERROR = "服务错误";

function asString(value) {
  if (typeof value === "string") {
    return value.trim();
  }
  if (value instanceof String) {
    return value.toString().trim();
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  return "";
}

function extractErrorMessage(error) {
  if (typeof error === "string") {
    return error.trim();
  }
  if (error && typeof error === "object") {
    return asString(error.message) || asString(error.error);
  }
  return "";
}

export function toUserError(error) {
  const message = extractErrorMessage(error);
  return message || GENERIC_SERVICE_ERROR;
}
