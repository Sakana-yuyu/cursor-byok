import assert from "node:assert/strict";
import test from "node:test";

import { initializeWorkspaceRootValue } from "./workspaceRootInitialization.js";

function deferred() {
  let resolve;
  const promise = new Promise((next) => {
    resolve = next;
  });
  return { promise, resolve };
}

function initializationHarness(initialValue = "E:\\stored-workspace") {
  const recentRoot = deferred();
  const workspaceRoot = { value: initialValue };
  const persisted = [];
  let mutationRevision = 0;

  return {
    recentRoot,
    workspaceRoot,
    persisted,
    markExplicitMutation() {
      mutationRevision += 1;
    },
    initialize() {
      return initializeWorkspaceRootValue({
        workspaceRoot,
        loadRecentRoot: () => recentRoot.promise,
        readStoredRoot: () => initialValue,
        getMutationRevision: () => mutationRevision,
        commitRoot: (value) => {
          workspaceRoot.value = value;
          persisted.push(value);
          return value;
        },
      });
    },
  };
}

test("workspace initialization preserves a direct edit while the backend root is loading", async () => {
  const harness = initializationHarness();
  const initialization = harness.initialize();

  harness.workspaceRoot.value = "E:\\manual-workspace";
  harness.recentRoot.resolve("E:\\recent-workspace");

  assert.equal(await initialization, "E:\\manual-workspace");
  assert.deepEqual(harness.persisted, []);
});

test("workspace initialization preserves an explicit update even when its text is unchanged", async () => {
  const harness = initializationHarness();
  const initialization = harness.initialize();

  harness.markExplicitMutation();
  harness.recentRoot.resolve("E:\\recent-workspace");

  assert.equal(await initialization, "E:\\stored-workspace");
  assert.deepEqual(harness.persisted, []);
});
