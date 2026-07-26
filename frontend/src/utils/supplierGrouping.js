/** 供应商列表分组：按名称 / 按连接（规范化 baseURL） */

export const SUPPLIER_GROUP_MODE_NAME = "name";
export const SUPPLIER_GROUP_MODE_CONNECTION = "connection";
export const SUPPLIER_GROUP_MODE_LEGACY = "legacy"; // baseURL + groupName（兼容旧链接）

const STORAGE_KEY = "cursor-byok.supplierGroupMode";

export function normalizeSupplierGroupMode(value) {
  const mode = String(value || "").trim().toLowerCase();
  if (mode === SUPPLIER_GROUP_MODE_NAME || mode === SUPPLIER_GROUP_MODE_CONNECTION) {
    return mode;
  }
  if (mode === SUPPLIER_GROUP_MODE_LEGACY) {
    return SUPPLIER_GROUP_MODE_LEGACY;
  }
  return SUPPLIER_GROUP_MODE_CONNECTION;
}

export function loadSupplierGroupMode() {
  try {
    return normalizeSupplierGroupMode(localStorage.getItem(STORAGE_KEY));
  } catch {
    return SUPPLIER_GROUP_MODE_CONNECTION;
  }
}

export function saveSupplierGroupMode(mode) {
  const normalized = normalizeSupplierGroupMode(mode);
  try {
    if (
      normalized === SUPPLIER_GROUP_MODE_NAME ||
      normalized === SUPPLIER_GROUP_MODE_CONNECTION
    ) {
      localStorage.setItem(STORAGE_KEY, normalized);
    }
  } catch {
    /* ignore */
  }
  return normalized;
}

/** 与 appState.normalizeBaseURL 语义对齐：小写 host、去尾斜杠 */
export function normalizeSupplierBaseURL(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  try {
    const parsed = new URL(text);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
      return text;
    }
    parsed.protocol = parsed.protocol.toLowerCase();
    parsed.hostname = parsed.hostname.toLowerCase();
    const normalized = parsed.toString().replace(/\/+$/, "");
    return normalized || parsed.toString();
  } catch {
    return text.replace(/\/+$/, "");
  }
}

export function displayGroupName(groupName) {
  const name = String(groupName || "").trim();
  return name || "默认分组";
}

export function rawGroupNameFromDisplay(display) {
  const name = String(display || "").trim();
  return name === "默认分组" ? "" : name;
}

/**
 * 将 modelAdapters 聚合成供应商卡片列表。
 * @param {Array} adapters
 * @param {'name'|'connection'|'legacy'} mode
 * @returns {Array<{ key, mode, baseURL, groupName, type, apiKey, customHeadersEnabled, customHeadersJSON, models }>}
 */
export function groupModelAdaptersAsSuppliers(adapters, mode = SUPPLIER_GROUP_MODE_CONNECTION) {
  const resolved = normalizeSupplierGroupMode(mode);
  const map = new Map();

  for (const adapter of adapters || []) {
    const baseURL = normalizeSupplierBaseURL(adapter.baseURL);
    const groupNameRaw = String(adapter.groupName || "").trim();
    const groupNameDisplay = displayGroupName(groupNameRaw);

    let key;
    if (resolved === SUPPLIER_GROUP_MODE_NAME) {
      key = `name::${groupNameRaw}`;
    } else if (resolved === SUPPLIER_GROUP_MODE_LEGACY) {
      key = `legacy::${baseURL}::${groupNameRaw}`;
    } else {
      key = `connection::${baseURL}`;
    }

    if (!map.has(key)) {
      map.set(key, {
        key,
        mode: resolved,
        baseURL,
        groupName: groupNameDisplay,
        groupNameRaw,
        type: adapter.type,
        apiKey: adapter.apiKey,
        customHeadersEnabled: adapter.customHeadersEnabled,
        customHeadersJSON: adapter.customHeadersJSON,
        models: [],
      });
    }
    const bucket = map.get(key);
    bucket.models.push(adapter);
    // 连接分组：展示用名称取首条非空分组名；多名称时仍以首条为主
    if (resolved === SUPPLIER_GROUP_MODE_CONNECTION && !bucket.groupNameRaw && groupNameRaw) {
      bucket.groupNameRaw = groupNameRaw;
      bucket.groupName = groupNameDisplay;
    }
  }

  return Array.from(map.values());
}

/**
 * 按当前分组模式判断 adapter 是否属于某供应商身份（路由 query / 删除条件）。
 * identity: { mode, baseURL?, groupName? }
 */
export function adapterMatchesSupplierIdentity(adapter, identity) {
  if (!adapter || !identity) return false;
  const mode = normalizeSupplierGroupMode(identity.mode || SUPPLIER_GROUP_MODE_LEGACY);
  const adapterBase = normalizeSupplierBaseURL(adapter.baseURL);
  const adapterGroup = String(adapter.groupName || "").trim();
  const idBase = normalizeSupplierBaseURL(identity.baseURL);
  const idGroup = String(identity.groupName || "").trim();

  if (mode === SUPPLIER_GROUP_MODE_NAME) {
    return adapterGroup === idGroup;
  }
  if (mode === SUPPLIER_GROUP_MODE_CONNECTION) {
    return adapterBase === idBase;
  }
  // legacy: baseURL + groupName
  return adapterBase === idBase && adapterGroup === idGroup;
}

export function supplierToRouteQuery(supplier) {
  const mode = normalizeSupplierGroupMode(supplier?.mode || SUPPLIER_GROUP_MODE_CONNECTION);
  const query = { mode };
  if (mode === SUPPLIER_GROUP_MODE_NAME) {
    query.groupName = supplier.groupNameRaw ?? rawGroupNameFromDisplay(supplier.groupName);
  } else if (mode === SUPPLIER_GROUP_MODE_CONNECTION) {
    query.baseURL = supplier.baseURL || "";
  } else {
    query.baseURL = supplier.baseURL || "";
    query.groupName = supplier.groupNameRaw ?? rawGroupNameFromDisplay(supplier.groupName);
  }
  return query;
}

export function supplierIdentityFromRouteQuery(query) {
  const q = query || {};
  let mode = String(q.mode || "").trim().toLowerCase();
  if (!mode) {
    // 旧链接无 mode：按 baseURL+groupName 兼容
    mode = SUPPLIER_GROUP_MODE_LEGACY;
  } else {
    mode = normalizeSupplierGroupMode(mode);
  }
  return {
    mode,
    baseURL: String(q.baseURL || "").trim(),
    groupName: String(q.groupName || "").trim(),
  };
}