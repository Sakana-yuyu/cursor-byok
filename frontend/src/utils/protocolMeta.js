// protocolMeta.js 承载 OpenAI/协议元数据常量与归一化纯函数，全部从
// appState.js 逐字搬移（零行为变化）。
import { asString } from "./valueCast";

export const ANTHROPIC_THINKING_EFFORT_DEFAULT = "xhigh";
export const OPENAI_ENDPOINT_RESPONSES = "/v1/responses";
export const OPENAI_ENDPOINT_CHAT_COMPLETIONS = "/v1/chat/completions";
export const OPENAI_ENDPOINT_CUSTOM = "/custom";
export const OPENAI_REQUEST_GROUP_RESPONSES = "responses";
export const OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS = "chat_completions";
export const OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS_COMPAT = "chat_completions_compat";
export const PROTOCOL_MODE_AUTO = "auto";
export const PROTOCOL_MODE_FIXED = "fixed";
export const PROTOCOL_GROUP_ANTHROPIC_MESSAGES = "messages";
export const PROTOCOL_GROUP_GEMINI_NATIVE = "gemini_native";

const SUPPORTED_OPENAI_ENDPOINTS = new Set([OPENAI_ENDPOINT_RESPONSES, OPENAI_ENDPOINT_CHAT_COMPLETIONS, OPENAI_ENDPOINT_CUSTOM]);
const SUPPORTED_OPENAI_REQUEST_GROUPS = new Set([
  OPENAI_REQUEST_GROUP_RESPONSES,
  OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS,
  OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS_COMPAT,
]);
const SUPPORTED_PROTOCOL_MODES = new Set([PROTOCOL_MODE_AUTO, PROTOCOL_MODE_FIXED]);

// normalizeOpenAIEndpoint 归一化 endpoint 路径。
// 支持三个预设值：/v1/responses、/v1/chat/completions、/custom（自定义路径）。
// 选 /custom 时，用户需在接口地址栏填写完整请求 URL。
export function normalizeOpenAIEndpoint(value) {
  const text = asString(value).toLowerCase();
  if (!text) {
    return OPENAI_ENDPOINT_RESPONSES;
  }
  return SUPPORTED_OPENAI_ENDPOINTS.has(text) ? text : "";
}

export function isValidOpenAIEndpoint(value) {
  return normalizeOpenAIEndpoint(value) !== "";
}

// normalizeOpenAIRequestGroup 归一化 OpenAI 请求分组/协议形态。
// 与后端 modelchannel.NormalizeOpenAIRequestGroup 行为一致：
// - 非 openai 类型返回空串；
// - group 为空时按 endpoint 推导默认值（responses 端点 → responses，其余 → chat_completions）；
// - group 命中三选一返回原值；其余返回空串（视为非法）。
export function normalizeOpenAIRequestGroup(type, endpoint, group) {
  if (asString(type).toLowerCase() !== "openai") {
    return "";
  }
  const normalized = asString(group).trim();
  if (!normalized) {
    return normalizeOpenAIEndpoint(endpoint) === OPENAI_ENDPOINT_RESPONSES
      ? OPENAI_REQUEST_GROUP_RESPONSES
      : OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS;
  }
  return SUPPORTED_OPENAI_REQUEST_GROUPS.has(normalized) ? normalized : "";
}

export function isValidOpenAIRequestGroup(type, endpoint, group) {
  return normalizeOpenAIRequestGroup(type, endpoint, group) !== "";
}

export function normalizeProtocolMode(value) {
  const normalized = asString(value).toLowerCase();
  if (!normalized) return PROTOCOL_MODE_AUTO;
  return SUPPORTED_PROTOCOL_MODES.has(normalized) ? normalized : "";
}

// inferProviderType 根据模型名推断最合适的 provider 协议族，避免把本应走原生协议的模型
// （claude、gemini）错误套用渠道级 openai 协议（导致 claude 缓存失效）。
// 规则：claude-* → anthropic、gemini-* → gemini、其余 → fallback（通常为渠道当前 type）。
// 与后端 modelchannel.InferProviderType 镜像，务必保持一致。
export function inferProviderType(modelID, fallback = "openai") {
  const model = asString(modelID).toLowerCase().trim();
  if (model.startsWith("claude")) return "anthropic";
  if (model.startsWith("gemini")) return "gemini";
  const fb = asString(fallback).toLowerCase().trim();
  if (fb === "anthropic" || fb === "gemini" || fb === "openai") return fb;
  return "openai";
}

export function classifyModelProtocol(type, modelID, baseURL, endpoint, configuredGroup = "") {
  const provider = asString(type).toLowerCase();
  if (provider === "anthropic") return PROTOCOL_GROUP_ANTHROPIC_MESSAGES;
  if (provider === "gemini") return PROTOCOL_GROUP_GEMINI_NATIVE;
  if (provider !== "openai") return "";
  const configured = asString(configuredGroup).toLowerCase();
  if (SUPPORTED_OPENAI_REQUEST_GROUPS.has(configured)) return configured;
  const normalizedEndpoint = normalizeOpenAIEndpoint(endpoint);
  const normalizedBaseURL = asString(baseURL).toLowerCase();
  const model = asString(modelID).toLowerCase();
  if (normalizedBaseURL.endsWith("/responses")) {
    return OPENAI_REQUEST_GROUP_RESPONSES;
  }
  if (normalizedBaseURL.endsWith("/chat/completions")) {
    return OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS;
  }
  if (normalizedBaseURL.includes("api.openai.com") || /^(gpt-|o1|o3|o4)/.test(model)) {
    return OPENAI_REQUEST_GROUP_RESPONSES;
  }
  if (model) return OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS;
  return normalizedEndpoint === OPENAI_ENDPOINT_RESPONSES
    ? OPENAI_REQUEST_GROUP_RESPONSES
    : OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS;
}

export function normalizeProtocolGroup(mode, type, modelID, baseURL, endpoint, configuredGroup) {
  const provider = asString(type).toLowerCase();
  const normalizedMode = normalizeProtocolMode(mode);
  const configured = asString(configuredGroup).toLowerCase();
  if (!normalizedMode) return "";
  if (provider === "anthropic") return configured && configured !== PROTOCOL_GROUP_ANTHROPIC_MESSAGES
    ? ""
    : PROTOCOL_GROUP_ANTHROPIC_MESSAGES;
  if (provider === "gemini") return configured && configured !== PROTOCOL_GROUP_GEMINI_NATIVE
    ? ""
    : PROTOCOL_GROUP_GEMINI_NATIVE;
  if (provider !== "openai") return "";
  if (normalizedMode === PROTOCOL_MODE_FIXED) {
    return SUPPORTED_OPENAI_REQUEST_GROUPS.has(configured) ? configured : "";
  }
  return classifyModelProtocol(provider, modelID, baseURL, endpoint, configured);
}