import { fetchLocalCacheStats, getHomeMetricsSummary } from "@/services/clientApi";
import { createPollingController } from "@/composables/usePolling";
import { onBeforeUnmount, onMounted, shallowRef } from "vue";

const DEFAULT_INTERVAL_MS = 5000;

const localCacheStats = shallowRef(null);
const homeMetricsSummary = shallowRef(null);

let refCount = 0;
let controller = null;

async function refreshSharedHomeMetrics() {
  const [summaryResult, cacheResult] = await Promise.allSettled([
    getHomeMetricsSummary(),
    fetchLocalCacheStats(),
  ]);
  if (summaryResult.status === "fulfilled") {
    homeMetricsSummary.value = summaryResult.value || {};
  }
  if (cacheResult.status === "fulfilled") {
    localCacheStats.value = cacheResult.value || {};
  }
}

function ensureController(intervalMs) {
  if (controller) {
    return;
  }
  controller = createPollingController(
    refreshSharedHomeMetrics,
    setTimeout,
    clearTimeout,
    intervalMs,
  );
}

/**
 * 首页指标与悬浮窗共享的轮询：fetchLocalCacheStats + getHomeMetricsSummary。
 * 引用计数归零时停止定时器，避免多组件重复拉同一数据。
 */
export function useSharedHomeMetricsRefresh({ intervalMs = DEFAULT_INTERVAL_MS, immediate = true } = {}) {
  onMounted(() => {
    refCount += 1;
    ensureController(intervalMs);
    if (refCount === 1) {
      controller.start({ immediate });
    }
  });

  onBeforeUnmount(() => {
    refCount = Math.max(0, refCount - 1);
    if (refCount === 0 && controller) {
      controller.stop();
    }
  });

  return {
    localCacheStats,
    homeMetricsSummary,
    refresh: refreshSharedHomeMetrics,
  };
}
