import { onBeforeUnmount, onMounted } from "vue";

/**
 * 通用轮询 composable：按固定间隔执行 task，组件卸载时自动清理定时器。
 * task 应自行处理内部守卫（如 visibility、loading、条件状态）与错误兜底。
 *
 * @param {() => any} task 轮询任务（返回 Promise 时以 fire-and-forget 方式执行）
 * @param {{intervalMs: number, immediate?: boolean, autostart?: boolean}} options
 *   intervalMs 轮询间隔（毫秒）；immediate 挂载后立即执行一次（默认 true）；
 *   autostart 挂载后自动开始轮询（默认 true，false 时需手动调用 start）。
 * @returns {{start: () => void, stop: () => void}}
 */
export function usePolling(task, { intervalMs, immediate = true, autostart = true } = {}) {
  let timer = null;

  function stop() {
    if (timer) {
      clearInterval(timer);
      timer = null;
    }
  }

  function start() {
    stop();
    timer = setInterval(() => {
      void task();
    }, intervalMs);
  }

  onMounted(() => {
    if (immediate) void task();
    if (autostart) start();
  });

  onBeforeUnmount(stop);

  return { start, stop };
}