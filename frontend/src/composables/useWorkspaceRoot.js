import { getRecentWorkspaceRoot } from "@/services/clientApi";
import { ref } from "vue";
import { initializeWorkspaceRootValue } from "./workspaceRootInitialization.js";

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
let mutationRevision = 0;

function persistWorkspaceRoot(value) {
  try {
    window.localStorage.setItem(WORKSPACE_ROOT_STORAGE_KEY, value);
  } catch {
    // Storage is optional; the shared runtime value remains usable.
  }
}

function commitWorkspaceRoot(value) {
  const normalized = normalizeWorkspaceRoot(value);
  workspaceRoot.value = normalized;
  persistWorkspaceRoot(normalized);
  return normalized;
}

function updateWorkspaceRoot(value) {
  mutationRevision += 1;
  return commitWorkspaceRoot(value);
}

function initializeWorkspaceRoot() {
  if (!initializationPromise) {
    initializationPromise = initializeWorkspaceRootValue({
      workspaceRoot,
      loadRecentRoot: getRecentWorkspaceRoot,
      readStoredRoot: readStoredWorkspaceRoot,
      getMutationRevision: () => mutationRevision,
      commitRoot: commitWorkspaceRoot,
    })
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
