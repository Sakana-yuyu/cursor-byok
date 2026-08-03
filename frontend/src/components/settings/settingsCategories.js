export const SETTINGS_CATEGORY_STORAGE_KEY = "cursor-byok.settings.category";

// 设置侧边栏分组：每组一个标题，组内按使用频率排序。
export const SETTINGS_GROUPS = [
  {
    key: "core",
    label: "基础",
    categories: ["general"],
  },
  {
    key: "service",
    label: "服务与模型",
    categories: ["cursor-service", "delegation", "skills-mcp", "prompts"],
  },
  {
    key: "view",
    label: "界面",
    categories: ["overlay"],
  },
  {
    key: "system",
    label: "系统",
    categories: ["history", "advanced"],
  },
];

export const SETTINGS_CATEGORIES = [
  {
    id: "general",
    value: "general",
    label: "通用",
    description: "工作区基础与常用偏好。",
  },
  {
    id: "cursor-service",
    value: "cursor-service",
    label: "Cursor 与服务",
    description: "本地服务与启动相关配置。",
  },
  {
    id: "delegation",
    value: "delegation",
    label: "模型与委派",
    description: "模型分组、任务委托与视觉委派。",
  },
  {
    id: "skills-mcp",
    value: "skills-mcp",
    label: "Skills 与 MCP",
    description: "跨工具技能和 MCP 扫描。",
  },
  {
    id: "prompts",
    value: "prompts",
    label: "提示词",
    description: "提示词注入与本地化。",
  },
  {
    id: "overlay",
    value: "overlay",
    label: "浮窗",
    description: "桌面浮窗样式与行为。",
  },
  {
    id: "history",
    value: "history",
    label: "历史与日志",
    description: "本地历史记录管理、日志导出与清理。",
  },
  {
    id: "advanced",
    value: "advanced",
    label: "高级",
    description: "高风险或低频系统设置。",
  },
];

const settingsCategoryIDs = new Set(SETTINGS_CATEGORIES.map((category) => category.id));

export function isValidSettingsCategory(categoryID) {
  return settingsCategoryIDs.has(categoryID);
}

export function normalizeSettingsCategory(categoryID) {
  return isValidSettingsCategory(categoryID) ? categoryID : "general";
}

export function readStoredSettingsCategory() {
  if (typeof window === "undefined" || !window.localStorage) {
    return "general";
  }

  return normalizeSettingsCategory(window.localStorage.getItem(SETTINGS_CATEGORY_STORAGE_KEY));
}

export function writeStoredSettingsCategory(categoryID) {
  if (typeof window === "undefined" || !window.localStorage) {
    return;
  }

  window.localStorage.setItem(
    SETTINGS_CATEGORY_STORAGE_KEY,
    normalizeSettingsCategory(categoryID),
  );
}