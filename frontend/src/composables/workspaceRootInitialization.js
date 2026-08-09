function normalizeWorkspaceRoot(value) {
  return String(value || "").trim();
}

export async function initializeWorkspaceRootValue({
  workspaceRoot,
  loadRecentRoot,
  readStoredRoot,
  getMutationRevision,
  commitRoot,
}) {
  const initialValue = normalizeWorkspaceRoot(workspaceRoot.value);
  const initialRevision = getMutationRevision();
  let recentRoot = "";

  try {
    recentRoot = await loadRecentRoot();
  } catch {
    recentRoot = "";
  }

  const currentValue = normalizeWorkspaceRoot(workspaceRoot.value);
  if (getMutationRevision() !== initialRevision || currentValue !== initialValue) {
    return currentValue;
  }

  return commitRoot(normalizeWorkspaceRoot(recentRoot) || normalizeWorkspaceRoot(readStoredRoot()));
}
