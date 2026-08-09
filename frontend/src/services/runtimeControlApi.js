import {
  CancelDelegationTask,
  CancelMCPServerConnection,
  ConnectMCPServer,
  DisconnectMCPServer,
  GetDelegationTaskSnapshots,
  GetSkillsMCPScanSnapshot,
  GetHistorySessions,
  DeleteHistorySessions,
  ClearHistory,
  DeleteHistoryDebugLogs,
  PurgeAllHistoryDebugLogs,
  GetHistoryDebugUsage,
  ExportSessionDebugBundle,
  ListSessionDebugFiles,
  ReadSessionDebugTail,
} from "@bindings/cursor/internal/bridge/proxyservice.js";
import { saveDelegationConfig as saveDelegationConfigBinding, invokeOperation } from "@/services/clientApi";

const DEFAULT_SUPERVISION = {
  enabled: false,
  supervisorModelID: "",
  reviewerModelID: "",
  workerGroupID: "",
  maxCorrections: 2,
  maxRetries: 1,
  maxRounds: 8,
  allowReassign: false,
  allowEscalate: false,
  strictUnavailable: false,
};

const DEFAULT_VISION_DELEGATION = {
  enabled: false,
  visionModelID: "",
  mode: "auto",
};

function normalizeSupervisionConfig(config) {
  const raw = config && typeof config === "object" ? config : {};
  const positive = (value, fallback) => {
    const parsed = Number.parseInt(value, 10);
    return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
  };
  return {
    ...DEFAULT_SUPERVISION,
    enabled: Boolean(raw.enabled),
    supervisorModelID: String(raw.supervisorModelID || raw.supervisorModelId || "").trim(),
    reviewerModelID: String(raw.reviewerModelID || raw.reviewerModelId || "").trim(),
    workerGroupID: String(raw.workerGroupID || raw.workerGroupId || "").trim(),
    maxCorrections: positive(raw.maxCorrections, 2),
    maxRetries: positive(raw.maxRetries, 1),
    maxRounds: positive(raw.maxRounds, 8),
    allowReassign: Boolean(raw.allowReassign),
    allowEscalate: Boolean(raw.allowEscalate),
    strictUnavailable: Boolean(raw.strictUnavailable),
  };
}

function normalizeVisionDelegationConfig(config) {
  const raw = config && typeof config === "object" ? config : {};
  const mode = String(raw.mode || "").trim().toLowerCase();
  const visionModelID = String(raw.visionModelID || raw.visionModelId || "").trim();
  return {
    ...DEFAULT_VISION_DELEGATION,
    enabled: visionModelID !== "" && Boolean(raw.enabled),
    visionModelID,
    mode: ["auto", "describe", "ocr"].includes(mode) ? mode : "auto",
  };
}

export function saveDelegationConfig(config) {
  return saveDelegationConfigBinding(config).then((saved) => ({
    ...(saved || {}),
    supervision: normalizeSupervisionConfig(saved?.supervision || config?.supervision),
    visionDelegation: normalizeVisionDelegationConfig(saved?.visionDelegation || config?.visionDelegation),
  }));
}

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
  return invokeOperation("GetDelegationTaskSnapshots", null, () => GetDelegationTaskSnapshots()).then((items) => (
    Array.isArray(items) ? items.map((item) => normalizeDelegationTaskSnapshot(item)) : []
  ));
}

export function cancelDelegationTask(taskID) {
  return invokeOperation("CancelDelegationTask", [taskID], () => CancelDelegationTask(taskID));
}

export function getMCPRuntimeServers(workspaceRoot = "") {
  return invokeOperation("GetSkillsMCPScanSnapshot", [workspaceRoot], () => GetSkillsMCPScanSnapshot(workspaceRoot))
    .then((snapshot) => snapshot?.mcpServers || []);
}

export function connectMCPRuntimeServer(identifier, attemptID, workspaceRoot = "") {
  return invokeOperation(
    "ConnectMCPServer",
    [workspaceRoot, identifier, attemptID],
    () => ConnectMCPServer(workspaceRoot, identifier, attemptID),
  );
}

export function disconnectMCPRuntimeServer(identifier, workspaceRoot = "") {
  return invokeOperation(
    "DisconnectMCPServer",
    [workspaceRoot, identifier],
    () => DisconnectMCPServer(workspaceRoot, identifier),
  );
}

