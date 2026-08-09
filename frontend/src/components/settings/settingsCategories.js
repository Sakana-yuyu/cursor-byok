export const SETTINGS_CATEGORY_STORAGE_KEY = "cursor-byok.settings.category";

import GeneralSettings from "@/components/settings/categories/GeneralSettings.vue";
import CursorServiceSettings from "@/components/settings/categories/CursorServiceSettings.vue";
import DelegationSettings from "@/components/settings/categories/DelegationSettings.vue";
import SkillsMcpSettings from "@/components/settings/categories/SkillsMcpSettings.vue";
import PromptSettings from "@/components/settings/categories/PromptSettings.vue";
import OverlaySettings from "@/components/settings/categories/OverlaySettings.vue";
import HistorySettings from "@/components/settings/categories/HistorySettings.vue";
import AdvancedSettings from "@/components/settings/categories/AdvancedSettings.vue";
import AboutSettings from "@/components/settings/categories/AboutSettings.vue";

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
    categories: ["skills-mcp", "prompts", "history", "advanced", "about"],
  },
];

export const SETTINGS_CATEGORIES = [
  {
    id: "general",
    value: "general",
    label: "通用",
    nav: "common",
    description: "工作区基础与常用偏好。",
    icon: "icon-[mdi--cog-outline]",
  },
  {
    id: "cursor-service",
    value: "cursor-service",
    label: "Cursor 与服务",
    nav: "common",
    description: "本地服务与启动相关配置。",
    icon: "icon-[mdi--server-network-outline]",
  },
  {
    id: "delegation",
    value: "delegation",
    label: "模型与委派",
    nav: "common",
    description: "模型分组、任务委托与视觉委派。",
    icon: "icon-[mdi--robot-outline]",
  },
  {
    id: "skills-mcp",
    value: "skills-mcp",
    label: "Skills 与 MCP",
    nav: "more",
    description: "跨工具技能和 MCP 扫描。",
    icon: "icon-[mdi--connection]",
  },
  {
    id: "prompts",
    value: "prompts",
    label: "提示词",
    nav: "more",
    description: "提示词注入与本地化。",
    icon: "icon-[mdi--format-quote-close]",
  },
  {
    id: "overlay",
    value: "overlay",
    label: "浮窗",
    nav: "common",
    description: "桌面浮窗样式与行为。",
    icon: "icon-[mdi--monitor]",
  },
  {
    id: "history",
    value: "history",
    label: "历史与日志",
    nav: "more",
    description: "本地历史记录管理、日志导出与清理。",
    icon: "icon-[mdi--history]",
  },
  {
    id: "advanced",
    value: "advanced",
    label: "高级",
    nav: "more",
    description: "高风险或低频系统设置。",
    icon: "icon-[mdi--tools]",
  },
  {
    id: "about",
    value: "about",
    label: "关于",
    nav: "more",
    description: "致谢与开源信息。",
    icon: "icon-[mdi--information-outline]",
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
  about: AboutSettings,
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

// 侧边栏收缩状态持久化：折叠为图标窄栏后刷新仍保持。
export const SETTINGS_SIDEBAR_COLLAPSED_KEY = "cursor-byok.settings.sidebar-collapsed";

export function readStoredSidebarCollapsed() {
  if (typeof window === "undefined" || !window.localStorage) {
    return false;
  }
  return window.localStorage.getItem(SETTINGS_SIDEBAR_COLLAPSED_KEY) === "true";
}

export function writeStoredSidebarCollapsed(collapsed) {
  if (typeof window === "undefined" || !window.localStorage) {
    return;
  }
  window.localStorage.setItem(SETTINGS_SIDEBAR_COLLAPSED_KEY, collapsed ? "true" : "false");
}