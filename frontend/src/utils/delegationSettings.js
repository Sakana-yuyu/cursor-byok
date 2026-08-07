// Pure delegation config helpers and defaults (no component reactive state).

export const DEFAULT_SUPERVISION = {
  enabled: false,
  supervisorModelID: "",
  reviewerModelID: "",
  workerGroupID: "",
  maxCorrections: 2,
  maxRetries: 1,
  maxRounds: 8,
  allowReassign: false,
  allowEscalate: false,
  strictUnavailable: false,
};

export function createImmediateState() {
  return {
    busy: false,
    error: "",
    retry: null,
  };
}

export function createDraftState() {
  return {
    busy: false,
    queued: false,
    error: "",
  };
}

export function retryState(state) {
  if (typeof state.retry === "function") {
    void state.retry();
  }
}

export function groupNameAutosaveKey(groupID) {
  return `delegation.group.${groupID}.name`;
}

export function groupImmediateAutosaveKey(groupID, action) {
  return `delegation.group.${groupID}.${action}`;
}

export function normalizeMaxConcurrencyValue(value, committedValue) {
  const parsed = Number.parseInt(String(value || "").trim(), 10);
  const currentValue = Number(committedValue || 0);
  const fallback = currentValue > 0 ? currentValue : 4;
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}

export function togglePermission(group, permission, enabled) {
  const next = { ...(group.toolPermissions || {}) };
  for (const tool of permission.tools) {
    if (enabled) {
      delete next[tool];
    } else {
      next[tool] = false;
    }
  }
  group.toolPermissions = next;
}

export function toggleModel(group, modelID, enabled) {
  const next = new Set(group.modelIDs || []);
  if (enabled) {
    next.add(modelID);
  } else {
    next.delete(modelID);
  }
  group.modelIDs = [...next];
  if (!group.modelIDs.includes(group.defaultModelID)) {
    group.defaultModelID = group.modelIDs[0] || "";
  }
  if (group.modelIDs.length === 0) {
    group.enabled = false;
  }
}

export function clearStateError(state) {
  state.error = "";
}

export function normalizeSupervision(value) {
  const raw = value && typeof value === "object" ? value : {};
  const positive = (input, fallback) => {
    const parsed = Number.parseInt(input, 10);
    return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
  };
  return {
    enabled: Boolean(raw.enabled),
    supervisorModelID: String(raw.supervisorModelID || raw.supervisorModelId || "").trim(),
    reviewerModelID: String(raw.reviewerModelID || raw.reviewerModelId || "").trim(),
    workerGroupID: String(raw.workerGroupID || raw.workerGroupId || "").trim(),
    maxCorrections: positive(raw.maxCorrections, 2),
    maxRetries: positive(raw.maxRetries, 1),
    maxRounds: positive(raw.maxRounds, 8),
    allowReassign: Boolean(raw.allowReassign),
    allowEscalate: Boolean(raw.allowEscalate),
    strictUnavailable: Boolean(raw.strictUnavailable),
  };
}

export function cloneConfigValue(value) {
  if (Array.isArray(value)) {
    return value.map((item) => cloneConfigValue(item));
  }
  if (value && typeof value === "object") {
    return Object.fromEntries(
      Object.entries(value).map(([key, item]) => [key, cloneConfigValue(item)]),
    );
  }
  return value;
}

export function configValuesEqual(left, right) {
  if (Object.is(left, right)) return true;
  if (Array.isArray(left) || Array.isArray(right)) {
    if (!Array.isArray(left) || !Array.isArray(right) || left.length !== right.length) return false;
    return left.every((item, index) => configValuesEqual(item, right[index]));
  }
  if (!left || !right || typeof left !== "object" || typeof right !== "object") return false;
  const leftKeys = Object.keys(left);
  const rightKeys = Object.keys(right);
  if (leftKeys.length !== rightKeys.length) return false;
  return leftKeys.every((key) => (
    Object.prototype.hasOwnProperty.call(right, key)
    && configValuesEqual(left[key], right[key])
  ));
}

export function reconcileSavedObject(savedValue, submittedValue, currentValue) {
  const saved = savedValue && typeof savedValue === "object" ? savedValue : {};
  const submitted = submittedValue && typeof submittedValue === "object" ? submittedValue : {};
  const current = currentValue && typeof currentValue === "object" ? currentValue : {};
  const keys = new Set([...Object.keys(saved), ...Object.keys(submitted), ...Object.keys(current)]);
  const reconciled = {};
  for (const key of keys) {
    if (!configValuesEqual(current[key], submitted[key])) {
      reconciled[key] = cloneConfigValue(current[key]);
      continue;
    }
    if (Object.prototype.hasOwnProperty.call(saved, key)) {
      reconciled[key] = cloneConfigValue(saved[key]);
    }
  }
  return reconciled;
}

export function normalizeSubagentProfileRows(rows) {
  const seen = new Set();
  const result = [];
  for (const item of Array.isArray(rows) ? rows : []) {
    const subagentType = String(item?.subagentType || "").trim();
    if (!subagentType || seen.has(subagentType)) continue;
    seen.add(subagentType);
    result.push({ subagentType, promptFragment: String(item?.promptFragment || "").trim() });
  }
  return result;
}

export function normalizeSupervisionLimit(field, value) {
  const fallback = field === "maxCorrections"
    ? DEFAULT_SUPERVISION.maxCorrections
    : field === "maxRetries" ? DEFAULT_SUPERVISION.maxRetries : DEFAULT_SUPERVISION.maxRounds;
  const parsed = Number.parseInt(String(value || "").trim(), 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}

export function normalizeVisionDelegation(value) {
  const raw = value && typeof value === "object" ? value : {};
  const visionModelID = String(raw.visionModelID || raw.visionModelId || "").trim();
  const mode = String(raw.mode || "").trim().toLowerCase();
  return {
    enabled: visionModelID !== "" && Boolean(raw.enabled),
    visionModelID,
    mode: ["auto", "describe", "ocr"].includes(mode) ? mode : "auto",
  };
}

