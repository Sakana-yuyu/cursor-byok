/** 新建模型时经路由进入编辑器的预填种子（含 apiKey，走 sessionStorage 不进 URL） */
export const MODEL_EDITOR_SEED_KEY = "cursor-byok.modelEditorSeed";

export function stashModelEditorSeed(seed) {
  if (!seed) return;
  try {
    sessionStorage.setItem(MODEL_EDITOR_SEED_KEY, JSON.stringify(seed));
  } catch {
    /* ignore */
  }
}

export function popModelEditorSeed() {
  try {
    const raw = sessionStorage.getItem(MODEL_EDITOR_SEED_KEY);
    sessionStorage.removeItem(MODEL_EDITOR_SEED_KEY);
    return raw ? JSON.parse(raw) : null;
  } catch {
    return null;
  }
}

/** 去「拉取模型」页前的草稿暂存：返回编辑器时回填未保存的填写内容。 */
export const MODEL_EDITOR_RETURN_KEY = "cursor-byok.modelEditorReturn";
const MODEL_EDITOR_RETURN_TTL_MS = 30 * 60 * 1000;

export function stashModelEditorReturn(draft, editorIndex) {
  if (!draft) return;
  try {
    sessionStorage.setItem(
      MODEL_EDITOR_RETURN_KEY,
      JSON.stringify({ draft, editorIndex, stashedAt: Date.now() }),
    );
  } catch {
    /* ignore */
  }
}

export function popModelEditorReturn() {
  try {
    const raw = sessionStorage.getItem(MODEL_EDITOR_RETURN_KEY);
    sessionStorage.removeItem(MODEL_EDITOR_RETURN_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object" || !parsed.draft) return null;
    if (Date.now() - Number(parsed.stashedAt || 0) > MODEL_EDITOR_RETURN_TTL_MS) return null;
    return parsed;
  } catch {
    return null;
  }
}
