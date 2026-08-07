import { asString } from "@/utils/valueCast";

const CURSOR_LAUNCH_PREFERENCES_KEY = "cursor-byok.cursor-launch.preferences";

function normalizeCursorManualPath(value) {
  return asString(value).replace(/^"|"$/g, "").trim();
}

export function getCursorManualPath() {
  try {
    const stored = localStorage.getItem(CURSOR_LAUNCH_PREFERENCES_KEY);
    const parsed = stored ? JSON.parse(stored) : {};
    return normalizeCursorManualPath(parsed?.manualPath);
  } catch {
    return "";
  }
}

export function setCursorManualPath(manualPath) {
  const normalized = normalizeCursorManualPath(manualPath);
  try {
    localStorage.setItem(CURSOR_LAUNCH_PREFERENCES_KEY, JSON.stringify({ manualPath: normalized }));
  } catch { /* localStorage 不可用时仅保留当前调用结果 */ }
  return normalized;
}
