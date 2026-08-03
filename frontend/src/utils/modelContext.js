const DATA_SOURCE = "主流大模型列表.xlsx";

/**
 * MODEL_CAPABILITIES — 主流大模型能力元数据表。
 * 条目按优先级从高到低排列，越靠前越优先匹配。
 * 与后端 internal/modelcontext/catalog.go 保持同步。
 */
const MODEL_CAPABILITIES = [
  // ─── Claude ───────────────────────────────────────────────────────────────
  { pattern: /^(?:claude-)?opus-?4[.-]?8(?:-|$)|^claude-4[.-]?8-opus(?:-|$)/,
    displayName: "Claude Opus 4.8", contextWindowTokens: 1_000_000, maxOutputTokens: 32_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^(?:claude-)?opus-?4[.-]?7(?:-|$)|^claude-4[.-]?7-opus(?:-|$)/,
    displayName: "Claude Opus 4.7", contextWindowTokens: 1_000_000, maxOutputTokens: 32_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^(?:claude-)?sonnet-?4[.-]?6(?:-|$)|^claude-4[.-]?6-sonnet(?:-|$)/,
    displayName: "Claude Sonnet 4.6", contextWindowTokens: 1_000_000, maxOutputTokens: 64_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^(?:claude-)?sonnet-?5(?:-|$)|^claude-5-sonnet(?:-|$)/,
    displayName: "Claude Sonnet 5", contextWindowTokens: 1_000_000, maxOutputTokens: 64_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^claude-3-5-sonnet/,
    displayName: "Claude 3.5 Sonnet", contextWindowTokens: 200_000, maxOutputTokens: 8_192,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^claude-3-5-haiku/,
    displayName: "Claude 3.5 Haiku", contextWindowTokens: 200_000, maxOutputTokens: 8_192,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^claude-3/,
    displayName: "Claude 3", contextWindowTokens: 200_000, maxOutputTokens: 4_096,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^claude/,
    displayName: "Claude", contextWindowTokens: 200_000, maxOutputTokens: 8_192,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: false },

  // ─── GPT ──────────────────────────────────────────────────────────────────
  // gpt-5.6-luna/sol/terra：Codex 实际上限 272K（非理论 1M），须在通用规则前匹配
  { pattern: /^gpt-?5[.-]?6-(?:luna|sol|terra)(?:-|$)/,
    displayName: "GPT-5.6 (Codex)", contextWindowTokens: 272_000, maxOutputTokens: 32_768,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^gpt-?5[.-]?6(?:-|$)/,
    displayName: "GPT-5.6", contextWindowTokens: 1_000_000, maxOutputTokens: 32_768,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^gpt-?5[.-]?5(?:-|$)/,
    displayName: "GPT-5.5", contextWindowTokens: 400_000, maxOutputTokens: 128_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^gpt-?5[.-]?4(?:-|$)/,
    displayName: "GPT-5.4", contextWindowTokens: 400_000, maxOutputTokens: 128_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^gpt-?5(?:-|$)/,
    displayName: "GPT-5", contextWindowTokens: 1_000_000, maxOutputTokens: 32_768,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^gpt-?4o-mini(?:-|$)/,
    displayName: "GPT-4o mini", contextWindowTokens: 128_000, maxOutputTokens: 16_384,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^gpt-?4o(?:-|$)/,
    displayName: "GPT-4o", contextWindowTokens: 128_000, maxOutputTokens: 16_384,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: false },
// PLACEHOLDER_CAPABILITIES_CONTINUED
  { pattern: /^o[13]-mini(?:-|$)/,
    displayName: "OpenAI o-mini", contextWindowTokens: 128_000, maxOutputTokens: 65_536,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^o[134](?:-|$)/,
    displayName: "OpenAI o 系列", contextWindowTokens: 200_000, maxOutputTokens: 100_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },

  // ─── Gemini ───────────────────────────────────────────────────────────────
  { pattern: /^gemini-2[.-]?5-pro/,
    displayName: "Gemini 2.5 Pro", contextWindowTokens: 2_000_000, maxOutputTokens: 65_536,
    supportsVision: true, supportsAudio: true, supportsTools: true, supportsThinking: true },
  { pattern: /^gemini-2[.-]?5-flash/,
    displayName: "Gemini 2.5 Flash", contextWindowTokens: 1_000_000, maxOutputTokens: 65_536,
    supportsVision: true, supportsAudio: true, supportsTools: true, supportsThinking: true },
  { pattern: /^gemini-2[.-]?0/,
    displayName: "Gemini 2.0", contextWindowTokens: 1_000_000, maxOutputTokens: 8_192,
    supportsVision: true, supportsAudio: true, supportsTools: true, supportsThinking: false },
  { pattern: /^gemini/,
    displayName: "Gemini", contextWindowTokens: 128_000, maxOutputTokens: 8_192,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: false },

  // ─── Grok ─────────────────────────────────────────────────────────────────
  { pattern: /^grok-?4[.-]?5(?:-|$)/,
    displayName: "Grok 4.5", contextWindowTokens: 500_000, maxOutputTokens: 32_768,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^grok-?4[.-]?(?:3|20)(?:-|$)/,
    displayName: "Grok 4.3/4.20", contextWindowTokens: 1_000_000, maxOutputTokens: 32_768,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^grok-?4(?:-|$)/,
    displayName: "Grok 4", contextWindowTokens: 256_000, maxOutputTokens: 32_768,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^grok/,
    displayName: "Grok", contextWindowTokens: 128_000, maxOutputTokens: 8_192,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: false },

  // ─── Qwen ─────────────────────────────────────────────────────────────────
  { pattern: /^qwen-?3[.-]?8-max-preview(?:-|$)/,
    displayName: "Qwen3-8-Max-Preview", contextWindowTokens: 1_000_000, maxOutputTokens: 32_768,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^qwen-?3[.-]?7-max(?:-|$)/,
    displayName: "Qwen3-7-Max", contextWindowTokens: 1_000_000, maxOutputTokens: 32_768,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^qwen-?(?:vl|2-vl|2\.5-vl)/,
    displayName: "Qwen-VL", contextWindowTokens: 128_000, maxOutputTokens: 8_192,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^qwen-?(?:max|plus|turbo|long)(?:-|$)/,
    displayName: "Qwen", contextWindowTokens: 128_000, maxOutputTokens: 8_192,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^qwen/,
    displayName: "Qwen", contextWindowTokens: 128_000, maxOutputTokens: 8_192,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },

  // ─── DeepSeek ─────────────────────────────────────────────────────────────
  { pattern: /^deepseek-?v?4-flash(?:-|$)/,
    displayName: "DeepSeek V4 Flash", contextWindowTokens: 1_000_000, maxOutputTokens: 32_768,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^deepseek-?v?4-pro(?:-|$)/,
    displayName: "DeepSeek V4 Pro", contextWindowTokens: 1_000_000, maxOutputTokens: 32_768,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^deepseek-?v?4(?:-|$)/,
    displayName: "DeepSeek V4", contextWindowTokens: 1_000_000, maxOutputTokens: 32_768,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^deepseek-?v?3(?:-|$)/,
    displayName: "DeepSeek V3", contextWindowTokens: 128_000, maxOutputTokens: 8_192,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^deepseek-?r[12](?:-|$)/,
    displayName: "DeepSeek R 系列", contextWindowTokens: 128_000, maxOutputTokens: 32_768,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^deepseek/,
    displayName: "DeepSeek", contextWindowTokens: 64_000, maxOutputTokens: 4_096,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },

  // ─── Kimi / Moonshot ──────────────────────────────────────────────────────
  { pattern: /^kimi-?k?2[.-]?6(?:-|$)/,
    displayName: "Kimi K2.6", contextWindowTokens: 256_000, maxOutputTokens: 16_384,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^kimi-?k[12](?:-|$)/,
    displayName: "Kimi K 系列", contextWindowTokens: 128_000, maxOutputTokens: 8_192,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^moonshot/,
    displayName: "Moonshot", contextWindowTokens: 128_000, maxOutputTokens: 4_096,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^kimi/,
    displayName: "Kimi", contextWindowTokens: 128_000, maxOutputTokens: 4_096,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },

  // ─── GLM ──────────────────────────────────────────────────────────────────
  { pattern: /^glm-?5[.-]?2(?:-|$)/,
    displayName: "GLM-5.2", contextWindowTokens: 200_000, maxOutputTokens: 8_192,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^glm-?4v/,
    displayName: "GLM-4V", contextWindowTokens: 128_000, maxOutputTokens: 4_096,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^glm/,
    displayName: "GLM", contextWindowTokens: 128_000, maxOutputTokens: 4_096,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },

  // ─── MiMo ─────────────────────────────────────────────────────────────────
  { pattern: /^mimo-?vl(?:-|$)/,
    displayName: "MiMo-VL", contextWindowTokens: 128_000, maxOutputTokens: 8_192,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^mimo/,
    displayName: "MiMo", contextWindowTokens: 128_000, maxOutputTokens: 8_192,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: true },

  // ─── MiniMax ──────────────────────────────────────────────────────────────
  { pattern: /^minimax-?(?:vl|vision)/,
    displayName: "MiniMax-VL", contextWindowTokens: 256_000, maxOutputTokens: 8_192,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^minimax/,
    displayName: "MiniMax", contextWindowTokens: 256_000, maxOutputTokens: 8_192,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },

  // ─── StepFun ──────────────────────────────────────────────────────────────
  { pattern: /^step-2/,
    displayName: "Step-2", contextWindowTokens: 512_000, maxOutputTokens: 16_384,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^step-1[.-]?v(?:-|$)/,
    displayName: "Step-1V", contextWindowTokens: 128_000, maxOutputTokens: 8_192,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^step-1/,
    displayName: "Step-1", contextWindowTokens: 256_000, maxOutputTokens: 8_192,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^step/,
    displayName: "StepFun", contextWindowTokens: 128_000, maxOutputTokens: 8_192,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },
];

