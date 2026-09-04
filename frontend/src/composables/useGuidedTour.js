import { shallowReactive } from "vue";

// 新手使用引导（guided tour）控制器。
// 纯逻辑层：步骤推进、跨路由跳转、目标元素等待与超时降级、完成标记持久化。
// DOM 定位与渲染由 GuidedTour.vue 注入（resolveTarget/waitFrame），便于 node --test 直测。
//
// 设计约定：
// - 步骤定义（含用户可见文案）由组件层传入，本模块不出现 UI 文案；
// - target 元素等待超时后降级为居中卡片（mode="center"），引导不因条件渲染元素缺失而卡死；
// - finish 写完成标记，skip 不写：完成标记只用于以后"新功能引导"扩展，入口常驻可重复观看。

const COMPLETED_STORAGE_KEY = "cursor-byok.guided-tour.completed";
const DEFAULT_ELEMENT_TIMEOUT_MS = 2000;
const ELEMENT_POLL_INTERVAL_MS = 80;

function defaultStorage() {
  if (typeof window === "undefined" || !window.localStorage) {
    return { getItem: () => null, setItem: () => {} };
  }
  return window.localStorage;
}

export function isGuidedTourCompleted(storage = defaultStorage()) {
  return storage.getItem(COMPLETED_STORAGE_KEY) === "true";
}

// createGuidedTourController 创建引导控制器。
// deps：
//   steps         步骤数组：{ route?, target?, placement?, center?, advanceOn?, elementTimeoutMs? }
//                 advanceOn="click"：用户点击目标元素后由组件层调用 advance() 前进；
//                 elementTimeoutMs 覆盖全局等待超时（启动服务等慢操作场景放宽）。
//   router        { push(path): Promise|void, currentPath(): string }
//   storage       { getItem(key), setItem(key, value) }，默认 window.localStorage
//   resolveTarget (selector) => Element|null，同步查询，控制器内部轮询
//   elementTimeoutMs 目标元素等待超时，默认 2000
export function createGuidedTourController(deps) {
  const steps = Array.isArray(deps?.steps) ? deps.steps : [];
  const router = deps?.router || { push: () => {}, currentPath: () => "/" };
  const storage = deps?.storage || defaultStorage();
  const resolveTarget = deps?.resolveTarget || (() => null);
  const elementTimeoutMs = Number.isFinite(deps?.elementTimeoutMs)
    ? deps.elementTimeoutMs
    : DEFAULT_ELEMENT_TIMEOUT_MS;

  // shallowReactive：targetEl 是 DOM 元素引用，深层代理会干扰 floating-ui/rect 读取。
  const state = shallowReactive({
    active: false,
    index: -1,
    // spotlight：高亮目标元素旁边挂气泡；center：居中卡片（欢迎/完成/元素缺失降级）。
    mode: "center",
    targetEl: null,
    // goTo 进行中（路由跳转/元素等待），期间按钮禁用防重入。
    resolving: false,
  });

  let pollTimer = null;
  // 会话序号：skip/finish 后旧 goTo 的异步续体不得再改状态。
  let runToken = 0;

  function clearPollTimer() {
    if (pollTimer) {
      clearTimeout(pollTimer);
      pollTimer = null;
    }
  }

  function stop(markCompleted) {
    runToken += 1;
    clearPollTimer();
    state.active = false;
    state.index = -1;
    state.mode = "center";
    state.targetEl = null;
    state.resolving = false;
    if (markCompleted) {
      try {
        storage.setItem(COMPLETED_STORAGE_KEY, "true");
      } catch {
        // localStorage 写失败（隐私模式等）不影响引导关闭。
      }
    }
  }

  function waitForTarget(selector, token, timeoutOverrideMs) {
    const timeoutMs = Number.isFinite(timeoutOverrideMs) ? timeoutOverrideMs : elementTimeoutMs;
    return new Promise((resolve) => {
      const startedAt = Date.now();
      const tick = () => {
        if (token !== runToken) {
          resolve(null);
          return;
        }
        const el = resolveTarget(selector);
        if (el) {
          resolve(el);
          return;
        }
        if (Date.now() - startedAt >= timeoutMs) {
          resolve(null);
          return;
        }
        pollTimer = setTimeout(tick, ELEMENT_POLL_INTERVAL_MS);
      };
      tick();
    });
  }

  async function goTo(index) {
    if (index < 0 || index >= steps.length) return;
    const token = runToken;
    state.index = index;
    state.resolving = true;
    state.targetEl = null;
    state.mode = "center";

    try {
      const step = steps[index];
      if (step.route && router.currentPath() !== step.route) {
        await router.push(step.route);
        if (token !== runToken) return;
      }
      if (step.target) {
        const el = await waitForTarget(step.target, token, step.elementTimeoutMs);
        if (token !== runToken) return;
        if (el) {
          state.targetEl = el;
          state.mode = "spotlight";
        }
        // 元素没等到：保持 center 降级展示文案，不阻塞引导。
      }
    } finally {
      if (token === runToken) {
        state.resolving = false;
      }
    }
  }

  return {
    state,
    stepCount: steps.length,
    start(fromIndex = 0) {
      if (state.active || !steps.length) return;
      state.active = true;
      void goTo(fromIndex);
    },
    next() {
      if (!state.active || state.resolving) return;
      if (state.index >= steps.length - 1) {
        stop(true);
        return;
      }
      void goTo(state.index + 1);
    },
    prev() {
      if (!state.active || state.resolving || state.index <= 0) return;
      void goTo(state.index - 1);
    },
    skip() {
      if (!state.active) return;
      stop(false);
    },
    // currentStep 供组件读取文案，避免组件直接依赖 steps 数组下标。
    currentStep() {
      return state.index >= 0 ? steps[state.index] || null : null;
    },
  };
}

// ─── 主窗口单例 ────────────────────────────────────────────────────────────
// 引导入口在首页、渲染在 App.vue 根部，共享同一控制器实例。
let controllerSingleton = null;

export function useGuidedTour() {
  if (!controllerSingleton) {
    throw new Error("guided tour controller not initialized: provideGuidedTourController must run in GuidedTour.vue first");
  }
  return controllerSingleton;
}

export function provideGuidedTourController(controller) {
  controllerSingleton = controller;
}
