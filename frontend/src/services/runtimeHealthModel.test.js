import test from "node:test";
import assert from "node:assert/strict";

import {
  AVAILABLE_RUNTIME_HEALTH,
  runtimeRetryDelay,
  transitionRuntimeFailure,
  transitionRuntimeOffline,
  transitionRuntimeSuccess,
} from "./runtimeHealthModel.js";

const retryableFailure = {
  code: "network_error",
  kind: "network",
  disposition: "retryable",
  userMessage: "暂时无法连接服务",
  traceId: "trace-1",
};

test("retryable runtime failure enters reconnecting state", () => {
  const next = transitionRuntimeFailure(AVAILABLE_RUNTIME_HEALTH, retryableFailure, 1_000);

  assert.equal(next.phase, "reconnecting");
  assert.equal(next.attempt, 1);
  assert.equal(next.retryAt, 1_000 + runtimeRetryDelay(1));
  assert.equal(next.lastFailure.traceId, "trace-1");
});

test("blocked runtime failure stops retrying", () => {
  const next = transitionRuntimeFailure(AVAILABLE_RUNTIME_HEALTH, {
    ...retryableFailure,
    disposition: "blocked",
  }, 1_000);

  assert.equal(next.phase, "blocked");
  assert.equal(next.retryAt, 0);
});

test("offline state pauses retry ladder", () => {
  const reconnecting = transitionRuntimeFailure(AVAILABLE_RUNTIME_HEALTH, retryableFailure, 1_000);
  const offline = transitionRuntimeOffline(reconnecting);

  assert.equal(offline.phase, "offline");
  assert.equal(offline.retryAt, 0);
  assert.equal(offline.attempt, 1);
});

test("success clears failures and retry budget", () => {
  const reconnecting = transitionRuntimeFailure(AVAILABLE_RUNTIME_HEALTH, retryableFailure, 1_000);
  const connected = transitionRuntimeSuccess(reconnecting, 2_000);

  assert.equal(connected.phase, "connected");
  assert.equal(connected.attempt, 0);
  assert.equal(connected.retryAt, 0);
  assert.equal(connected.lastFailure, null);
  assert.equal(connected.lastConnectedAt, 2_000);
});

test("retry delay is bounded", () => {
  assert.equal(runtimeRetryDelay(1), 1_000);
  assert.equal(runtimeRetryDelay(3), 5_000);
  assert.equal(runtimeRetryDelay(99), 30_000);
});