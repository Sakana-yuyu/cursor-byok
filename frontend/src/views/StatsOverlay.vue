<script setup>
import { getHomeMetricsSummary, fetchLocalCacheStats } from "@/services/clientApi";
import { getStatsOverlayPreferences, appState } from "@/state/appState";
import { computed, onMounted, onUnmounted, ref, watch } from "vue";

const summary = ref({});
const localCache = ref({});
const loading = ref(false);
const preferences = ref({ style: "card" });
const updated = ref(false);
let timer = null;
let updatedTimer = null;

const REFRESH_MS = 10000;
const STORAGE_KEY = "cursor-byok.stats-overlay.preferences";

function formatCompact(value) {
  const n = Number(value || 0);
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + "M";
  if (n >= 1_000) return (n / 1_000).toFixed(1) + "K";
  return String(Math.round(n));
}

function formatRate(value) {
  if (value == null) return "—";
  return (Number(value) * 100).toFixed(1) + "%";
}

const cacheHitRate = computed(() => summary.value.cacheHitRate ?? null);
const totalTokens = computed(() => Number(summary.value.requestTokensTotal || 0));
const promptTokens = computed(() => Number(summary.value.promptTokensTotal || 0));
const turnsTotal = computed(() => Number(summary.value.turnsTotal || 0));
const validTurns = computed(() => Number(summary.value.validTurnsTotal || 0));
const invalidTurns = computed(() => Number(summary.value.invalidTurnsTotal || 0));
const localCacheHits = computed(() => Number(localCache.value.hits || 0));
const localCacheMisses = computed(() => Number(localCache.value.misses || 0));
const localCacheRate = computed(() => {
  const h = localCacheHits.value;
  const m = localCacheMisses.value;
  if (h + m === 0) return null;
  return h / (h + m);
});
const displayRate = computed(() => cacheHitRate.value ?? localCacheRate.value);
const gaugeDash = computed(() => `${Math.max(0, Math.min(1, Number(displayRate.value || 0))) * 100} 100`);
const style = computed(() => preferences.value.style || "card");

function markUpdated() {
  updated.value = true;
  if (updatedTimer) clearTimeout(updatedTimer);
  updatedTimer = setTimeout(() => { updated.value = false; }, 900);
}

async function load() {
  loading.value = true;
  try {
    const [s, c] = await Promise.allSettled([getHomeMetricsSummary(), fetchLocalCacheStats()]);
    if (s.status === "fulfilled") summary.value = s.value || {};
    if (c.status === "fulfilled") localCache.value = c.value || {};
    markUpdated();
  } catch (_) {
    // 浮窗静默失败，不弹错误
  } finally {
    loading.value = false;
  }
}

function syncPreferences() {
  const next = getStatsOverlayPreferences();
  preferences.value = { ...next };
}
function onStorage(event) {
  if (!event || event.key === STORAGE_KEY) syncPreferences();
}
function onPreferencesChanged() {
  syncPreferences();
}

watch(() => appState.statsOverlayPreferences, (next) => {
  if (next) preferences.value = { ...next };
}, { deep: true });

onMounted(() => {
  syncPreferences();
  void load();
  timer = setInterval(load, REFRESH_MS);
  window.addEventListener("storage", onStorage);
  window.addEventListener("stats-overlay-preferences-changed", onPreferencesChanged);
});

onUnmounted(() => {
  if (timer) clearInterval(timer);
  if (updatedTimer) clearTimeout(updatedTimer);
  window.removeEventListener("storage", onStorage);
  window.removeEventListener("stats-overlay-preferences-changed", onPreferencesChanged);
});
</script>

