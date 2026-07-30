import {
  CancelDelegationTask,
  CancelMCPServerConnection,
  ConnectMCPServer,
  DisconnectMCPServer,
  GetDelegationTaskSnapshots,
  GetSkillsMCPScanSnapshot,
} from "@bindings/cursor/internal/bridge/proxyservice.js";

export function getDelegationTaskSnapshots() {
  return GetDelegationTaskSnapshots();
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
