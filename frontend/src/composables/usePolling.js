import { onBeforeUnmount, onMounted } from "vue";

export function createPollingController(task, schedule, cancel, intervalMs) {
  let active = false;
  let running = false;
  let timer = null;
  let nextIntervalMs = intervalMs;

  function clearTimer() {
    if (timer !== null) {
      cancel(timer);
      timer = null;
    }
  }

  function scheduleNext() {
    if (!active || running || timer !== null) return;
    timer = schedule(() => {
      timer = null;
      run();
    }, nextIntervalMs);
  }

  function resolveNextInterval(result, error) {
    const resolved = typeof intervalMs === "function" ? intervalMs(result, error) : intervalMs;
    const parsed = Number(resolved);
    nextIntervalMs = Number.isFinite(parsed) && parsed >= 0 ? parsed : 0;
  }

  function run() {
    if (!active || running) return;
    running = true;
    let result;
    try {
      result = task();
    } catch (error) {
      running = false;
      resolveNextInterval(undefined, error);
      scheduleNext();
      return;
    }
    void Promise.resolve(result)
      .then(
        (value) => resolveNextInterval(value, undefined),
        (error) => resolveNextInterval(undefined, error),
      )
      .finally(() => {
        running = false;
        scheduleNext();
      });
  }

  function start({ immediate = false, initialResult, initialError } = {}) {
    if (active) return;
    active = true;
    if (immediate) run();
    else {
      resolveNextInterval(initialResult, initialError);
      scheduleNext();
    }
  }

  function stop() {
    active = false;
    clearTimer();
  }

  return { start, stop };
}

/**
 * 通用轮询 composable：task 完成后再等待固定间隔，组件卸载时自动清理定时器。
 * task 应自行处理内部守卫（如 visibility、loading、条件状态）与错误兜底。
 *
 * @param {() => any} task 轮询任务；返回 Promise 时会等待其 settle 后再安排下一次
 * @param {{intervalMs: number | ((result: any, error: any) => number), immediate?: boolean, autostart?: boolean}} options
 *   intervalMs 固定间隔，或根据任务结算结果决定下一次轮询间隔的函数；immediate 挂载后立即执行一次（默认 true）；
 *   autostart 挂载后自动开始轮询（默认 true，false 时需手动调用 start）。
 * @returns {{start: (options?: {immediate?: boolean, initialResult?: any, initialError?: any}) => void, stop: () => void}}
 */
export function usePolling(task, { intervalMs, immediate = true, autostart = true } = {}) {
  const controller = createPollingController(task, setTimeout, clearTimeout, intervalMs);

  onMounted(() => {
    if (autostart) {
      controller.start({ immediate });
    } else if (immediate) {
      controller.start({ immediate: true });
      controller.stop();
    }
  });

  onBeforeUnmount(controller.stop);

  return {
    start: controller.start,
    stop: controller.stop,
  };
}
