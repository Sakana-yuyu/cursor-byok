<script setup>
import { getHomeMetricsSummary, fetchLocalCacheStats } from "@/services/clientApi";
import { computed, onMounted, onUnmounted, ref } from "vue";

const summary = ref({});
const localCache = ref({});
const loading = ref(false);
const updated = ref(false);
let timer = null;
let updatedTimer = null;

const REFRESH_MS = 10000;

const cacheHitRate = computed(() => summary.value.cacheHitRate);
const totalTokens = computed(() => summary.value.requestTokensTotal || 0);
const turnsTotal = computed(() => summary.value.turnsTotal || 0);
const localCacheHits = computed(() => localCache.value.hits || 0);

function formatRate(rate) {
  if (rate == null) return "—";
  return `${(rate * 100).toFixed(1)}%`;
}

function formatCompact(num) {
  if (num == null || num === 0) return "0";
  if (num >= 1e9) return `${(num / 1e9).toFixed(1)}B`;
  if (num >= 1e6) return `${(num / 1e6).toFixed(1)}M`;
  if (num >= 1e3) return `${(num / 1e3).toFixed(1)}K`;
  return String(num);
}

async function load() {
  loading.value = true;
  try {
    const [summaryData, cacheData] = await Promise.all([
      getHomeMetricsSummary(),
      fetchLocalCacheStats(),
    ]);
    summary.value = summaryData || {};
    localCache.value = cacheData || {};
    updated.value = true;
    if (updatedTimer) clearTimeout(updatedTimer);
    updatedTimer = setTimeout(() => { updated.value = false; }, 800);
  } catch (err) {
    console.error("[StatsOverlay] load failed:", err);
  } finally {
    loading.value = false;
  }
}

onMounted(() => {
  void load();
  timer = setInterval(load, REFRESH_MS);
});

onUnmounted(() => {
  if (timer) clearInterval(timer);
  if (updatedTimer) clearTimeout(updatedTimer);
});
</script>

<template>
  <div class="stats-overlay" style="--wails-draggable: drag">
    <div class="stats-panel">
      <header class="panel-header">
        <span class="panel-title">实时统计</span>
        <span class="status-dot" :class="{ 'is-loading': loading, 'is-updated': updated }"></span>
      </header>

      <div class="metrics-grid">
        <div class="metric-item">
          <div class="metric-label">缓存命中</div>
          <div class="metric-value" :class="{ 'is-good': cacheHitRate != null && cacheHitRate > 0.3 }">
            {{ formatRate(cacheHitRate) }}
          </div>
        </div>

        <div class="metric-item">
          <div class="metric-label">Token 消耗</div>
          <div class="metric-value">{{ formatCompact(totalTokens) }}</div>
        </div>

        <div class="metric-item">
          <div class="metric-label">对话轮次</div>
          <div class="metric-value">{{ formatCompact(turnsTotal) }}</div>
        </div>

        <div class="metric-item">
          <div class="metric-label">本地缓存</div>
          <div class="metric-value">{{ formatCompact(localCacheHits) }}</div>
        </div>
      </div>

      <footer class="panel-footer">
        <span class="footer-hint">每 10 秒自动刷新</span>
      </footer>
    </div>
  </div>
</template>

<style scoped>
.stats-overlay {
  --accent: #6ee7a5;
  --text-primary: #e8f0ed;
  --text-secondary: #8b9692;
  --bg-surface: rgba(12, 18, 20, 0.88);
  --border-color: rgba(110, 231, 165, 0.2);
  --shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
  
  width: 100vw;
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: transparent;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
}

.stats-panel {
  width: 320px;
  background: var(--bg-surface);
  border: 1px solid var(--border-color);
  border-radius: 16px;
  box-shadow: var(--shadow);
  backdrop-filter: blur(20px) saturate(180%);
  -webkit-backdrop-filter: blur(20px) saturate(180%);
  overflow: hidden;
  transition: transform 0.2s ease, opacity 0.2s ease;
}

.stats-panel:hover {
  transform: translateY(-2px);
}

.panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 20px;
  border-bottom: 1px solid rgba(110, 231, 165, 0.1);
}

.panel-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
  letter-spacing: 0.02em;
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--text-secondary);
  transition: all 0.3s ease;
}

.status-dot.is-loading {
  background: #f39c12;
  animation: pulse 1.5s ease-in-out infinite;
}

.status-dot.is-updated {
  background: var(--accent);
  box-shadow: 0 0 8px rgba(110, 231, 165, 0.8);
}

.metrics-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 1px;
  background: rgba(110, 231, 165, 0.08);
  padding: 1px;
}

.metric-item {
  background: var(--bg-surface);
  padding: 20px 16px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  transition: background 0.2s ease;
}

.metric-item:hover {
  background: rgba(110, 231, 165, 0.05);
}

.metric-label {
  font-size: 11px;
  font-weight: 500;
  color: var(--text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.06em;
}

.metric-value {
  font-size: 24px;
  font-weight: 700;
  color: var(--text-primary);
  font-family: ui-monospace, "SF Mono", Monaco, "Cascadia Mono", "Segoe UI Mono", monospace;
  letter-spacing: -0.02em;
  transition: color 0.3s ease;
}

.metric-value.is-good {
  color: var(--accent);
}

.panel-footer {
  padding: 12px 20px;
  border-top: 1px solid rgba(110, 231, 165, 0.1);
  text-align: center;
}

.footer-hint {
  font-size: 11px;
  color: var(--text-secondary);
  opacity: 0.7;
}

@keyframes pulse {
  0%, 100% {
    opacity: 1;
    transform: scale(1);
  }
  50% {
    opacity: 0.6;
    transform: scale(1.2);
  }
}

@media (prefers-reduced-motion: reduce) {
  *,
  *::before,
  *::after {
    animation-duration: 0.001ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 0.001ms !important;
  }
}
</style>