// PLACEHOLDER_FUNCTIONS
function normalizeModelID(value) {
  return String(value || "")
    .trim()
    .toLowerCase()
    .replace(/^models\//, "")
    .replace(/^.*\//, "")
    .replace(/[\s_]+/g, "-");
}

/**
 * resolveModelCapabilities — 根据模型 ID 返回完整能力元数据，未知模型返回 null。
 */
export function resolveModelCapabilities(modelID) {
  const normalized = normalizeModelID(modelID);
  if (!normalized) return null;
  const entry = MODEL_CAPABILITIES.find(({ pattern }) => pattern.test(normalized));
  if (!entry) return null;
  const { pattern: _p, ...rest } = entry;
  return { ...rest, modelID: normalized, source: DATA_SOURCE };
}

/**
 * resolveModelContextWindow — 向后兼容：返回 { tokens, source, label, modelID } 或 null。
 */
export function resolveModelContextWindow(modelID) {
  const c = resolveModelCapabilities(modelID);
  if (!c) return null;
  return { tokens: c.contextWindowTokens, source: c.source, label: c.displayName, modelID: c.modelID };
}

/**
 * contextWindowTokensForModel — 优先使用显式值，否则从目录推断。向后兼容。
 */
export function contextWindowTokensForModel(modelID, explicitValue) {
  const parsed = Number(explicitValue);
  if (Number.isInteger(parsed) && parsed > 0) return parsed;
  return resolveModelCapabilities(modelID)?.contextWindowTokens || 0;
}

export const MODEL_CONTEXT_DATA_SOURCE = DATA_SOURCE;
