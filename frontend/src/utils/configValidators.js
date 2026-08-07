// configValidators.js 承载配置 JSON 校验与余额查询头解析纯函数，全部从
// appState.js 逐字搬移（零行为变化）。
import { asString } from "./valueCast";

export const BALANCE_QUERY_HEADERS_DEFAULT_JSON = `{
}`;

export function validateJSONObject(value, label) {
  const text = asString(value);
  if (!text) {
    return `${label}不能为空`;
  }
  try {
    const parsed = JSON.parse(text);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      return `${label}必须是 JSON 对象`;
    }
  } catch (_error) {
    return `${label}必须是合法 JSON 对象`;
  }
  return "";
}

export function validateHeadersJSON(value) {
  const objectError = validateJSONObject(value, "自定义请求头 JSON");
  if (objectError) {
    return objectError;
  }
  const parsed = JSON.parse(asString(value));
  for (const [key, item] of Object.entries(parsed)) {
    if (!asString(key)) {
      return "自定义请求头名称不能为空";
    }
    if (typeof item !== "string") {
      return `自定义请求头 ${key} 的值必须是字符串`;
    }
  }
  return "";
}

// parseBalanceQueryHeaders 把 map 或 JSON 字符串收成 map[string]string（对齐后端 BalanceQueryHeaders）。
export function parseBalanceQueryHeaders(value) {
  if (value && typeof value === "object" && !Array.isArray(value)) {
    const out = {};
    for (const [key, item] of Object.entries(value)) {
      const name = asString(key).trim();
      if (!name) continue;
      out[name] = typeof item === "string" ? item : String(item ?? "");
    }
    return out;
  }
  const text = asString(value).trim();
  if (!text) {
    return {};
  }
  try {
    const parsed = JSON.parse(text);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      return {};
    }
    const out = {};
    for (const [key, item] of Object.entries(parsed)) {
      const name = asString(key).trim();
      if (!name) continue;
      out[name] = typeof item === "string" ? item : String(item ?? "");
    }
    return out;
  } catch (_error) {
    return {};
  }
}

export function balanceQueryHeadersToJSON(headers) {
  if (!headers || typeof headers !== "object" || Array.isArray(headers) || Object.keys(headers).length === 0) {
    return BALANCE_QUERY_HEADERS_DEFAULT_JSON;
  }
  try {
    return JSON.stringify(headers, null, 2);
  } catch (_error) {
    return BALANCE_QUERY_HEADERS_DEFAULT_JSON;
  }
}

export function validateBalanceQueryHeadersJSON(value) {
  const text = asString(value).trim();
  if (!text) {
    return "";
  }
  const objectError = validateJSONObject(text, "余额查询请求头 JSON");
  if (objectError) {
    return objectError;
  }
  const parsed = JSON.parse(text);
  for (const [key, item] of Object.entries(parsed)) {
    if (!asString(key)) {
      return "余额查询请求头名称不能为空";
    }
    if (typeof item !== "string") {
      return `余额查询请求头 ${key} 的值必须是字符串`;
    }
  }
  return "";
}

export function hasBalanceQueryHeadersJSON(value) {
  return asString(value).trim() !== "";
}

export function validateOpenAIExtraParamsJSON(value) {
  return validateJSONObject(value, "额外参数 JSON");
}

export function validateAnthropicExtraParamsJSON(value) {
  return validateJSONObject(value, "Anthropic 额外参数 JSON");
}

export function normalizePricing(value) {
  if (!value || typeof value !== "object") return null;
  const number = (input) => {
    const parsed = Number(input);
    return Number.isFinite(parsed) && parsed >= 0 ? parsed : null;
  };
  const pricing = {
    input: number(value.input ?? value.inputPrice ?? value.input_price),
    output: number(value.output ?? value.outputPrice ?? value.output_price),
    cacheRead: number(value.cacheRead ?? value.cache_read ?? value.cache_read_price),
    cacheWrite: number(value.cacheWrite ?? value.cache_write ?? value.cache_write_price),
    currency: asString(value.currency) || "USD",
    known: Boolean(value.known),
    source: asString(value.source),
  };
  pricing.known = pricing.known || [pricing.input, pricing.output, pricing.cacheRead, pricing.cacheWrite].some((item) => item != null);
  return pricing.known ? pricing : null;
}