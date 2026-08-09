import { reactive } from "vue";
import { GetState } from "@bindings/cursor/internal/bridge/proxyservice.js";
import { isBrowserPreview } from "@/services/runtimeAdapter";
import { normalizeClientError } from "@/utils/errorContract";
import {
  AVAILABLE_RUNTIME_HEALTH,
  transitionRuntimeConnecting,
  transitionRuntimeFailure,
  transitionRuntimeOffline,
  transitionRuntimeSuccess,
} from "@/services/runtimeHealthModel";

const PROBE_TIMEOUT_MS = 8_000;
const NOTICE_DELAY_MS = 1_500;

export const runtimeHealthState = reactive({
  ...AVAILABLE_RUNTIME_HEALTH,
  noticeVisible: false,
});

let retryTimer = 0;
let noticeTimer = 0;
let probePromise = null;
let started = false;

function clearTimer(timer) {
  if (timer && typeof window !== "undefined") window.clearTimeout(timer);
}

function clearScheduledRetry() {
  clearTimer(retryTimer);
  retryTimer = 0;
}

function hideNotice() {
  clearTimer(noticeTimer);
  noticeTimer = 0;
  runtimeHealthState.noticeVisible = false;
}

function scheduleNotice() {
  if (runtimeHealthState.noticeVisible || noticeTimer || typeof window === "undefined") return;
  noticeTimer = window.setTimeout(() => {
    noticeTimer = 0;
    if (["offline", "reconnecting", "blocked"].includes(runtimeHealthState.phase)) {
      runtimeHealthState.noticeVisible = true;
    }
  }, NOTICE_DELAY_MS);
}

function applyState(next) {
  Object.assign(runtimeHealthState, next);
}

function isOffline() {
  return typeof navigator !== "undefined" && navigator.onLine === false;
}

function withProbeTimeout(promise) {
  if (typeof window === "undefined") return promise;
  let timer = 0;
  const timeout = new Promise((_, reject) => {
    timer = window.setTimeout(() => reject(new Error("runtime health probe timeout")), PROBE_TIMEOUT_MS);
  });
  return Promise.race([promise, timeout]).finally(() => {
    if (timer) window.clearTimeout(timer);
  });
}

function scheduleRetry() {
  clearScheduledRetry();
  if (typeof window === "undefined" || runtimeHealthState.phase !== "reconnecting") return;
  const delay = Math.max(0, Number(runtimeHealthState.retryAt || 0) - Date.now());
  retryTimer = window.setTimeout(() => {
    retryTimer = 0;
    void probeRuntimeHealth();
  }, delay);
}

export function reportRuntimeOperationFailure(error) {
  if (isBrowserPreview) return;
  const normalized = error?.disposition ? error : normalizeClientError(error, { operation: "runtime.health" });
  clearScheduledRetry();
  if (isOffline()) {
    applyState(transitionRuntimeOffline(runtimeHealthState));
    runtimeHealthState.lastFailure = normalized;
    runtimeHealthState.noticeVisible = true;
    return;
  }
  applyState(transitionRuntimeFailure(runtimeHealthState, normalized));
  if (runtimeHealthState.phase === "reconnecting") {
    scheduleNotice();
    scheduleRetry();
  } else {
    runtimeHealthState.noticeVisible = true;
  }
}

export function reportRuntimeOperationSuccess() {
  if (isBrowserPreview) return;
  clearScheduledRetry();
  hideNotice();
  applyState(transitionRuntimeSuccess(runtimeHealthState));
}

export function probeRuntimeHealth({ immediate = false } = {}) {
  if (isBrowserPreview) {
    applyState(transitionRuntimeSuccess(runtimeHealthState));
    return Promise.resolve(true);
  }
  if (probePromise) return probePromise;
  if (isOffline()) {
    applyState(transitionRuntimeOffline(runtimeHealthState));
    runtimeHealthState.noticeVisible = true;
    return Promise.resolve(false);
  }

  clearScheduledRetry();
  if (immediate) runtimeHealthState.attempt = 0;
  applyState(transitionRuntimeConnecting(runtimeHealthState));
  probePromise = withProbeTimeout(GetState())
    .then(() => {
      reportRuntimeOperationSuccess();
      return true;
    })
    .catch((error) => {
      reportRuntimeOperationFailure(normalizeClientError(error, { operation: "GetState" }));
      return false;
    })
    .finally(() => {
      probePromise = null;
    });
  return probePromise;
}

function handleOnline() {
  void probeRuntimeHealth({ immediate: true });
}

function handleOffline() {
  clearScheduledRetry();
  applyState(transitionRuntimeOffline(runtimeHealthState));
  runtimeHealthState.noticeVisible = true;
}

function handleVisibilityChange() {
  if (document.visibilityState === "visible" && runtimeHealthState.phase !== "connected") {
    void probeRuntimeHealth({ immediate: true });
  }
}

export function startRuntimeHealthSupervisor() {
  if (started || typeof window === "undefined" || isBrowserPreview) return;
  started = true;
  window.addEventListener("online", handleOnline);
  window.addEventListener("offline", handleOffline);
  document.addEventListener("visibilitychange", handleVisibilityChange);
}

export function stopRuntimeHealthSupervisor() {
  if (!started || typeof window === "undefined") return;
  started = false;
  clearScheduledRetry();
  hideNotice();
  window.removeEventListener("online", handleOnline);
  window.removeEventListener("offline", handleOffline);
  document.removeEventListener("visibilitychange", handleVisibilityChange);
}