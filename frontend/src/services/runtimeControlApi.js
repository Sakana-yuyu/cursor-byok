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

function normalizeDelegationTaskSnapshot(snapshot) {
  const phase = String(snapshot?.supervisionPhase || snapshot?.supervisionStatus || snapshot?.phase || "").trim();
  const workerRole = String(snapshot?.workerRole || snapshot?.role || "").trim();
  const progressSummary = String(snapshot?.progressSummary || snapshot?.checkpoint?.progressSummary || "").trim();
  const issueCategory = String(snapshot?.issueCategory || snapshot?.supervisionIssueCode || "").trim();
  const correctionCount = positiveInteger(snapshot?.correctionCount);
  const retryCount = positiveInteger(snapshot?.retryCount);
  const reassignCount = positiveInteger(snapshot?.reassignCount);
  const escalateCount = positiveInteger(snapshot?.escalateCount) || (snapshot?.escalated ? 1 : 0);
  const supervisionRound = positiveInteger(snapshot?.supervisionRound || snapshot?.checkpoint?.round);
  return {
    ...snapshot,
    supervisionPhase: phase,
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
