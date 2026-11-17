import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import { createConfigWriter, mergeConfigPatch } from "./configWriter.js";

const appSource = await readFile(new URL("../state/appState.js", import.meta.url), "utf8");
const settingsSource = appSource.slice(
  appSource.indexOf("export async function saveAutoMatchContextWindow("),
  appSource.indexOf("export async function reloadUserConfig("),
).replaceAll("export async function", "async function");
const makeSettingsActions = new Function(
  "persistConfigPatch", "appState", "asBoolean", "normalizeRouteMode", "normalizeExternalAPI",
  `${settingsSource}\nreturn { saveAutoMatchContextWindow, saveRoutingMode, saveComputerUse, saveDebugLogEnabled, saveBillingQueryEnabled, saveExternalAPI, saveMirrorCaptureEnabled, saveMirrorCaptureProtocolFidelity };`,
);

function deferred() {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
}

function harness(initial = {}, overrides = {}) {
  let stored = structuredClone(initial);
  const writes = [];
  const applied = [];
  const saving = [];
  const writer = createConfigWriter({
    load: async () => structuredClone(stored),
    save: async (payload) => {
      await overrides.beforeSave?.(payload);
      stored = structuredClone(payload);
      writes.push(stored);
    },
    prepare: (config) => ({ payload: config, error: "" }),
    apply: (config, options) => applied.push({ config, options }),
    setSaving: (value) => saving.push(value),
    toError: (error) => error.message,
    ...overrides.dependencies,
  });
  return { writer, writes, applied, saving, stored: () => stored };
}

test("appState settings actions submit field patches without reading or resetting other settings", async () => {
  const h = harness({
    log: false,
    billingQuery: { enabled: true },
    computerUse: { mode: "desktop", browserStartURL: "about:blank" },
    externalAPI: { enabled: false, apiKey: "retained-key" },
    modelAdapters: [{ id: "latest-model" }],
    delegation: { groups: ["latest-group"] },
  });
  const actions = makeSettingsActions(h.writer.patch, structuredClone(h.stored()), Boolean, (mode) => mode, (value) => value);
  await Promise.all([
    actions.saveDebugLogEnabled(true),
    actions.saveBillingQueryEnabled(false),
    actions.saveComputerUse({ mode: "browser" }),
    actions.saveComputerUse({ browserStartURL: "https://example.com" }),
    actions.saveExternalAPI({ enabled: true }),
    actions.saveAutoMatchContextWindow(false),
  ]);
  assert.deepEqual(h.stored(), {
    log: true,
    billingQuery: { enabled: false },
    computerUse: { mode: "browser", browserStartURL: "https://example.com" },
    externalAPI: { enabled: true, apiKey: "retained-key" },
    modelAdapters: [{ id: "latest-model" }],
    delegation: { groups: ["latest-group"] },
    autoMatchContextWindow: false,
  });
});

test("mirror capture disabling clears fidelity without resetting hosts", async () => {
  const h = harness({ mirrorCapture: { enabled: true, protocolFidelity: true, hosts: ["custom.example"] } });
  const actions = makeSettingsActions(h.writer.patch, {}, Boolean, (mode) => mode, (value) => value);
  await actions.saveMirrorCaptureEnabled(false);
  await actions.saveMirrorCaptureEnabled(true);
  assert.deepEqual(h.stored().mirrorCapture, { enabled: true, protocolFidelity: false, hosts: ["custom.example"] });
});

test("patch merging preserves sibling fields, replaces arrays and does not mutate inputs", () => {
  const current = { computerUse: { mode: "desktop", browserStartURL: "about:blank" }, models: [1, 2] };
  const patch = { computerUse: { mode: "browser" }, models: [3] };
  const result = mergeConfigPatch(current, patch);
  assert.deepEqual(result, { computerUse: { mode: "browser", browserStartURL: "about:blank" }, models: [3] });
  assert.equal(current.computerUse.mode, "desktop");
  assert.deepEqual(current.models, [1, 2]);
});

