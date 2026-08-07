const INTEGER_FORMATTER = new Intl.NumberFormat("en-US");

const COMPACT_UNITS = [
  { value: 1e18, suffix: "E" },
  { value: 1e15, suffix: "P" },
  { value: 1e12, suffix: "T" },
  { value: 1e9, suffix: "B" },
  { value: 1e6, suffix: "M" },
  { value: 1e3, suffix: "K" },
];

function normalizeInteger(value) {
  const number = Number(value);
  if (!Number.isFinite(number)) {
    return 0;
  }
  return Math.round(number);
}

function trimTrailingZeros(text) {
  return text.replace(/\.0$/, "").replace(/(\.\d*[1-9])0+$/, "$1");
}

export function formatInteger(value) {
  return INTEGER_FORMATTER.format(normalizeInteger(value));
}

export function formatCompactInteger(value) {
  const number = normalizeInteger(value);
  const absNumber = Math.abs(number);
  const unit = COMPACT_UNITS.find(({ value: threshold }) => absNumber >= threshold);

  if (!unit) {
    return formatInteger(number);
  }

  const scaled = number / unit.value;
  const fractionDigits = Math.abs(scaled) < 100 ? 1 : 0;

  return `${trimTrailingZeros(scaled.toFixed(fractionDigits))}${unit.suffix}`;
}