<template>
  <div class="stats-overlay" :class="[`stats-overlay--${style}`, { 'is-updated': updated }]" style="--wails-draggable: drag">
    <header class="overlay-header">
      <span class="overlay-kicker">实时统计</span>
      <span class="status-dot" :class="{ 'is-loading': loading }" title="每 10 秒自动刷新"></span>
    </header>

    <section v-if="style === 'card'" class="card-panel" aria-label="实时统计指标">
      <div class="metric-card" :title="`本地缓存命中 ${localCacheHits} · 未命中 ${localCacheMisses}` + (localCacheRate != null ? ` · 命中率 ${(localCacheRate * 100).toFixed(1)}%` : '')">
        <div class="metric-label">缓存命中</div><div class="metric-value" :class="{ 'is-good': cacheHitRate != null && cacheHitRate > 0.3 }">{{ formatRate(cacheHitRate) }}</div>
      </div>
      <div class="metric-card" :title="`Prompt: ${promptTokens.toLocaleString()}`">
        <div class="metric-label">Token 消耗</div><div class="metric-value">{{ formatCompact(totalTokens) }}</div>
      </div>
      <div class="metric-card" :title="`有效 ${validTurns} / 异常 ${invalidTurns}`">
        <div class="metric-label">对话轮次</div><div class="metric-value">{{ formatCompact(turnsTotal) }}</div>
      </div>
      <div class="metric-card" title="基于内置官方价格估算">
        <div class="metric-label">价值估算</div><div class="metric-value metric-value--good">—</div>
      </div>
    </section>

    <section v-else-if="style === 'engine'" class="engine-panel" aria-label="引擎遥测">
      <div class="engine-gauge">
        <svg viewBox="0 0 120 120" aria-hidden="true"><circle class="gauge-track" cx="60" cy="60" r="48" /><circle class="gauge-value" cx="60" cy="60" r="48" pathLength="100" :stroke-dasharray="gaugeDash" /></svg>
        <div class="gauge-center"><strong>{{ formatRate(displayRate) }}</strong><span>命中率</span></div>
      </div>
      <div class="telemetry-grid">
        <div><span>Tokens</span><strong>{{ formatCompact(totalTokens) }}</strong></div>
        <div><span>Turns</span><strong>{{ formatCompact(turnsTotal) }}</strong></div>
        <div><span>Local hits</span><strong>{{ formatCompact(localCacheHits) }}</strong></div>
        <div><span>Prompt</span><strong>{{ formatCompact(promptTokens) }}</strong></div>
      </div>
    </section>

    <section v-else class="orb-panel" aria-label="实时统计球体">
      <div class="orb-satellite orb-satellite--top"><span>命中率</span><strong>{{ formatRate(displayRate) }}</strong></div>
      <div class="orb-satellite orb-satellite--right"><span>Tokens</span><strong>{{ formatCompact(totalTokens) }}</strong></div>
      <div class="orb-satellite orb-satellite--bottom"><span>Turns</span><strong>{{ formatCompact(turnsTotal) }}</strong></div>
      <div class="orb-satellite orb-satellite--left"><span>Local</span><strong>{{ formatCompact(localCacheHits) }}</strong></div>
      <div class="orb-core"><div class="orb-glow"></div><div class="orb-sphere"></div></div>
    </section>
  </div>
</template>

