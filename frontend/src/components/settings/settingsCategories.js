export const SETTINGS_CATEGORY_STORAGE_KEY = "cursor-byok.settings.category";

import GeneralSettings from "@/components/settings/categories/GeneralSettings.vue";
import CursorServiceSettings from "@/components/settings/categories/CursorServiceSettings.vue";
import DelegationSettings from "@/components/settings/categories/DelegationSettings.vue";
import SkillsMcpSettings from "@/components/settings/categories/SkillsMcpSettings.vue";
import PromptSettings from "@/components/settings/categories/PromptSettings.vue";
import OverlaySettings from "@/components/settings/categories/OverlaySettings.vue";
import HistorySettings from "@/components/settings/categories/HistorySettings.vue";
import AdvancedSettings from "@/components/settings/categories/AdvancedSettings.vue";

// 设置侧边栏分组：每组一个标题，组内按使用频率排序。
// nav 决定入口层级：common 常驻显示，more 收纳到可展开的「更多设置」。
export const SETTINGS_GROUPS = [
  {
    key: "core",
    label: "基础",
    nav: "common",
    categories: ["general"],
  },
  {
    key: "service",
    label: "服务与模型",
    nav: "common",
    categories: ["cursor-service", "delegation"],
  },
  {
    key: "view",
    label: "界面",
    nav: "common",
    categories: ["overlay"],
  },
  {
    key: "more",
    label: "更多设置",
    nav: "more",
    categories: ["skills-mcp", "prompts", "history", "advanced"],
  },
];

export const SETTINGS_CATEGORIES = [
  {
    id: "general",
    value: "general",
    label: "通用",
    nav: "common",
    description: "工作区基础与常用偏好。",
  },
  {
    id: "cursor-service",
    value: "cursor-service",
    label: "Cursor 与服务",
    nav: "common",
    description: "本地服务与启动相关配置。",
  },
  {
    id: "delegation",
    value: "delegation",
    label: "模型与委派",
    nav: "common",
    description: "模型分组、任务委托与视觉委派。",
  },
  {
    id: "skills-mcp",
    value: "skills-mcp",
    label: "Skills 与 MCP",
    nav: "more",
    description: "跨工具技能和 MCP 扫描。",
  },
  {
    id: "prompts",
    value: "prompts",
    label: "提示词",
    nav: "more",
    description: "提示词注入与本地化。",
  },
  {
    id: "overlay",
    value: "overlay",
    label: "浮窗",
    nav: "common",
    description: "桌面浮窗样式与行为。",
  },
  {
    id: "history",
    value: "history",
    label: "历史与日志",
    nav: "more",
    description: "本地历史记录管理、日志导出与清理。",
  },
  {
    id: "advanced",
    value: "advanced",
    label: "高级",
    nav: "more",
    description: "高风险或低频系统设置。",
  },
];

// 分类组件映射：与 SETTINGS_CATEGORIES 的 id 一一对应。
// 设置页保持单页内静态加载，避免动态 import 在 dev/preview 环境下的解析差异。
export const SETTINGS_CATEGORY_COMPONENTS = {
  general: GeneralSettings,
  "cursor-service": CursorServiceSettings,
  delegation: DelegationSettings,
  "skills-mcp": SkillsMcpSettings,
  prompts: PromptSettings,
  overlay: OverlaySettings,
  history: HistorySettings,
  advanced: AdvancedSettings,
};

export function resolveSettingsCategoryComponent(categoryID) {
  return SETTINGS_CATEGORY_COMPONENTS[normalizeSettingsCategory(categoryID)] ?? null;
}

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