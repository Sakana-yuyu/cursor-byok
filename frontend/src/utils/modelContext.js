const DATA_SOURCE = "主流大模型列表.xlsx";

const CONTEXT_RULES = [
  { pattern: /^(?:claude-)?opus-?4[.-]?8(?:-|$)|^claude-4[.-]?8-opus(?:-|$)/, tokens: 1_000_000, label: "Claude Opus 4.8" },
  { pattern: /^(?:claude-)?opus-?4[.-]?7(?:-|$)|^claude-4[.-]?7-opus(?:-|$)/, tokens: 1_000_000, label: "Claude Opus 4.7" },
  { pattern: /^(?:claude-)?sonnet-?4[.-]?6(?:-|$)|^claude-4[.-]?6-sonnet(?:-|$)/, tokens: 1_000_000, label: "Claude Sonnet 4.6" },
  { pattern: /^(?:claude-)?sonnet-?5(?:-|$)|^claude-5-sonnet(?:-|$)/, tokens: 1_000_000, label: "Claude Sonnet 5" },
  { pattern: /^gpt-?5[.-]?6(?:-|$)/, tokens: 1_000_000, label: "GPT-5.6 系列" },
  { pattern: /^gpt-?4o(?:-|$)/, tokens: 128_000, label: "GPT-4o" },
  { pattern: /^grok-?4[.-]?5(?:-|$)/, tokens: 500_000, label: "Grok 4.5" },
  { pattern: /^grok-?4[.-]?(?:3|20)(?:-|$)/, tokens: 1_000_000, label: "Grok 4.3/4.20" },
  { pattern: /^qwen-?3[.-]?8-max-preview(?:-|$)/, tokens: 1_000_000, label: "Qwen 3.8-Max-Preview" },
  { pattern: /^qwen-?3[.-]?7-max(?:-|$)/, tokens: 1_000_000, label: "Qwen 3.7-Max" },
  { pattern: /^deepseek-?v?4-flash(?:-|$)/, tokens: 1_000_000, label: "DeepSeek V4 Flash" },
  { pattern: /^deepseek-?v?4-pro(?:-|$)/, tokens: 1_000_000, label: "DeepSeek V4 Pro" },
  { pattern: /^kimi-?k?2[.-]?6(?:-|$)/, tokens: 256_000, label: "Kimi K2.6" },
  // Excel 中 GLM-5.2 为 200K-1M，自动配置采用保守下限，用户仍可手动覆盖。
  { pattern: /^glm-?5[.-]?2(?:-|$)/, tokens: 200_000, label: "GLM-5.2" },
];

function normalizeModelID(value) {
  return String(value || "")
    .trim()
    .toLowerCase()
    .replace(/^models\//, "")
    .replace(/^.*\//, "")
    .replace(/[\s_]+/g, "-");
}

export function resolveModelContextWindow(modelID) {
  const normalized = normalizeModelID(modelID);
  if (!normalized) return null;
  const rule = CONTEXT_RULES.find(({ pattern }) => pattern.test(normalized));
  return rule
    ? { tokens: rule.tokens, source: DATA_SOURCE, label: rule.label, modelID: normalized }
    : null;
}

export function contextWindowTokensForModel(modelID, explicitValue) {
  const parsed = Number(explicitValue);
  if (Number.isInteger(parsed) && parsed > 0) return parsed;
  return resolveModelContextWindow(modelID)?.tokens || 0;
}

export const MODEL_CONTEXT_DATA_SOURCE = DATA_SOURCE;