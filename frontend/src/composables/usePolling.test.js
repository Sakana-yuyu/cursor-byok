import assert from "node:assert/strict";
import test from "node:test";

import { createPollingController } from "./usePolling.js";

function deferred() {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

function schedulerHarness() {
  const scheduled = [];
  const canceled = [];
  return {
    scheduled,
    canceled,
    schedule(callback, delay) {
      const handle = { callback, delay };
      scheduled.push(handle);
      return handle;
    },
    cancel(handle) {
      canceled.push(handle);
    },
  };
}

test("polling schedules the next run only after the current task settles", async () => {
  const firstRun = deferred();
  const harness = schedulerHarness();
  let calls = 0;
  const controller = createPollingController(
    () => {
      calls += 1;
      return firstRun.promise;
    },
    harness.schedule,
    harness.cancel,
    25,
  );

  controller.start({ immediate: true });
  assert.equal(calls, 1);
  assert.equal(harness.scheduled.length, 0);

  firstRun.resolve();
  await firstRun.promise;
  await Promise.resolve();

  assert.equal(harness.scheduled.length, 1);
  assert.equal(harness.scheduled[0].delay, 25);
  harness.scheduled[0].callback();
  assert.equal(calls, 2);
});

test("polling start is idempotent and stop blocks post-settlement scheduling", async () => {
  const firstRun = deferred();
  const harness = schedulerHarness();
  let calls = 0;
  const controller = createPollingController(
    () => {
      calls += 1;
      return firstRun.promise;
    },
    harness.schedule,
    harness.cancel,
    25,
  );

  controller.start({ immediate: true });
  controller.start({ immediate: true });
  assert.equal(calls, 1);

  controller.stop();
  firstRun.resolve();
  await firstRun.promise;
  await Promise.resolve();

  assert.equal(harness.scheduled.length, 0);
  assert.equal(calls, 1);
});

test("polling continues after a synchronous task failure", () => {
  const harness = schedulerHarness();
  const controller = createPollingController(
    () => {
      throw new Error("failed");
    },
    harness.schedule,
    harness.cancel,
    25,
  );

  controller.start({ immediate: true });

  assert.equal(harness.scheduled.length, 1);
  assert.equal(harness.scheduled[0].delay, 25);
});
