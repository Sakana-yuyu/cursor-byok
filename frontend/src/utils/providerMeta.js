// 供应商展示元数据：内部 value 用小写协议名，UI 统一显示品牌名。
export const PROVIDERS = [
  {
    value: "openai",
    label: "OpenAI",
    icon: "icon-[bxl--openai]",
    accent: "#10a37f",
  },
  {
    value: "anthropic",
    label: "Anthropic",
    icon: "icon-[logos--claude-icon]",
    accent: "#d97757",
  },
  {
    value: "gemini",
    label: "Gemini",
    icon: "icon-[logos--google-gemini]",
    accent: "#4285f4",
  },
];

const providerByValue = Object.fromEntries(PROVIDERS.map((item) => [item.value, item]));

export function normalizeProviderType(type) {
  const raw = String(type || "").trim().toLowerCase();
  if (!raw) return "";
  if (raw === "openai" || raw === "oai" || raw.includes("openai")) return "openai";
  if (raw === "anthropic" || raw.includes("anthropic") || raw.includes("claude")) {
    return "anthropic";
  }
  if (raw === "gemini" || raw.includes("gemini") || raw.includes("google")) return "gemini";
  return raw;
}

export function providerMeta(type) {
  const key = normalizeProviderType(type);
  return providerByValue[key] || null;
}

export function providerLabel(type) {
  const meta = providerMeta(type);
  if (meta) return meta.label;
  const raw = String(type || "").trim();
  return raw || "-";
}

export function providerIcon(type) {
  return providerMeta(type)?.icon || "";
}

export function providerSelectOptions() {
  return PROVIDERS.map(({ value, label }) => ({ value, label }));
}