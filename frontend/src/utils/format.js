// format.js 承载跨组件复用的展示格式化函数。每个函数都是从原组件逐字
// 搬移（零行为变化），新组件接入时优先从这里 import 而不是再写一份。

export function currencySymbol(currency) {
  const code = String(currency || "").toUpperCase();
  if (code === "USD") return "$";
  if (code === "CNY" || code === "RMB") return "¥";
  if (code === "EUR") return "€";
  return "";
}

export function formatMoney(value, currency) {
  if (value == null || !Number.isFinite(Number(value))) return "—";
  const symbol = currencySymbol(currency);
  const num = Number(value).toFixed(2);
  return symbol ? `${symbol}${num}` : `${num} ${String(currency || "").toUpperCase()}`.trim();
}