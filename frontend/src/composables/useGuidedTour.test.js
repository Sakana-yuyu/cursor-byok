import test from "node:test";
import assert from "node:assert/strict";
import { createGuidedTourController, isGuidedTourCompleted } from "./useGuidedTour.js";

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function waitFor(condition, timeoutMs = 3000) {
  const startedAt = Date.now();
  while (!condition()) {
    if (Date.now() - startedAt > timeoutMs) throw new Error("waitFor timeout");
    await sleep(10);
  }
}

function makeRouter(initialPath = "/") {
  const r = {
    path: initialPath,
    pushed: [],
    async push(path) {
      this.pushed.push(path);
      this.path = path;
    },
    currentPath() {
      return this.path;
    },
  };
  return r;
}

function makeStorage() {
  const map = new Map();
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
    dump: () => map,
  };
}

function makeController(overrides = {}) {
  const router = overrides.router || makeRouter();
  const storage = overrides.storage || makeStorage();
  const targets = overrides.targets || {};
  const resolveTarget = overrides.resolveTarget || ((selector) => targets[selector] || null);
  const controller = createGuidedTourController({
    steps: overrides.steps || [
      { route: "/", center: true },
      { route: "/", target: "[data-tour-nav='/model-config']", placement: "right" },
      { route: "/model-config", target: "[data-tour-target='model-config-root']" },
    ],
    router,
    storage,
    resolveTarget,
    elementTimeoutMs: overrides.elementTimeoutMs ?? 120,
  });
  return { controller, router, storage };
}

test("start activates the first step without route push when already there", async () => {
  const { controller, router } = makeController();
  controller.start();
  assert.equal(controller.state.active, true);
  await waitFor(() => !controller.state.resolving);
  assert.equal(controller.state.index, 0);
  assert.equal(controller.state.mode, "center");
  assert.deepEqual(router.pushed, []);
});

test("start is ignored while already active", async () => {
  const { controller } = makeController();
  controller.start();
  controller.start();
  await waitFor(() => !controller.state.resolving);
  assert.equal(controller.state.index, 0);
});

test("next advances and resolves a target element into spotlight mode", async () => {
  const element = {};
  const { controller } = makeController({ targets: { "[data-tour-nav='/model-config']": element } });
  controller.start();
  await waitFor(() => !controller.state.resolving);
  controller.next();
  assert.equal(controller.state.resolving, true);
  await waitFor(() => !controller.state.resolving);
  assert.equal(controller.state.index, 1);
  assert.equal(controller.state.mode, "spotlight");
  assert.equal(controller.state.targetEl, element);
});

test("next on the last step finishes and persists the completed flag", async () => {
  const { controller, storage } = makeController();
  controller.start(2); // last step (has route change + missing target)
  await waitFor(() => !controller.state.resolving);
  controller.next();
  assert.equal(controller.state.active, false);
  assert.equal(controller.state.index, -1);
  assert.equal(isGuidedTourCompleted(storage), true);
});

test("skip closes the tour without the completed flag", async () => {
  const { controller, storage } = makeController();
  controller.start();
  await waitFor(() => !controller.state.resolving);
  controller.skip();
  assert.equal(controller.state.active, false);
  assert.equal(isGuidedTourCompleted(storage), false);
});

test("prev is a no-op on the first step and walks back otherwise", async () => {
  const { controller } = makeController();
  controller.start();
  await waitFor(() => !controller.state.resolving);
  controller.prev();
  assert.equal(controller.state.index, 0);
  controller.next();
  await waitFor(() => !controller.state.resolving);
  controller.prev();
  await waitFor(() => !controller.state.resolving);
  assert.equal(controller.state.index, 0);
});

test("route change pushes the target route before resolving the element", async () => {
  const element = {};
  const router = makeRouter("/");
  const { controller } = makeController({ router, targets: { "[data-tour-target='model-config-root']": element } });
  controller.start(2);
  await waitFor(() => !controller.state.resolving);
  assert.deepEqual(router.pushed, ["/model-config"]);
  assert.equal(controller.state.mode, "spotlight");
});

test("missing target element degrades to center mode after timeout", async () => {
  const { controller } = makeController({ targets: {} });
  controller.start(1);
  await waitFor(() => !controller.state.resolving);
  assert.equal(controller.state.mode, "center");
  assert.equal(controller.state.targetEl, null);
});

test("navigation buttons are ignored while resolving", async () => {
  const router = makeRouter("/");
  // slow target: only resolves after the second poll, so resolving is observable
  let attempts = 0;
  const resolveTarget = () => {
    attempts += 1;
    return attempts >= 2 ? {} : null;
  };
  const { controller } = makeController({ router, resolveTarget });
  controller.start(2); // triggers route push + element wait
  controller.next();
  controller.prev();
  assert.equal(controller.state.index, 2);
  await waitFor(() => !controller.state.resolving);
  assert.equal(controller.state.index, 2);
});

test("skip during element wait stops the pending goTo from mutating state", async () => {
  const router = makeRouter("/");
  const { controller } = makeController({ router, targets: {} });
  controller.start(1); // element never resolves; waits ~timeoutMs
  await waitFor(() => controller.state.resolving === true);
  controller.skip();
  assert.equal(controller.state.active, false);
  await sleep(220); // let the stale poll fire
  assert.equal(controller.state.active, false);
  assert.equal(controller.state.index, -1);
});

test("currentStep exposes the active step definition or null", async () => {
  const { controller } = makeController();
  assert.equal(controller.currentStep(), null);
  controller.start();
  await waitFor(() => !controller.state.resolving);
  assert.equal(controller.currentStep().center, true);
  controller.skip();
  assert.equal(controller.currentStep(), null);
});
