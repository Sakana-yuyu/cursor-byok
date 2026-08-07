import { computed, onScopeDispose, ref } from "vue";

const DEFAULT_DEBOUNCE_MS = 500;

function createEntry() {
  return {
    timerID: null,
    revision: 0,
    lastSave: null,
    queuedCount: 0,
    pendingCount: 0,
    error: null,
  };
}

export function useSettingsAutosave() {
  const entries = new Map();
  const version = ref(0);

  function touch() {
    version.value += 1;
  }

  function ensureEntry(key) {
    const normalizedKey = String(key || "").trim();
    if (!normalizedKey) {
      throw new Error("Settings autosave key is required.");
    }

    if (!entries.has(normalizedKey)) {
      entries.set(normalizedKey, createEntry());
      touch();
    }

    return [normalizedKey, entries.get(normalizedKey)];
  }

  function clearTimer(entry) {
    let changed = false;
    if (entry.timerID !== null) {
      window.clearTimeout(entry.timerID);
      entry.timerID = null;
      changed = true;
    }
    if (entry.queuedCount > 0) {
      entry.queuedCount = 0;
      changed = true;
    }
    if (changed) {
      touch();
    }
  }

  async function run(key, save) {
    const [normalizedKey, entry] = ensureEntry(key);
    const saveCallback = save ?? entry.lastSave;

    if (typeof saveCallback !== "function") {
      return;
    }

    clearTimer(entry);
    entry.lastSave = saveCallback;
    entry.revision += 1;
    const currentRevision = entry.revision;
    entry.pendingCount += 1;
    touch();

    try {
      await saveCallback();
      if (entry.revision === currentRevision) {
        entry.error = null;
      }
    } catch (error) {
      if (entry.revision === currentRevision) {
        entry.error = error;
      }
      throw error;
    } finally {
      entry.pendingCount = Math.max(0, entry.pendingCount - 1);
      entries.set(normalizedKey, entry);
      touch();
    }
  }

  function schedule(key, save, options = {}) {
    const [normalizedKey, entry] = ensureEntry(key);
    clearTimer(entry);
    entry.lastSave = save;
    entry.revision += 1;
    entry.queuedCount = 1;
    touch();

    const debounceMs = Number.isFinite(options.debounceMs)
      ? Math.max(0, Number(options.debounceMs))
      : DEFAULT_DEBOUNCE_MS;

    entry.timerID = window.setTimeout(() => {
      entry.timerID = null;
      entry.queuedCount = 0;
      touch();
      void run(normalizedKey).catch(() => {});
    }, debounceMs);
  }

  async function retry(key) {
    const [, entry] = ensureEntry(key);
    if (typeof entry.lastSave !== "function") {
      return;
    }

    await run(key, entry.lastSave);
  }

  async function flush(key) {
    const keys = typeof key === "undefined"
      ? Array.from(entries.keys())
      : [String(key || "").trim()].filter(Boolean);

    const pendingRuns = [];
    for (const currentKey of keys) {
      const entry = entries.get(currentKey);
      if (!entry?.timerID) {
        continue;
      }

      clearTimer(entry);
      pendingRuns.push(run(currentKey, entry.lastSave));
    }

    await Promise.all(pendingRuns);
  }

  function setError(key, error) {
    const [, entry] = ensureEntry(key);
    entry.error = error;
    touch();
  }

  function clearError(key) {
    const [, entry] = ensureEntry(key);
    entry.error = null;
    touch();
  }

  const hasErrors = computed(() => {
    version.value;
    return Array.from(entries.values()).some((entry) => Boolean(entry.error));
  });

  const status = computed(() => {
    version.value;
    if (Array.from(entries.values()).some((entry) => entry.queuedCount > 0 || entry.pendingCount > 0)) {
      return "saving";
    }
    if (hasErrors.value) {
      return "error";
    }
    return "saved";
  });

  onScopeDispose(() => {
    for (const [key, entry] of entries.entries()) {
      if (entry.timerID !== null && typeof entry.lastSave === "function") {
        // 卸载前把未落盘的防抖保存立即发出，避免切页/切分类时静默丢弃。
        // Wails/HTTP 调用不依赖组件生命周期，fire-and-forget 可正常完成。
        void run(key, entry.lastSave).catch(() => {});
      } else {
        clearTimer(entry);
      }
    }
  });

  return {
    status,
    hasErrors,
    schedule,
    run,
    retry,
    flush,
    setError,
    clearError,
  };
}
