import catalog from "../../../internal/modelcontext/models.json" with { type: "json" };

// Shared with Go: array order is first-match-wins. Patterns use the common
// JavaScript/Go regexp subset (no lookarounds, backreferences or inline flags).
const MODEL_CAPABILITIES = catalog.rules.map(({ pattern, ...capability }) => ({
  ...capability,
  pattern: new RegExp(pattern),
}));

function normalizeFullModelID(value) {
  return String(value || "")
    .trim()
    .toLowerCase()
    .replace(/^models\//, "")
    .replace(/[ _]/g, "-");
}

function normalizeModelID(value) {
  return normalizeFullModelID(value).replace(/^.*\//, "");
}

/** Return recognized capabilities independently of the configured channel limit. */
export function resolveModelCapabilities(modelID) {
  const fullID = normalizeFullModelID(modelID);
  if (!fullID) return null;
  const normalized = normalizeModelID(modelID);
  // Full provider IDs take priority; basename fallback is only for builtin rules,
  // never for merging entries returned by a provider catalog.
  const entry = MODEL_CAPABILITIES.find(({ pattern }) =>
    (!fullID.includes("/") || pattern.source.includes("/")) && pattern.test(fullID))
    || MODEL_CAPABILITIES.find(({ pattern }) => pattern.test(normalized));
  if (!entry) return null;
  const { pattern, ...rest } = entry;
  return {
    ...rest,
    reasoningEfforts: rest.reasoningEfforts ? [...rest.reasoningEfforts] : undefined,
    modelID: normalized,
    source: rest.source || "builtin_catalog",
    matchedPattern: pattern.source,
  };
}

/** Backwards-compatible context-only lookup. */
export function resolveModelContextWindow(modelID) {
  const c = resolveModelCapabilities(modelID);
  if (!c) return null;
  return { tokens: c.contextWindowTokens, source: c.source, label: c.displayName, modelID: c.modelID };
}

/** Explicit positive values (including legacy values of unknown origin) win. */
export function contextWindowTokensForModel(modelID, explicitValue) {
  const parsed = Number(explicitValue);
  if (Number.isSafeInteger(parsed) && parsed > 0) return parsed;
  return resolveModelCapabilities(modelID)?.contextWindowTokens || 0;
}

export function isModelCovered(modelID) {
  return resolveModelCapabilities(modelID) !== null;
}