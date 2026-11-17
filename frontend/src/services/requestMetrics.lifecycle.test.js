import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { computed, effectScope, ref, watch } from "vue";
import { createPollingController } from "../composables/usePolling.js";

const source = (await readFile(new URL("../views/RequestMetrics.vue", import.meta.url), "utf8"))
  .match(/<script setup>([\s\S]*?)<\/script>/)[1]
  .replace(/^import .*;\r?$/gm, "");

const reloadSource = (await readFile(new URL("../state/appState.js", import.meta.url), "utf8"))
  .match(/export async function reloadUserConfig\(options = \{\}\) \{[\s\S]*?\n\}/)[0]
  .replace("export ", "");
const makeReloadUserConfig = new Function("loadPersistedUserConfig", "applyConfigToState", `${reloadSource}\nreturn reloadUserConfig;`);

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}

// Execute the actual script-setup body with lifecycle hooks and I/O supplied by
// the harness. Vue refs/watchers and the production polling controller stay real.
function harness({ config = Promise.resolve({}), counts = Promise.resolve(3), details = Promise.resolve([]) } = {}) {
  const mounted = [];
  const unmounted = [];
  const timers = new Set();
  const listeners = new Set();
  let detailCalls = 0;
  let configCommits = 0;
  const bindings = {
    computed, ref, watch,
    onMounted: (callback) => mounted.push(callback),
    onUnmounted: (callback) => unmounted.push(callback),
    useRouter: () => ({}),
    appState: { modelAdapters: [] },
    fetchRecentRequestMetricsCount: () => counts,
    fetchRecentRequestMetricsAbnormalCount: async () => 0,
    fetchRecentRequestMetricsDegradedCount: async () => 0,
    fetchRecentRequestMetrics: () => { detailCalls += 1; return details; },
    reloadUserConfig: makeReloadUserConfig(
      () => config,
      () => { configCommits += 1; },
    ),
    document: {
      hidden: false,
      addEventListener: (_name, callback) => listeners.add(callback),
      removeEventListener: (_name, callback) => listeners.delete(callback),
    },
    usePolling: (task, options) => createPollingController(task,
      (callback) => { timers.add(callback); return callback; },
      (callback) => timers.delete(callback), options.intervalMs),
  };
  const scope = effectScope();
  const state = scope.run(() => new Function(...Object.keys(bindings), `${source}\nreturn { rows, totalCount, loading, error };`)(...Object.values(bindings)));
  const initialization = mounted[0]();
  return {
    state, initialization, timers, listeners,
    detailCalls: () => detailCalls,
    configCommits: () => configCommits,
    unmount() { unmounted.forEach((callback) => callback()); scope.stop(); },
  };
}

const flush = async () => { for (let i = 0; i < 8; i += 1) await Promise.resolve(); };

test("leaving during slow config initialization cannot restart polling or visibility subscriptions", async () => {
  const config = deferred();
  const h = harness({ config: config.promise });
  await flush();
  h.unmount();
  config.resolve({});
  await h.initialization;
  assert.equal(h.timers.size, 0);
  assert.equal(h.listeners.size, 0);
  assert.equal(h.configCommits(), 0);
});

test("leaving during count loading discards counts and never begins detail loading", async () => {
  const counts = deferred();
  const h = harness({ counts: counts.promise });
  h.unmount();
  counts.resolve(123);
  await h.initialization;
  assert.equal(h.state.totalCount.value, 0);
  assert.equal(h.detailCalls(), 0);
  assert.equal(h.timers.size, 0);
  assert.equal(h.listeners.size, 0);
});

for (const outcome of ["resolve", "reject"]) {
  test(`leaving during detail loading discards late ${outcome} results`, async () => {
    const details = deferred();
    const h = harness({ details: details.promise });
    await flush();
    assert.equal(h.detailCalls(), 1);
    h.unmount();
    const previous = { rows: h.state.rows.value, error: h.state.error.value, loading: h.state.loading.value };
    if (outcome === "resolve") details.resolve([{ eventId: "late-result" }]);
    else details.reject(new Error("late-error"));
    await h.initialization;
    assert.equal(h.state.rows.value, previous.rows);
    assert.equal(h.state.error.value, previous.error);
    assert.equal(h.state.loading.value, previous.loading);
    assert.equal(h.timers.size, 0);
    assert.equal(h.listeners.size, 0);
  });
}

test("mounted initialization enables one timer and listener, both removed on leaving", async () => {
  const h = harness();
  await h.initialization;
  assert.equal(h.timers.size, 1);
  assert.equal(h.listeners.size, 1);
  h.unmount();
  assert.equal(h.timers.size, 0);
  assert.equal(h.listeners.size, 0);
});