export function cancelMCPRuntimeConnection(identifier, attemptID) {
  return invokeOperation(
    "CancelMCPServerConnection",
    [identifier, attemptID],
    () => CancelMCPServerConnection(identifier, attemptID),
  );
}

function normalizeHistorySession(session) {
  const raw = session && typeof session === "object" ? session : {};
  return {
    ...raw,
    id: String(raw.id || ""),
    createdAtUnixMs: Number(raw.createdAtUnixMs || 0),
    updatedAtUnixMs: Number(raw.updatedAtUnixMs || 0),
    sizeBytes: Number(raw.sizeBytes || 0),
    debugSizeBytes: Number(raw.debugSizeBytes || 0),
    subagentType: String(raw.subagentType || "").trim(),
    mode: String(raw.mode || "").trim(),
    title: String(raw.title || "").trim(),
    hasDebug: Boolean(raw.hasDebug),
    status: String(raw.status || "").trim(),
    requestId: String(raw.requestId || "").trim(),
  };
}

export function getHistorySessions() {
  return invokeOperation("GetHistorySessions", null, () => GetHistorySessions()).then((items) => (
    Array.isArray(items) ? items.map((item) => normalizeHistorySession(item)) : []
  ));
}

export function deleteHistorySessions(sessionIDs) {
  const ids = Array.isArray(sessionIDs) ? sessionIDs : [];
  return invokeOperation("DeleteHistorySessions", [ids], () => DeleteHistorySessions(ids));
}

export function clearHistory() {
  return invokeOperation("ClearHistory", null, () => ClearHistory()).then((count) => Number(count || 0));
}

// deleteHistoryDebugLogs 清理指定会话的调试日志，返回释放的字节数。
export function deleteHistoryDebugLogs(sessionIDs) {
  const ids = Array.isArray(sessionIDs) ? sessionIDs : [];
  return invokeOperation("DeleteHistoryDebugLogs", [ids], () => DeleteHistoryDebugLogs(ids))
    .then((bytes) => Number(bytes || 0));
}

// purgeAllHistoryDebugLogs 清理全部调试日志（含无会话归属的孤儿日志），返回释放的字节数。
// 由后端统一遍历目录，前端不需要先列出会话 ID，避免漏掉非 UUID 会话与孤儿日志。
export function purgeAllHistoryDebugLogs() {
  return invokeOperation("PurgeAllHistoryDebugLogs", null, () => PurgeAllHistoryDebugLogs())
    .then((bytes) => Number(bytes || 0));
}

export function getHistoryDebugUsage() {
  return invokeOperation("GetHistoryDebugUsage", null, () => GetHistoryDebugUsage())
    .then((bytes) => Number(bytes || 0));
}

function normalizeSessionDebugFile(file) {
  const raw = file && typeof file === "object" ? file : {};
  return {
    name: String(raw.name || ""),
    sizeBytes: Number(raw.sizeBytes || 0),
    modTimeUnixMs: Number(raw.modTimeUnixMs || 0),
  };
}

// listSessionDebugFiles 列出指定会话 debug 子目录下的文件元信息。
// debug 目录不存在时后端返回空切片。
export function listSessionDebugFiles(sessionID) {
  const id = String(sessionID || "");
  return invokeOperation("ListSessionDebugFiles", [id], () => ListSessionDebugFiles(id)).then((items) => (
    Array.isArray(items) ? items.map((item) => normalizeSessionDebugFile(item)) : []
  ));
}

// readSessionDebugTail 读取指定会话 debug 文件的尾部内容。
// maxBytes<=0 时后端使用默认 64KiB。
export function readSessionDebugTail(sessionID, filename, maxBytes = 0) {
  const id = String(sessionID || "");
  const name = String(filename || "");
  const size = Number(maxBytes || 0);
  return invokeOperation("ReadSessionDebugTail", [id, name, size], () => ReadSessionDebugTail(id, name, size))
    .then((text) => String(text || ""));
}

// exportSessionDebugBundle 打包指定会话的排查证据为 zip，返回 zip 文件路径。
export function exportSessionDebugBundle(sessionID) {
  const id = String(sessionID || "");
  return invokeOperation("ExportSessionDebugBundle", [id], () => ExportSessionDebugBundle(id))
    .then((path) => String(path || ""));
}
