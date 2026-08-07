// Model editor static metadata (pure constants + small helpers, no reactive state).

export const reasoningEffortOptions = [
  { label: "低", value: "low", icon: "icon-[mdi--head-outline]" },
  { label: "中", value: "medium", icon: "icon-[mdi--head-lightbulb-outline]" },
  { label: "高", value: "high", icon: "icon-[mdi--brain]" },
  { label: "极高", value: "xhigh", icon: "icon-[mdi--head-cog-outline]" },
  { label: "最高", value: "max", icon: "icon-[mdi--brain]" },
];

export const anthropicThinkingEffortOptions = [
  { label: "低", value: "low", icon: "icon-[mdi--head-outline]" },
  { label: "中", value: "medium", icon: "icon-[mdi--head-lightbulb-outline]" },
  { label: "高", value: "high", icon: "icon-[mdi--brain]" },
  { label: "极高", value: "xhigh", icon: "icon-[mdi--head-cog-outline]" },
  { label: "Max", value: "max", icon: "icon-[mdi--brain]" },
];

export const CONTEXT_TIERS = [
  { label: "128K", tokens: 128_000 },
  { label: "200K", tokens: 200_000 },
  { label: "256K", tokens: 256_000 },
  { label: "500K", tokens: 500_000 },
  { label: "1M", tokens: 1_000_000 },
];

export function hasBalanceQueryHeadersOverride(value) {
  const text = String(value || "").trim();
  if (!text || text === "{}") return false;
  try {
    const parsed = JSON.parse(text);
    return Boolean(parsed && typeof parsed === "object" && !Array.isArray(parsed) && Object.keys(parsed).length > 0);
  } catch (_error) {
    // 非法 JSON 也算覆盖，方便用户看到并修正
    return true;
  }
}

export const fieldTips = {
  displayName: "仅用于界面展示，便于你区分不同模型。",
  modelID: "请求实际发送给服务端的模型名称，例如 gpt-4.1 或 claude-sonnet。",
  baseURL: "模型服务的 API 根地址，通常为兼容 OpenAI 或 Anthropic 的接口入口。",
  modelCatalogURL: "直接读取模型列表的完整 URL。留空时使用供应商预设地址或兼容协议的自动候选地址。",
  apiKey: "调用该模型服务需要使用的访问密钥。",
  contextWindowTokens: "模型单次可接受的最大上下文 Token 数。留空时使用默认值。",
  reasoningEffort: "推理强度仅对部分支持 reasoning_effort 的模型生效，并不是所有模型都支持。越高通常越稳，但也可能更慢。",
  maxCompletionTokens: "单次回复允许生成的最大 Token 数。留空时使用默认值。",
  openAIEndpoint: "选择接口协议端点。选“自定义路径”时，请在接口地址栏填写完整请求地址（含 /chat/completions 或 /responses 路径后缀），系统会根据末段自动判断协议形态。",
  protocolMode: "自动识别会根据供应商、模型名称和接口地址选择协议；固定协议会严格使用你指定的协议，测速不会自动覆盖。",
  openAIRequestGroup: "请求分组决定请求体协议形态，括号内为发送给服务端的技术值。Responses 适用于新版 /v1/responses 接口；Chat Completions 适用于标准对话补全接口；Chat Completions 兼容模式用于部分字段不完全兼容的第三方网关（会跳过高级字段）。一般与接口端点保持对应。",
  openAIExtraParams: "开启后会把 JSON 对象覆盖到 OpenAI 请求体。同名字段以这里为准。OpenAI service_tier 支持 auto、default、flex、scale、priority。",
  customHeaders: "开启后会把 JSON 对象覆盖到最终请求头。同名请求头以这里为准，值必须是字符串。",
  anthropicExtraParams: "开启后会把 JSON 对象覆盖到 Anthropic 请求体。同名字段以这里为准。",
  anthropicMaxTokens: "Anthropic 模型单次回复允许生成的最大 Token 数。留空时使用默认值。",
  anthropicThinkingEffort: "Anthropic adaptive thinking 的思考强度。请求会固定使用新版 thinking.type=adaptive。",
  tooltipData: "模型列表 hover 时显示的备注说明。",
  balanceQueryURL: "自定义余额查询接口的完整 URL。支持 {{apiKey}}、{{baseUrl}}、{{accessToken}}、{{userId}}。",
  balanceQueryField: "从 JSON 响应中取值的点分路径，例如 data.balance 或 data.0.total_balance。仅自定义模板需要。",
  balanceQueryHeaders: "自定义余额查询请求头 JSON。支持 {{apiKey}}、{{baseUrl}}、{{accessToken}}、{{userId}}。",
  balanceProfile: "查询模板：自定义 / 通用模板 / New API / Token Plan / 官方。官方模板按接口地址识别具名供应商，通用模板请求 /user/balance。",
  balanceAccessToken: "New API 个人安全设置中生成的访问令牌（不是渠道 sk）。",
  balanceUserID: "New API 用户 ID，请求头 New-Api-User 使用。",
  balanceCodingPlanProvider: "Token Plan 供应商。智谱团队版与个人版 baseURL 相同，必须显式选择 Team。",
};

