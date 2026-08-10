const DEFAULT_FAILOVER_LIMIT = 3;
const DEFAULT_PROBE_TIMEOUT_SECONDS = 5;
const DEFAULT_EXECUTION_TIMEOUT_SECONDS = 120;
const MAX_PROBE_TIMEOUT_SECONDS = 30;
const MAX_EXECUTION_TIMEOUT_SECONDS = 7200;
const SENSITIVE_OPTION_TOKENS = new Set([
  "AUTH",
  "COOKIE",
  "CREDENTIAL",
  "KEY",
  "PASSWORD",
  "PRIVATE",
  "SECRET",
  "SESSION",
  "TOKEN",
]);
const COMPACT_SENSITIVE_OPTION_NAMES = new Set([
  "accesskey",
  "accesstoken",
  "apikey",
  "apitoken",
  "authkey",
  "authtoken",
  "privatekey",
  "refreshtoken",
  "secretkey",
  "sessionkey",
  "sessiontoken",
]);

function asObject(value) {
  return value && typeof value === "object" && !Array.isArray(value) ? value : {};
}

function asTrimmedString(value) {
  return typeof value === "string" ? value.trim() : "";
}

function boundedPositiveInteger(value, fallback, maximum) {
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed) || parsed <= 0) return fallback;
  return Math.min(parsed, maximum);
}

function optionNameTokens(value) {
  return asTrimmedString(value)
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/([A-Z]+)([A-Z][a-z])/g, "$1 $2")
    .split(/[^A-Za-z0-9]+/)
    .filter(Boolean)
    .map((token) => token.toUpperCase());
}

function isSensitiveOptionName(value) {
  const compact = asTrimmedString(value).replace(/[^A-Za-z0-9]+/g, "").toLowerCase();
  return COMPACT_SENSITIVE_OPTION_NAMES.has(compact)
    || optionNameTokens(value).some((token) => SENSITIVE_OPTION_TOKENS.has(token));
}

function normalizeEnvironmentVariables(value) {
  const names = Array.isArray(value) ? value : [];
  return [...new Set(names
    .map(asTrimmedString)
    .filter((name) => /^[A-Z_][A-Z0-9_]*$/.test(name)))]
    .sort();
}

function normalizeOptions(value) {
  const result = {};
  for (const [rawKey, rawValue] of Object.entries(asObject(value))) {
    const key = asTrimmedString(rawKey);
    if (!key || isSensitiveOptionName(key)) continue;
    result[key] = asTrimmedString(rawValue);
  }
  return result;
}

function normalizeExecutor(value) {
  const raw = asObject(value);
  const priority = Number.parseInt(raw.priority, 10);
  return {
    id: asTrimmedString(raw.id).toLowerCase(),
    kind: asTrimmedString(raw.kind).toLowerCase() || "builtin",
    displayName: asTrimmedString(raw.displayName),
    enabled: raw.enabled === true,
    priority: Number.isFinite(priority) && priority > 0 ? priority : 0,
    executable: asTrimmedString(raw.executable),
    probeTimeoutSeconds: boundedPositiveInteger(
      raw.probeTimeoutSeconds,
      DEFAULT_PROBE_TIMEOUT_SECONDS,
      MAX_PROBE_TIMEOUT_SECONDS,
    ),
    executionTimeoutSeconds: boundedPositiveInteger(
      raw.executionTimeoutSeconds,
      DEFAULT_EXECUTION_TIMEOUT_SECONDS,
      MAX_EXECUTION_TIMEOUT_SECONDS,
    ),
    environmentVariables: normalizeEnvironmentVariables(raw.environmentVariables),
    options: normalizeOptions(raw.options),
  };
}

export function normalizeDelegationExecutorPolicy(source) {
  const raw = asObject(source);
  const seen = new Set();
  const executors = [];
  for (const value of Array.isArray(raw.executors) ? raw.executors : []) {
    const executor = normalizeExecutor(value);
    if (!executor.id || seen.has(executor.id)) continue;
    seen.add(executor.id);
    executors.push(executor);
  }
  return {
    executorFailoverLimit: boundedPositiveInteger(
      raw.executorFailoverLimit,
      DEFAULT_FAILOVER_LIMIT,
      Number.MAX_SAFE_INTEGER,
    ),
    executors,
  };
}
