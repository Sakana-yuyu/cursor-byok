import { classifyModelProtocol } from "@/utils/protocolMeta";

export const OPENAI_ENDPOINT_RESPONSES = "/v1/responses";
export const PROTOCOL_GROUP_ANTHROPIC_MESSAGES = "messages";
export const PROTOCOL_GROUP_GEMINI_NATIVE = "gemini_native";

export function formatHost(value) {
  const text = String(value || "").trim();
  if (!text) return "-";
  try { return new URL(text).host || text; } catch { return text.replace(/^https?:\/\//, ""); }
}
export function maskSecret(value) {
  const text = String(value || "").trim();
  if (!text) return "-";
  if (text.length <= 8) return `${"*".repeat(Math.max(text.length - 2, 0))}${text.slice(-2)}`;
  return `${text.slice(0, 4)}****${text.slice(-4)}`;
}

export function formatOpenAIRequestGroup(group, endpoint) {
  const normalizedGroup = String(group || "").trim();
  if (normalizedGroup === "responses") return "Responses";
  if (normalizedGroup === "chat_completions") return "Chat Completions";
  if (normalizedGroup === "chat_completions_compat") return "Chat Completions / Compat";
  return String(endpoint || "").trim() === OPENAI_ENDPOINT_RESPONSES ? "Responses" : "Chat Completions";
}

export function defaultOpenAIRequestGroup(endpoint) {
  return String(endpoint || "").trim() === OPENAI_ENDPOINT_RESPONSES ? "responses" : "chat_completions";
}

export function resolvedOpenAIEndpoint(adapter) {
  return String(adapter?.openAIEndpoint || "").trim() || OPENAI_ENDPOINT_RESPONSES;
}

export function resolvedOpenAIRequestGroup(adapter) {
  return String(adapter?.protocolGroup || adapter?.openAIRequestGroup || "").trim() || defaultOpenAIRequestGroup(resolvedOpenAIEndpoint(adapter));
}

export function balanceSourceLabel(source) {
  if (source === "openai_billing") return "openai billing";
  if (source === "sub2api_usage") return "sub2api usage";
  if (source === "newapi") return "New API";
  if (source === "token_plan") return "Token Plan";
  if (source === "configured") return "自定义查询";
  if (source === "deepseek") return "DeepSeek";
  if (source === "stepfun") return "阶跃星辰";
  if (source === "siliconflow") return "SiliconFlow";
  if (source === "openrouter") return "OpenRouter";
  if (source === "novita") return "Novita";
  if (source === "moonshot") return "Moonshot / Kimi";
  return String(source || "").trim();
}

export function protocolGroupForType(type, modelID = "", baseURL = "", configuredGroup = "") {
  if (type === "anthropic") return PROTOCOL_GROUP_ANTHROPIC_MESSAGES;
  if (type === "gemini") return PROTOCOL_GROUP_GEMINI_NATIVE;
  // OpenAI 类型优先采用供应商模板给出的分组，再按模型名与 baseURL 推断；
  // 兼容 gpt-oss 等模型名规则与供应商实际协议不一致的中转站。
  return classifyModelProtocol(type, modelID, baseURL, "", configuredGroup);
}

export function parseBalanceHeaders(value) {
  const text = String(value || "").trim();
  if (!text) return undefined;
  try {
    const parsed = JSON.parse(text);
    if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") {
      throw new Error("请求头必须是 JSON 对象");
    }
    return parsed;
  } catch (error) {
    throw new Error(`查询请求头格式错误：${error?.message || "请输入 JSON 对象"}`);
  }
}

