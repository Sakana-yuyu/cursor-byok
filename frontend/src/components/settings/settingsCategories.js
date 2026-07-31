export const SETTINGS_CATEGORY_STORAGE_KEY = "cursor-byok.settings.category";

export const SETTINGS_CATEGORIES = [
  {
    id: "general",
    value: "general",
    label: "常规",
    description: "工作区基础与常用偏好。",
  },
  {
    id: "cursor-service",
    value: "cursor-service",
    label: "Cursor 服务",
    description: "本地服务与启动相关配置。",
  },
  {
    id: "overlay",
    value: "overlay",
    label: "浮窗",
    description: "桌面浮窗样式与行为。",
  },
  {
    id: "delegation",
    value: "delegation",
    label: "委托",
    description: "任务委托与运行时面板。",
  },
  {
    id: "skills-mcp",
    value: "skills-mcp",
    label: "技能与 MCP",
    description: "跨工具技能和 MCP 扫描。",
  },
  {
    id: "prompts",
    value: "prompts",
    label: "提示词",
    description: "提示词注入与本地化。",
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