test("concurrent saves of different fields merge against the latest persisted configuration", async () => {
  const gate = deferred();
  let calls = 0;
  const h = harness({ log: false, billingQuery: { enabled: true }, delegation: { groups: ["keep"] } }, {
    beforeSave: async () => { if (++calls === 1) await gate.promise; },
  });
  const first = h.writer.patch({ log: true });
  const second = h.writer.patch({ billingQuery: { enabled: false } });
  gate.resolve();
  assert.deepEqual(await Promise.all([first, second]), [{ ok: true, error: "" }, { ok: true, error: "" }]);
  assert.deepEqual(h.stored(), { log: true, billingQuery: { enabled: false }, delegation: { groups: ["keep"] } });
});

test("queued patches retain only the explicitly submitted values, not later draft mutations", async () => {
  const h = harness({ computerUse: { browserStartURL: "about:blank" } });
  const patch = { computerUse: { mode: "browser" }, modelAdapters: [{ id: "submitted" }] };
  const pending = h.writer.patch(patch);
  patch.computerUse.mode = "desktop";
  patch.modelAdapters[0].id = "mutated";
  await pending;
  assert.equal(h.stored().computerUse.mode, "browser");
  assert.equal(h.stored().modelAdapters[0].id, "submitted");
});

test("execution-time patches see preceding queued writes", async () => {
  const h = harness({ log: false });
  const first = h.writer.patch({ log: true });
  const second = h.writer.patch((latest) => ({ autoDisableFailedModels: latest.log }));
  await Promise.all([first, second]);
  assert.deepEqual(h.stored(), { log: true, autoDisableFailedModels: true });
});

test("concurrent nested patches do not reset sibling settings", async () => {
  const h = harness({ computerUse: { mode: "desktop", browserStartURL: "about:blank" } });
  await Promise.all([
    h.writer.patch({ computerUse: { mode: "browser" } }),
    h.writer.patch({ computerUse: { browserStartURL: "https://example.com" } }),
  ]);
  assert.deepEqual(h.stored().computerUse, { mode: "browser", browserStartURL: "https://example.com" });
});

test("a failed save reports its own failure and the queued next save still succeeds", async () => {
  let calls = 0;
  const h = harness({ log: false }, {
    beforeSave: () => { if (++calls === 1) throw new Error("disk unavailable"); },
  });
  const results = await Promise.all([
    h.writer.patch({ log: true }),
    h.writer.patch({ billingQuery: { enabled: true } }),
  ]);
  assert.deepEqual(results, [{ ok: false, error: "disk unavailable" }, { ok: true, error: "" }]);
  assert.deepEqual(h.stored(), { log: false, billingQuery: { enabled: true } });
  assert.deepEqual(h.saving, [true, false, true, false]);
  assert.equal(h.applied.length, 1);
});

test("load and validation failures do not poison the queue", async () => {
  let loads = 0;
  const h = harness({}, { dependencies: {
    load: async () => { if (++loads === 1) throw new Error("read failed"); return {}; },
    prepare: (config) => ({ payload: config, error: config.invalid ? "invalid" : "" }),
  } });
  assert.equal((await h.writer.patch({ log: true })).error, "read failed");
  assert.equal((await h.writer.patch({ invalid: true })).error, "invalid");
  assert.equal((await h.writer.patch({ log: true })).ok, true);
  assert.equal(h.writes.length, 1);
});

test("model read-modify-write transactions share the queue and retain delegation and model-only apply", async () => {
  const h = harness({ modelAdapters: [{ id: "a", disabled: false }, { id: "b", disabled: false }], delegation: { groups: ["unchanged"] } });
  const disable = (id) => h.writer.transaction(({ config, persist }) => persist({
    modelAdapters: config.modelAdapters.map((model) => model.id === id ? { ...model, disabled: true } : model),
  }, { modelAdaptersOnly: true }));
  await Promise.all([disable("a"), h.writer.patch({ log: true }), disable("b")]);
  assert.deepEqual(h.stored().modelAdapters.map((model) => model.disabled), [true, true]);
  assert.deepEqual(h.stored().delegation, { groups: ["unchanged"] });
  assert.equal(h.stored().log, true);
  assert.deepEqual(h.applied[0].options, { modelAdaptersOnly: true });
  assert.deepEqual(h.applied[2].options, { modelAdaptersOnly: true });
});