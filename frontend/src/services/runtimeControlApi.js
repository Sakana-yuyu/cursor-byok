import {
  CancelDelegationTask,
  CancelMCPServerConnection,
  ConnectMCPServer,
  DisconnectMCPServer,
  GetDelegationTaskSnapshots,
  GetSkillsMCPScanSnapshot,
} from "@bindings/cursor/internal/bridge/proxyservice.js";
import { getDelegationConfig as getDelegationConfigBinding, saveDelegationConfig as saveDelegationConfigBinding } from "@/services/clientApi";

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

export function getDelegationConfig() {
  return getDelegationConfigBinding().then((config) => ({
    ...(config || {}),
    supervision: normalizeSupervisionConfig(config?.supervision),
    visionDelegation: normalizeVisionDelegationConfig(config?.visionDelegation),
  }));
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
