function finiteNumber(value) {
  if (value === null || value === undefined || value === "") return null;
  const number = Number(value);
  return Number.isFinite(number) ? number : null;
}

function clampFraction(value) {
  const number = finiteNumber(value);
  if (number === null) return null;
  return Math.min(1, Math.max(0, number));
}

function fractionFromAmount(value, limit) {
  const amount = finiteNumber(value);
  const maximum = finiteNumber(limit);
  if (amount === null || maximum === null || maximum <= 0) return null;
  return clampFraction(amount / maximum);
}

function derivedStatus(usedFraction) {
  if (usedFraction === null) return "unknown";
  if (usedFraction >= 1) return "exhausted";
  if (usedFraction >= 0.8) return "warning";
  return "ok";
}

function semanticWindowLabel(id, fallback, index) {
  if (id === "7d") return "周限额";
  const duration = id.match(/^(\d+)([hmds])$/);
  if (duration) {
    const value = duration[1];
    if (duration[2] === "h") return `${value}小时`;
    if (duration[2] === "m") return `${value}分钟`;
    if (duration[2] === "d") return `${value}天`;
    if (duration[2] === "s") return `${value}秒`;
  }
  return fallback || `额度窗口 ${index + 1}`;
}

export function normalizeProviderUsageWindows(windows) {
  if (!Array.isArray(windows)) return [];
  return windows
    .filter((window) => window && typeof window === "object")
    .map((window, index) => {
      const limit = finiteNumber(window.limit);
      const used = finiteNumber(window.used);
      const remaining = finiteNumber(window.remaining);
      let usedFraction = clampFraction(window.usedFraction);
      let remainingFraction = clampFraction(window.remainingFraction);

      if (usedFraction === null) usedFraction = fractionFromAmount(used, limit);
      if (remainingFraction === null) remainingFraction = fractionFromAmount(remaining, limit);
      if (usedFraction === null && remainingFraction !== null) usedFraction = 1 - remainingFraction;
      if (remainingFraction === null && usedFraction !== null) remainingFraction = 1 - usedFraction;

      const status = ["ok", "warning", "exhausted", "unknown"].includes(window.status)
        ? window.status
        : derivedStatus(usedFraction);
      const id = String(window.id || `window-${index + 1}`).trim() || `window-${index + 1}`;
      const fallbackLabel = String(window.label || "").trim();
      return {
        id,
        label: semanticWindowLabel(id.toLowerCase(), fallbackLabel, index),
        unit: String(window.unit || "").trim(),
        used,
        limit,
        remaining,
        usedFraction,
        remainingFraction,
        usedPercent: usedFraction === null ? null : usedFraction * 100,
        remainingPercent: remainingFraction === null ? null : remainingFraction * 100,
        resetsAt: String(window.resetsAt || "").trim(),
        status,
      };
    });
}

export function formatProviderUsageWindowReset(resetsAt, locale, timeZone) {
  const target = new Date(String(resetsAt || "").trim());
  if (!Number.isFinite(target.getTime())) return "";
  return new Intl.DateTimeFormat(locale || undefined, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
    ...(timeZone ? { timeZone } : {}),
  }).format(target);
}

export function formatProviderUsageWindowPercent(value) {
  const number = finiteNumber(value);
  if (number === null) return "—";
  return `${Math.round(Math.min(100, Math.max(0, number)))}%`;
}
