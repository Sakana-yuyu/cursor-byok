const RETRY_DELAYS_MS = [1_000, 2_000, 5_000, 10_000, 30_000];

export const AVAILABLE_RUNTIME_HEALTH = Object.freeze({
  phase: "available",
  attempt: 0,
  retryAt: 0,
  lastFailure: null,
  lastConnectedAt: 0,
});

export function runtimeRetryDelay(attempt) {
  const index = Math.max(0, Math.min(RETRY_DELAYS_MS.length - 1, Number(attempt || 1) - 1));
  return RETRY_DELAYS_MS[index];
}

export function transitionRuntimeFailure(current, failure, now = Date.now()) {
  const previous = current || AVAILABLE_RUNTIME_HEALTH;
  if (failure?.disposition === "blocked" || failure?.disposition === "fatal") {
    return {
      ...previous,
      phase: "blocked",
      retryAt: 0,
      lastFailure: failure || null,
    };
  }
  const attempt = Math.max(0, Number(previous.attempt || 0)) + 1;
  return {
    ...previous,
    phase: "reconnecting",
    attempt,
    retryAt: now + runtimeRetryDelay(attempt),
    lastFailure: failure || null,
  };
}

export function transitionRuntimeOffline(current) {
  const previous = current || AVAILABLE_RUNTIME_HEALTH;
  return {
    ...previous,
    phase: "offline",
    retryAt: 0,
  };
}

export function transitionRuntimeConnecting(current) {
  const previous = current || AVAILABLE_RUNTIME_HEALTH;
  return {
    ...previous,
    phase: previous.attempt > 0 ? "reconnecting" : "connecting",
    retryAt: 0,
  };
}

export function transitionRuntimeSuccess(current, now = Date.now()) {
  const previous = current || AVAILABLE_RUNTIME_HEALTH;
  return {
    ...previous,
    phase: "connected",
    attempt: 0,
    retryAt: 0,
    lastFailure: null,
    lastConnectedAt: now,
  };
}