const DATA_SOURCE = "主流大模型列表.xlsx";

/**
 * MODEL_CAPABILITIES — 主流大模型能力元数据表。
 * 条目按优先级从高到低排列，越靠前越优先匹配。
 * 与后端 internal/modelcontext/catalog.go 保持同步。
 */
const MODEL_CAPABILITIES = [
  // ─── Claude ───────────────────────────────────────────────────────────────
  // 2026-09 官方文档：Fable 5.1 / Fable 5 / Opus 5 均为 1M 上下文、128K 输出、
  // 自适应思考常开（effort 五档，默认 high）；Haiku 4.5 为 200K/64K 手动思考预算。
  { pattern: /^claude-?fable-?5[.-]?1(?:-|$)/,
    displayName: "Claude Fable 5.1", contextWindowTokens: 1_000_000, maxOutputTokens: 128_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true,
    reasoningEfforts: ["low", "medium", "high", "xhigh", "max"] },
  { pattern: /^claude-?fable-?5(?:-|$)/,
    displayName: "Claude Fable 5", contextWindowTokens: 1_000_000, maxOutputTokens: 128_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true,
    reasoningEfforts: ["low", "medium", "high", "xhigh", "max"] },
  { pattern: /^(?:claude-)?opus-?5(?:-|$)|^claude-5-opus(?:-|$)/,
    displayName: "Claude Opus 5", contextWindowTokens: 1_000_000, maxOutputTokens: 128_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true,
    reasoningEfforts: ["low", "medium", "high", "xhigh", "max"] },
  { pattern: /^claude-?haiku-?4[.-]?5(?:-|$)/,
    displayName: "Claude Haiku 4.5", contextWindowTokens: 200_000, maxOutputTokens: 64_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
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
    displayName: "Claude Sonnet 5", contextWindowTokens: 1_000_000, maxOutputTokens: 128_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true,
    reasoningEfforts: ["low", "medium", "high", "xhigh", "max"] },
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
  // GPT-6 Astra：1.05M 上下文 / 128K 输出，思考不可关闭（effort 无 none 档）。
  { pattern: /^gpt-?6/,
    displayName: "GPT-6 Astra", contextWindowTokens: 1_048_576, maxOutputTokens: 128_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true,
    reasoningEfforts: ["low", "medium", "high", "xhigh", "max"] },
  // gpt-5.6-luna/sol/terra：Codex 实际上限 272K（非理论 1M），须在通用规则前匹配
  { pattern: /^gpt-?5[.-]?6-luna(?:-|$)/,
    displayName: "GPT-5.6 Luna", contextWindowTokens: 272_000, maxOutputTokens: 128_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true,
    reasoningEfforts: ["none", "low", "medium", "high", "xhigh", "max"] },
  { pattern: /^gpt-?5[.-]?6-terra(?:-|$)/,
    displayName: "GPT-5.6 Terra", contextWindowTokens: 272_000, maxOutputTokens: 128_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true,
    reasoningEfforts: ["none", "low", "medium", "high", "xhigh", "max"] },
  { pattern: /^gpt-?5[.-]?6-sol(?:-|$)/,
    displayName: "GPT-5.6 Sol", contextWindowTokens: 272_000, maxOutputTokens: 128_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true,
    reasoningEfforts: ["none", "low", "medium", "high", "xhigh", "max"] },
  { pattern: /^gpt-?5[.-]?6(?:-|$)/,
    displayName: "GPT-5.6", contextWindowTokens: 1_000_000, maxOutputTokens: 128_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true,
    reasoningEfforts: ["none", "low", "medium", "high", "xhigh", "max"] },
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
  { pattern: /^o[13]-mini(?:-|$)/,
    displayName: "OpenAI o-mini", contextWindowTokens: 128_000, maxOutputTokens: 65_536,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^o[134](?:-|$)/,
    displayName: "OpenAI o 系列", contextWindowTokens: 200_000, maxOutputTokens: 100_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },

  // ─── Gemini ───────────────────────────────────────────────────────────────
  // Gemini 3.x Flash 家族：1M 上下文 / 64K 输出，多模态输入（含音频），
  // 思考档位 low/medium/high（minimal 不支持会报错）。
  { pattern: /^gemini-3[.-]?8-flash/,
    displayName: "Gemini 3.8 Flash", contextWindowTokens: 1_048_576, maxOutputTokens: 65_536,
    supportsVision: true, supportsAudio: true, supportsTools: true, supportsThinking: true,
    reasoningEfforts: ["low", "medium", "high"] },
  { pattern: /^gemini-3[.-]?7-flash/,
    displayName: "Gemini 3.7 Flash", contextWindowTokens: 1_048_576, maxOutputTokens: 65_536,
    supportsVision: true, supportsAudio: true, supportsTools: true, supportsThinking: true,
    reasoningEfforts: ["low", "medium", "high"] },
  { pattern: /^gemini-3[.-]?6-flash/,
    displayName: "Gemini 3.6 Flash", contextWindowTokens: 1_048_576, maxOutputTokens: 65_536,
    supportsVision: true, supportsAudio: true, supportsTools: true, supportsThinking: true },
  { pattern: /^gemini-3[.-]?5-flash-lite/,
    displayName: "Gemini 3.5 Flash-Lite", contextWindowTokens: 1_048_576, maxOutputTokens: 65_536,
    supportsVision: true, supportsAudio: true, supportsTools: true, supportsThinking: true },
  { pattern: /^gemini-3[.-]?5-flash/,
    displayName: "Gemini 3.5 Flash", contextWindowTokens: 1_048_576, maxOutputTokens: 65_536,
    supportsVision: true, supportsAudio: true, supportsTools: true, supportsThinking: true },
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
  { pattern: /^kimi-?3(?:-|\.|$)/,
    displayName: "Kimi 3.0", contextWindowTokens: 256_000, maxOutputTokens: 16_384,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^kimi-?k3(?:-|\.|$)/,
    displayName: "Kimi K3", contextWindowTokens: 256_000, maxOutputTokens: 16_384,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: false },
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
  // 2026-09 按 bigmodel.cn / docs.z.ai 官方文档同步：5.x 为纯文本（V 后缀才是视觉），
  // 思考自 4.5 代起混合支持；reasoning_effort 自 GLM-5.2 起支持，5.3 起不可禁用思考。
  { pattern: /^glm-?5[.-]?3-flash(?:-|$)/,
    displayName: "GLM-5.3-Flash", contextWindowTokens: 1_000_000, maxOutputTokens: 128_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true,
    reasoningEfforts: ["low", "high", "max"] },
  { pattern: /^glm-?5[.-]?3(?:-|$)/,
    displayName: "GLM-5.3", contextWindowTokens: 1_000_000, maxOutputTokens: 128_000,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: true,
    reasoningEfforts: ["low", "high", "max"] },
  { pattern: /^glm-?5[.-]?2(?:-|$)/,
    displayName: "GLM-5.2", contextWindowTokens: 1_000_000, maxOutputTokens: 128_000,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: true,
    reasoningEfforts: ["none", "minimal", "low", "medium", "high", "xhigh", "max"] },
  { pattern: /^glm-?5[.-]?1(?:-|$)/,
    displayName: "GLM-5.1", contextWindowTokens: 200_000, maxOutputTokens: 128_000,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^glm-?5-turbo(?:-|$)/,
    displayName: "GLM-5-Turbo", contextWindowTokens: 200_000, maxOutputTokens: 128_000,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^glm-?5(?:-|$)/,
    displayName: "GLM-5", contextWindowTokens: 200_000, maxOutputTokens: 128_000,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^glm-?4[.-]?7-flashx(?:-|$)/,
    displayName: "GLM-4.7-FlashX", contextWindowTokens: 200_000, maxOutputTokens: 128_000,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^glm-?4[.-]?7-flash(?:-|$)/,
    displayName: "GLM-4.7-Flash", contextWindowTokens: 200_000, maxOutputTokens: 128_000,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^glm-?4[.-]?7(?:-|$)/,
    displayName: "GLM-4.7", contextWindowTokens: 200_000, maxOutputTokens: 128_000,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^glm-?4[.-]?6v-flashx(?:-|$)/,
    displayName: "GLM-4.6V-FlashX", contextWindowTokens: 128_000, maxOutputTokens: 32_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^glm-?4[.-]?6v-flash(?:-|$)/,
    displayName: "GLM-4.6V-Flash", contextWindowTokens: 128_000, maxOutputTokens: 32_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^glm-?4[.-]?6v(?:-|$)/,
    displayName: "GLM-4.6V", contextWindowTokens: 128_000, maxOutputTokens: 32_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^glm-?4[.-]?6(?:-|$)/,
    displayName: "GLM-4.6", contextWindowTokens: 200_000, maxOutputTokens: 128_000,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^glm-?4[.-]?5-airx(?:-|$)/,
    displayName: "GLM-4.5-AirX", contextWindowTokens: 128_000, maxOutputTokens: 98_304,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^glm-?4[.-]?5-air(?:-|$)/,
    displayName: "GLM-4.5-Air", contextWindowTokens: 128_000, maxOutputTokens: 98_304,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^glm-?4[.-]?5-x(?:-|$)/,
    displayName: "GLM-4.5-X", contextWindowTokens: 128_000, maxOutputTokens: 98_304,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^glm-?4[.-]?5-flash(?:-|$)/,
    displayName: "GLM-4.5-Flash", contextWindowTokens: 128_000, maxOutputTokens: 98_304,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^glm-?4[.-]?5(?:-|$)/,
    displayName: "GLM-4.5", contextWindowTokens: 128_000, maxOutputTokens: 98_304,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^glm-?4[.-]?1v-thinking-flashx(?:-|$)/,
    displayName: "GLM-4.1V-Thinking-FlashX", contextWindowTokens: 65_536, maxOutputTokens: 16_384,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^glm-?4[.-]?1v-thinking-flash(?:-|$)/,
    displayName: "GLM-4.1V-Thinking-Flash", contextWindowTokens: 65_536, maxOutputTokens: 16_384,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^glm-?4-flashx-250414(?:-|$)/,
    displayName: "GLM-4-FlashX-250414", contextWindowTokens: 128_000, maxOutputTokens: 16_384,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^glm-?4-flash-250414(?:-|$)/,
    displayName: "GLM-4-Flash-250414", contextWindowTokens: 128_000, maxOutputTokens: 16_384,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^glm-?4-long(?:-|$)/,
    displayName: "GLM-4-Long", contextWindowTokens: 1_000_000, maxOutputTokens: 4_096,
    supportsVision: false, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^glm-?4v-flash(?:-|$)/,
    displayName: "GLM-4V-Flash", contextWindowTokens: 16_384, maxOutputTokens: 1_024,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^glm-?4v/,
    displayName: "GLM-4V", contextWindowTokens: 128_000, maxOutputTokens: 4_096,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: false },
  { pattern: /^glm-?5v-turbo(?:-|$)/,
    displayName: "GLM-5V-Turbo", contextWindowTokens: 200_000, maxOutputTokens: 128_000,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^glm-?4[.-]?5v(?:-|$)/,
    displayName: "GLM-4.5V", contextWindowTokens: 128_000, maxOutputTokens: 8_192,
    supportsVision: true, supportsAudio: false, supportsTools: true, supportsThinking: true },
  { pattern: /^glm-?ocr(?:-|$)/,
    displayName: "GLM-OCR", contextWindowTokens: 128_000, maxOutputTokens: 8_192,
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

/**
 * isModelCovered — 报告模型 ID 是否命中内置能力目录。
 * 与后端 modelcontext.Lookup 的判定口径一致（同一份规则数据源）。
 * 未覆盖 ≠ 不支持：表示能力未知，需要用户补填或目录补录。
 */
export function isModelCovered(modelID) {
  const normalized = normalizeModelID(modelID);
  if (!normalized) return false;
  return MODEL_CAPABILITIES.some(({ pattern }) => pattern.test(normalized));
}
