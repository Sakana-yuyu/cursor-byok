import {
  CancelDelegationTask,
  CancelMCPServerConnection,
  ConnectMCPServer,
  DisconnectMCPServer,
  GetDelegationTaskSnapshots,
  GetSkillsMCPScanSnapshot,
} from "@bindings/cursor/internal/bridge/proxyservice.js";

function positiveInteger(value) {
  const next = Number.parseInt(value, 10);
  return Number.isFinite(next) && next > 0 ? next : 0;
}

const terminalTaskStatuses = new Set(["completed", "failed", "canceled", "timed_out"]);

function normalizeDelegationTaskSnapshot(snapshot) {
  const phase = String(snapshot?.supervisionPhase || snapshot?.supervisionStatus || snapshot?.phase || "").trim();
  const reviewPending = Boolean(snapshot?.reviewPending) || phase === "reviewing";
  const workerRole = String(snapshot?.workerRole || snapshot?.role || "").trim();
  const progressSummary = String(snapshot?.progressSummary || snapshot?.checkpoint?.progressSummary || "").trim();
  const issueCategory = String(snapshot?.issueCategory || snapshot?.supervisionIssueCode || "").trim();
  const correctionCount = positiveInteger(snapshot?.correctionCount);
  const retryCount = positiveInteger(snapshot?.retryCount);
  const reassignCount = positiveInteger(snapshot?.reassignCount);
  const escalateCount = positiveInteger(snapshot?.escalateCount) || (snapshot?.escalated ? 1 : 0);
  const supervisionRound = positiveInteger(snapshot?.supervisionRound || snapshot?.checkpoint?.round);
  const cancelable = typeof snapshot?.cancelable === "boolean"
    ? snapshot.cancelable
    : reviewPending || !terminalTaskStatuses.has(String(snapshot?.status || "").trim());
  return {
    ...snapshot,
    supervisionPhase: phase,
    reviewPending,
    cancelable,
    workerRole,
    supervisionRound,
    correctionCount,
    retryCount,
    reassignCount,
    escalateCount,
    issueCategory,
    progressSummary,
    isSupervised: Boolean(phase || workerRole || supervisionRound || correctionCount || retryCount || reassignCount || escalateCount || issueCategory || progressSummary),
  };
}

export function getDelegationTaskSnapshots() {
  return GetDelegationTaskSnapshots().then((items) => (
    Array.isArray(items) ? items.map((item) => normalizeDelegationTaskSnapshot(item)) : []
  ));
}

export function cancelDelegationTask(taskID) {
  return CancelDelegationTask(taskID);
}

export function getMCPRuntimeServers(workspaceRoot = "") {
  return GetSkillsMCPScanSnapshot(workspaceRoot).then((snapshot) => snapshot?.mcpServers || []);
}

export function connectMCPRuntimeServer(identifier, attemptID, workspaceRoot = "") {
  return ConnectMCPServer(workspaceRoot, identifier, attemptID);
}

export function disconnectMCPRuntimeServer(identifier, workspaceRoot = "") {
  return DisconnectMCPServer(workspaceRoot, identifier);
}

export function cancelMCPRuntimeConnection(identifier, attemptID) {
  return CancelMCPServerConnection(identifier, attemptID);
}
