import { getRecentWorkspaceRoot } from "@/services/clientApi";
import { ref } from "vue";

const WORKSPACE_ROOT_STORAGE_KEY = "cursor-byok-mcp-workspace-root";

function normalizeWorkspaceRoot(value) {
  return String(value || "").trim();
}

function readStoredWorkspaceRoot() {
  try {
    return normalizeWorkspaceRoot(window.localStorage.getItem(WORKSPACE_ROOT_STORAGE_KEY));
  } catch {
    return "";
  }
}

const workspaceRoot = ref(readStoredWorkspaceRoot());
let initializationPromise;

function persistWorkspaceRoot(value) {
  try {
    window.localStorage.setItem(WORKSPACE_ROOT_STORAGE_KEY, value);
  } catch {
    // Storage is optional; the shared runtime value remains usable.
  }
}

function updateWorkspaceRoot(value) {
  const normalized = normalizeWorkspaceRoot(value);
  workspaceRoot.value = normalized;
  persistWorkspaceRoot(normalized);
  return normalized;
}

function initializeWorkspaceRoot() {
  if (!initializationPromise) {
    initializationPromise = Promise.resolve()
      .then(() => getRecentWorkspaceRoot())
      .catch(() => "")
      .then((recentRoot) => updateWorkspaceRoot(
        normalizeWorkspaceRoot(recentRoot) || readStoredWorkspaceRoot(),
      ))
      .finally(() => {
        initializationPromise = null;
      });
  }
  return initializationPromise.then(() => workspaceRoot.value);
}

export function useWorkspaceRoot() {
  return {
    workspaceRoot,
    initializeWorkspaceRoot,
    updateWorkspaceRoot,
  };
}
