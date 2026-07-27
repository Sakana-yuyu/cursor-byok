// 品牌供应商模板。supplierID 与后端余额识别保持稳定；type 仍表示实际协议适配器。
export const SUPPLIER_TEMPLATES = [
  {
    id: "custom",
    label: "自定义供应商",
    type: "openai",
    baseURL: "",
    endpoint: "/v1/chat/completions",
    requestGroup: "chat_completions",
    models: [],
    allowCustomURL: true,
  },
  {
    id: "openai",
    label: "OpenAI",
    type: "openai",
    baseURL: "https://api.openai.com/v1",
    endpoint: "/v1/responses",
    requestGroup: "responses",
    models: ["gpt-5", "gpt-4.1"],
    allowCustomURL: true,
  },
  {
    id: "anthropic",
    label: "Anthropic",
    type: "anthropic",
    baseURL: "https://api.anthropic.com",
    endpoint: "",
    requestGroup: "",
    models: ["claude-sonnet-4-20250514", "claude-3-7-sonnet-latest"],
    allowCustomURL: true,
  },
  {
    id: "gemini",
    label: "Gemini",
    type: "gemini",
    baseURL: "https://generativelanguage.googleapis.com/v1beta",
    endpoint: "",
    requestGroup: "gemini_native",
    models: ["gemini-2.5-pro", "gemini-2.5-flash"],
    allowCustomURL: true,
  },
  {
    id: "deepseek",
    label: "DeepSeek",
    type: "openai",
    baseURL: "https://api.deepseek.com/v1",
    endpoint: "/v1/chat/completions",
    requestGroup: "chat_completions",
    models: ["deepseek-chat", "deepseek-reasoner"],
    allowCustomURL: true,
  },
  {
    id: "moonshot",
    label: "Kimi / Moonshot",
    type: "openai",
    baseURL: "https://api.moonshot.cn/v1",
    endpoint: "/v1/chat/completions",
    requestGroup: "chat_completions",
    models: ["kimi-k2.5", "moonshot-v1-128k"],
    allowCustomURL: true,
  },
  {
    id: "volcengine",
    label: "火山方舟（豆包）",
    type: "openai",
    baseURL: "https://ark.cn-beijing.volces.com/api/v3",
    endpoint: "/v1/chat/completions",
    requestGroup: "chat_completions",
    models: ["doubao-seed-1-6-251015", "doubao-1-5-pro-32k-250115"],
    allowCustomURL: true,
    presets: [
      { id: "volcengine-coding", label: "Coding Plan", model: "doubao-seed-code" },
      { id: "volcengine-agent", label: "Agent Plan", model: "doubao-seed-1-6-251015" },
    ],
  },
  {
    id: "openrouter",
    label: "OpenRouter",
    type: "openai",
    baseURL: "https://openrouter.ai/api/v1",
    endpoint: "/v1/chat/completions",
    requestGroup: "chat_completions",
    models: ["openai/gpt-4.1", "anthropic/claude-sonnet-4"],
    allowCustomURL: true,
  },
  {
    id: "siliconflow",
    label: "SiliconFlow",
    type: "openai",
    baseURL: "https://api.siliconflow.cn/v1",
    endpoint: "/v1/chat/completions",
    requestGroup: "chat_completions",
    models: ["deepseek-ai/DeepSeek-V3", "Qwen/Qwen3-Coder-480B-A35B-Instruct"],
    allowCustomURL: true,
  },
  {
    id: "stepfun",
    label: "阶跃星辰",
    type: "openai",
    baseURL: "https://api.stepfun.com/v1",
    endpoint: "/v1/chat/completions",
    requestGroup: "chat_completions",
    models: ["step-3.5-flash"],
    allowCustomURL: true,
  },
  {
    id: "novita",
    label: "Novita",
    type: "openai",
    baseURL: "https://api.novita.ai/v3/openai",
    endpoint: "/v1/chat/completions",
    requestGroup: "chat_completions",
    models: ["deepseek/deepseek-v3-0324"],
    allowCustomURL: true,
  },
];

const supplierByID = new Map(SUPPLIER_TEMPLATES.map((item) => [item.id, item]));

export function supplierTemplate(id) {
  return supplierByID.get(String(id || "custom").trim().toLowerCase()) || supplierByID.get("custom");
}

export function supplierSelectOptions() {
  return SUPPLIER_TEMPLATES.map(({ id, label }) => ({ value: id, label }));
}

export function supplierLabel(id) {
  return supplierTemplate(id)?.label || "自定义供应商";
}
