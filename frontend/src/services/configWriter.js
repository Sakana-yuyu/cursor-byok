function copyPatch(value) {
  if (Array.isArray(value)) return value.map(copyPatch);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, copyPatch(item)]));
  }
  return value;
}

// Arrays are replacements; object patches preserve unrelated nested settings.
export function mergeConfigPatch(current, patch) {
  const merged = { ...current };
  for (const [key, value] of Object.entries(patch)) {
    merged[key] = value && typeof value === "object" && !Array.isArray(value)
      ? mergeConfigPatch(current?.[key] ?? {}, value)
      : value;
  }
  return merged;
}

// One dependency object owns the write boundary, not a snapshot of reactive state.
export function createConfigWriter({ load, save, prepare, apply, setSaving, toError }) {
  let tail = Promise.resolve();

  function transaction(operation) {
    const pending = tail.catch(() => {}).then(async () => {
      try {
        const config = await load();
        const persist = async (patch, options = {}) => {
          const { payload, error } = prepare(mergeConfigPatch(config, patch));
          if (error) return { ok: false, error };
          setSaving(true);
          try {
            await save(payload);
            apply(await load(), options);
            return { ok: true, error: "" };
          } finally {
            setSaving(false);
          }
        };
        return await operation({ config, persist });
      } catch (error) {
        return { ok: false, error: toError(error) };
      }
    });
    tail = pending.catch(() => {});
    return pending;
  }

  function patch(changes, options) {
    const requested = typeof changes === "function" ? changes : copyPatch(changes);
    return transaction(({ config, persist }) => persist(
      typeof requested === "function" ? requested(config) : requested,
      options,
    ));
  }

  return { patch, transaction };
}