<style scoped>
.stats-overlay { --accent: #6ee7a5; --muted: #777; width: 100vw; height: 100vh; padding: 6px 8px; overflow: hidden; background: transparent; color: #e5e5e5; font-family: inherit; }
.overlay-header { height: 15px; display: flex; align-items: center; justify-content: space-between; padding: 0 2px; }
.overlay-kicker { color: #858585; font-size: 10px; font-weight: 600; letter-spacing: .04em; }
.status-dot { width: 6px; height: 6px; border-radius: 50%; background: #10ad5d; animation: breathe 3.8s ease-in-out infinite; }
.status-dot.is-loading { background: #fbbf24; }
.card-panel { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 5px; padding: 5px; border: 1px solid #343434; border-radius: 8px; background: rgba(24,24,24,.84); }
.metric-card { min-width: 0; padding: 4px 6px; border: 1px solid rgba(62,62,62,.75); border-radius: 5px; background: rgba(36,36,36,.9); }
.metric-label { color: #777; font-size: 9px; white-space: nowrap; }
.metric-value { color: #fff; font-family: var(--font-num, ui-monospace, monospace); font-size: 15px; line-height: 1.15; transition: color .35s ease, text-shadow .35s ease; }
.metric-value.is-good, .metric-value--good { color: var(--accent); }
.is-updated .metric-value { text-shadow: 0 0 10px rgba(110,231,165,.7); }
.engine-panel { position: relative; display: flex; align-items: center; gap: 8px; min-height: calc(100vh - 27px); padding: 5px 7px; border: 1px solid rgba(71,112,112,.55); border-radius: 9px; background: linear-gradient(135deg, rgba(18,39,40,.92), rgba(18,24,28,.85)); overflow: hidden; }
.engine-panel::after { content: ''; position: absolute; inset: 0; background: repeating-linear-gradient(0deg, transparent 0 11px, rgba(110,231,165,.07) 12px); animation: scan 8s linear infinite; pointer-events: none; }
.engine-gauge { position: relative; flex: 0 0 74px; height: 74px; }
.engine-gauge svg { width: 100%; height: 100%; transform: rotate(-90deg); }
.gauge-track, .gauge-value { fill: none; stroke-width: 7; }
.gauge-track { stroke: rgba(120,170,165,.2); }
.gauge-value { stroke: var(--accent); stroke-linecap: round; transition: stroke-dasharray .7s ease; }
.gauge-center { position: absolute; inset: 0; display: flex; flex-direction: column; align-items: center; justify-content: center; }
.gauge-center strong { color: #f5fff9; font: 600 14px var(--font-num, ui-monospace, monospace); }
.gauge-center span { color: #8da9a4; font-size: 8px; }
.telemetry-grid { z-index: 1; display: grid; flex: 1; grid-template-columns: repeat(2, minmax(0,1fr)); gap: 5px; }
.telemetry-grid div { padding: 3px 5px; border-left: 2px solid rgba(110,231,165,.6); background: rgba(5,15,17,.36); }
.telemetry-grid span { display: block; color: #77918e; font-size: 8px; white-space: nowrap; }
.telemetry-grid strong { color: #d8eee8; font: 12px var(--font-num, ui-monospace, monospace); }
.orb-panel { position: relative; width: 100%; height: calc(100vh - 27px); min-height: 80px; overflow: visible; }
.orb-core { position: absolute; inset: 18% 27%; display: grid; place-items: center; animation: float 5s ease-in-out infinite; }
.orb-sphere { width: 44px; height: 44px; border-radius: 50%; background: radial-gradient(circle at 30% 25%, #eafff4 0 5%, #86eabb 18%, #168f76 52%, #0a3235 100%); box-shadow: inset -8px -8px 13px rgba(0,0,0,.45), 0 0 18px rgba(79,220,185,.6); }
.orb-glow { position: absolute; width: 62px; height: 62px; border-radius: 50%; background: rgba(58,206,171,.2); filter: blur(10px); animation: pulse 3.5s ease-in-out infinite; }
.orb-satellite { position: absolute; z-index: 2; display: flex; flex-direction: column; align-items: center; min-width: 47px; padding: 2px 4px; border: 1px solid rgba(93,180,160,.42); border-radius: 5px; background: rgba(14,28,29,.88); line-height: 1.1; }
.orb-satellite span { color: #7ea39b; font-size: 8px; white-space: nowrap; }.orb-satellite strong { color: #e7fff3; font: 11px var(--font-num, ui-monospace, monospace); }.orb-satellite--top { top: 0; left: 50%; transform: translateX(-50%); }.orb-satellite--right { top: 50%; right: 0; transform: translateY(-50%); }.orb-satellite--bottom { bottom: 0; left: 50%; transform: translateX(-50%); }.orb-satellite--left { top: 50%; left: 0; transform: translateY(-50%); }
@keyframes breathe { 0%,100% { opacity: .55; box-shadow: 0 0 0 transparent; } 50% { opacity: 1; box-shadow: 0 0 7px currentColor; } }
@keyframes scan { from { transform: translateY(-12px); } to { transform: translateY(12px); } }
@keyframes float { 0%,100% { transform: translateY(-1px); } 50% { transform: translateY(2px); } }
@keyframes pulse { 0%,100% { opacity: .55; transform: scale(.92); } 50% { opacity: .9; transform: scale(1.06); } }
@media (prefers-reduced-motion: reduce) { *, *::before, *::after { animation-duration: .001ms !important; animation-iteration-count: 1 !important; transition-duration: .001ms !important; } }
</style>
