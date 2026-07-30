<script setup>
import { getHomeMetricsSummary, fetchLocalCacheStats } from "@/services/clientApi";
import { getStatsOverlayPreferences, setStatsOverlayPreferences, appState } from "@/state/appState";
import { computed, onMounted, onUnmounted, ref, watch } from "vue";

const summary = ref({});
const localCache = ref({});
const loading = ref(false);
const preferences = ref({ style: "card" });
const updated = ref(false);
const isCollapsed = ref(false); // 收缩状态
const isHovering = ref(false); // 鼠标悬停状态
const snapEdge = ref(null); // 吸附边缘: 'left' | 'right' | 'top' | 'bottom' | null
let timer = null;
let updatedTimer = null;
let positionTimer = null;
let lastSavedX = null;
let lastSavedY = null;

const REFRESH_MS = 10000;
const POSITION_CHECK_MS = 300;       // 轮询提速，响应更灵敏
const STORAGE_KEY = "cursor-byok.stats-overlay.preferences";
const SNAP_ENTER_THRESHOLD = 40;     // 进入吸附区的距离（像素）
const SNAP_LEAVE_THRESHOLD = 80;     // 离开吸附区的距离——比进入阈值大，形成迟滞防抖动

// snapCollapse 是否启用贴边自动收缩（来自 preferences）
const snapCollapse = computed(() => preferences.value.snapCollapse !== false);

async function toggleSnapCollapse() {
  const next = !snapCollapse.value;
  await setStatsOverlayPreferences({ snapCollapse: next });
  if (!next) {
    // 关闭贴标收缩时立即展开面板
    isCollapsed.value = false;
  }
}

// 监听窗口位置变化并持久化。窗口通过原生拖动移动，
// window.screenX/screenY 反映窗口在屏幕上的坐标。
function checkPosition() {
  const x = window.screenX;
  const y = window.screenY;
  if (typeof x !== "number" || typeof y !== "number") return;

  // 使用 availLeft/availTop 修正多屏偏移，防止次级屏幕的左/上边缘误判
  const screenLeft   = window.screen.availLeft  || 0;
  const screenTop    = window.screen.availTop   || 0;
  const screenRight  = screenLeft + (window.screen.availWidth  || window.screen.width);
  const screenBottom = screenTop  + (window.screen.availHeight || window.screen.height);
  const windowWidth  = window.outerWidth;
  const windowHeight = window.outerHeight;

  // 迟滞：已吸附到某边时用更大的离开阈值，防止窗口在边缘附近来回闪烁
  const t = (edge) => snapEdge.value === edge ? SNAP_LEAVE_THRESHOLD : SNAP_ENTER_THRESHOLD;

  let newSnapEdge = null;
  if      (x <= screenLeft + t("left"))                   newSnapEdge = "left";
  else if (x + windowWidth  >= screenRight  - t("right")) newSnapEdge = "right";
  else if (y <= screenTop   + t("top"))                   newSnapEdge = "top";
  else if (y + windowHeight >= screenBottom - t("bottom")) newSnapEdge = "bottom";

  // 更新吸附状态，仅在边缘变化时才操作 isCollapsed
  if (newSnapEdge !== snapEdge.value) {
    snapEdge.value = newSnapEdge;
    if (snapCollapse.value) {
      if (newSnapEdge && !isHovering.value) {
        isCollapsed.value = true;
      } else if (!newSnapEdge) {
        isCollapsed.value = false;
      }
    }
  }

  // 位置变化超过阈值才保存，避免频繁写入
  if (lastSavedX === null || lastSavedY === null || Math.abs(x - lastSavedX) > 2 || Math.abs(y - lastSavedY) > 2) {
    lastSavedX = x;
    lastSavedY = y;
    void setStatsOverlayPreferences({ x, y });
  }
}

// 鼠标进入浮窗
function handleMouseEnter() {
  isHovering.value = true;
  if (snapEdge.value && snapCollapse.value) {
    isCollapsed.value = false;
  }
}

// 鼠标离开浮窗
function handleMouseLeave() {
  isHovering.value = false;
  if (snapEdge.value && snapCollapse.value) {
    isCollapsed.value = true;
  }
}

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

// 动态 title（国际化）：tooltip 文案由 $ls 在模板渲染，这里只拼装数值部分。
const localCacheTitle = computed(() => {
  const rate = localCacheRate.value != null ? ` · ${(localCacheRate.value * 100).toFixed(1)}%` : "";
  return `${localCacheHits.value} · ${localCacheMisses.value}${rate}`;
});
const promptTitle = computed(() => promptTokens.value.toLocaleString());
const turnsTitle = computed(() => `${validTurns.value} / ${invalidTurns.value}`);

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
  // 用已保存坐标初始化，防止首次轮询把窗口初始位置（可能是屏幕中心）误覆盖掉已存的正确坐标
  const saved = getStatsOverlayPreferences();
  if (typeof saved.x === "number") lastSavedX = saved.x;
  if (typeof saved.y === "number") lastSavedY = saved.y;
  void load();
  timer = setInterval(load, REFRESH_MS);
  positionTimer = setInterval(checkPosition, POSITION_CHECK_MS);
  window.addEventListener("storage", onStorage);
  window.addEventListener("stats-overlay-preferences-changed", onPreferencesChanged);
});

onUnmounted(() => {
  if (timer) clearInterval(timer);
  if (updatedTimer) clearTimeout(updatedTimer);
  if (positionTimer) clearInterval(positionTimer);
  window.removeEventListener("storage", onStorage);
  window.removeEventListener("stats-overlay-preferences-changed", onPreferencesChanged);
});
</script>

<template>
  <div 
    class="stats-overlay" 
    :class="[
      `stats-overlay--${style}`, 
      { 
        'is-updated': updated,
        'is-collapsed': isCollapsed,
        [`is-snap-${snapEdge}`]: snapEdge
      }
    ]" 
    style="--wails-draggable: drag"
    @mouseenter="handleMouseEnter"
    @mouseleave="handleMouseLeave"
  >
    <!-- 收缩时的悬浮球 -->
    <div v-if="isCollapsed" class="float-ball">
      <div class="ball-glow"></div>
      <div class="ball-core">
        <div class="ball-icon">📊</div>
      </div>
    </div>

    <!-- 完整面板 -->
    <div v-show="!isCollapsed" class="overlay-content">
    <header class="overlay-header">
      <span class="overlay-kicker">实时统计</span>
      <button
        class="snap-toggle"
        :class="{ 'is-off': !snapCollapse }"
        :title="snapCollapse ? '贴边自动收缩：开启（点击关闭）' : '贴边自动收缩：关闭（点击开启）'"
        style="--wails-draggable: no-drag"
        @click.stop="toggleSnapCollapse"
      ></button>
      <span class="status-dot" :class="{ 'is-loading': loading }" title="每 10 秒自动刷新"></span>
    </header>

    <section v-if="style === 'card'" class="card-panel" aria-label="实时统计指标">
      <div class="metric-card" :title="localCacheTitle">
        <div class="metric-label">缓存命中</div><div class="metric-value" :class="{ 'is-good': cacheHitRate != null && cacheHitRate > 0.3 }">{{ formatRate(cacheHitRate) }}</div>
      </div>
      <div class="metric-card" :title="promptTitle">
        <div class="metric-label">Token 消耗</div><div class="metric-value">{{ formatCompact(totalTokens) }}</div>
      </div>
      <div class="metric-card" :title="turnsTitle">
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
      <div class="orb-orbit">
        <div class="orb-satellite orb-satellite--top"><span>命中率</span><strong>{{ formatRate(displayRate) }}</strong></div>
        <div class="orb-satellite orb-satellite--right"><span>Tokens</span><strong>{{ formatCompact(totalTokens) }}</strong></div>
        <div class="orb-satellite orb-satellite--bottom"><span>Turns</span><strong>{{ formatCompact(turnsTotal) }}</strong></div>
        <div class="orb-satellite orb-satellite--left"><span>Local</span><strong>{{ formatCompact(localCacheHits) }}</strong></div>
      </div>
      <div class="orb-core"><div class="orb-glow"></div><div class="orb-sphere"><div class="orb-sphere-highlight"></div></div><div class="orb-ring"></div></div>
    </section>
    </div>

    <!-- 极简实时数据条：收起时显示缩略信息，仅卡片式与引擎仪表支持，球形不显示 -->
    <div v-if="isCollapsed && (style === 'card' || style === 'engine')" class="mini-bar" :class="{ 'is-tick': updated }">
      <span class="mini-rate" :class="{ 'is-good': displayRate != null && displayRate > 0.3 }">{{ formatRate(displayRate) }}</span>
      <span class="mini-sep">·</span>
      <span class="mini-tokens">{{ formatCompact(totalTokens) }}</span>
      <span class="mini-sep">·</span>
      <span class="mini-turns">{{ formatCompact(turnsTotal) }}</span>
      <span class="mini-tick" :class="{ 'is-on': updated }"></span>
    </div>
  </div>
</template>

<style scoped>
.stats-overlay { --accent: #6ee7a5; --muted: #777; width: 100vw; height: 100vh; display: flex; align-items: center; justify-content: center; overflow: hidden; background: transparent; color: #e5e5e5; font-family: inherit; transition: all 0.3s ease; pointer-events: none; }
.stats-overlay > * { pointer-events: auto; }

/* 悬浮球样式 */
.float-ball { position: relative; width: 60px; height: 60px; cursor: pointer; }
.ball-glow { position: absolute; inset: -8px; border-radius: 50%; background: radial-gradient(circle, rgba(110,231,165,0.3), transparent 70%); animation: ballPulse 2s ease-in-out infinite; }
.ball-core { position: absolute; inset: 0; border-radius: 50%; background: linear-gradient(135deg, rgba(110,231,165,0.25), rgba(42,42,42,0.95)); border: 2px solid rgba(110,231,165,0.6); box-shadow: 0 4px 16px rgba(0,0,0,0.5), inset 0 1px 0 rgba(255,255,255,0.1); display: flex; align-items: center; justify-content: center; }
.ball-icon { font-size: 24px; filter: drop-shadow(0 2px 4px rgba(0,0,0,0.3)); }

@keyframes ballPulse {
  0%, 100% { opacity: 0.6; transform: scale(1); }
  50% { opacity: 1; transform: scale(1.05); }
}

/* 完整面板容器 */
.overlay-content { padding: 6px 8px; }

/* 收缩状态：完整面板隐藏 */
.stats-overlay.is-collapsed .overlay-content { display: none; }

/* 收缩状态：容器收缩到悬浮球大小，避免大块背景 */
.stats-overlay.is-collapsed { width: auto; height: auto; }

/* 吸附边缘时的位置调整 */
.stats-overlay.is-snap-left .float-ball { transform: translateX(-20px); }
.stats-overlay.is-snap-right .float-ball { transform: translateX(20px); }
.stats-overlay.is-snap-top .float-ball { transform: translateY(-20px); }
.stats-overlay.is-snap-bottom .float-ball { transform: translateY(20px); }

/* 鼠标悬停时展开 */
.stats-overlay.is-collapsed:hover .float-ball { transform: translate(0, 0); }

.overlay-header { height: 15px; display: flex; align-items: center; justify-content: space-between; padding: 0 2px; transition: opacity 0.3s ease; }
.overlay-kicker { color: #858585; font-size: 10px; font-weight: 600; letter-spacing: .04em; }

/* 贴标收缩开关按钮 */
.snap-toggle { width: 10px; height: 10px; border: 1.5px solid rgba(110,231,165,.55); border-radius: 2px; background: none; cursor: pointer; padding: 0; flex-shrink: 0; position: relative; transition: border-color .2s, opacity .2s; }
.snap-toggle::after { content: ''; position: absolute; top: 50%; left: 50%; transform: translate(-50%, -50%); width: 4px; height: 4px; border-radius: 50%; background: rgba(110,231,165,.75); transition: background .2s; }
.snap-toggle.is-off { border-color: rgba(100,100,100,.45); }
.snap-toggle.is-off::after { background: rgba(100,100,100,.45); }
.snap-toggle:hover { border-color: rgba(110,231,165,.9); opacity: 1; }
.snap-toggle.is-off:hover { border-color: rgba(150,150,150,.7); }

.status-dot { width: 6px; height: 6px; border-radius: 50%; background: #10ad5d; animation: breathe 3.8s ease-in-out infinite; }
.status-dot.is-loading { background: #fbbf24; }
.card-panel { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 4px; padding: 4px; border: 1px solid rgba(255,255,255,.06); border-radius: 8px; background: rgba(18,18,18,.72); backdrop-filter: blur(12px); -webkit-backdrop-filter: blur(12px); transition: opacity 0.3s ease; }
.metric-card { min-width: 0; padding: 3px 6px; border: 1px solid rgba(62,62,62,.75); border-radius: 5px; background: rgba(36,36,36,.9); }
.metric-label { color: #777; font-size: 9px; white-space: nowrap; }
.metric-value { color: #fff; font-family: var(--font-num, ui-monospace, monospace); font-size: 14px; line-height: 1.15; transition: color .35s ease, text-shadow .35s ease; }
.metric-value.is-good, .metric-value--good { color: var(--accent); }
.is-updated .metric-value { text-shadow: 0 0 6px rgba(110,231,165,.7); }
.engine-panel { position: relative; display: flex; align-items: center; gap: 8px; min-height: calc(100vh - 27px); padding: 5px 7px; border: 1px solid rgba(71,112,112,.55); border-radius: 9px; background: linear-gradient(135deg, rgba(18,39,40,.92), rgba(18,24,28,.85)); overflow: hidden; transition: opacity 0.3s ease; }
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
.orb-panel { position: relative; width: 100%; height: calc(100vh - 27px); min-height: 80px; overflow: visible; display: grid; place-items: center; }
.orb-core { position: relative; display: grid; place-items: center; animation: orbFloat 6s ease-in-out infinite; }
.orb-sphere { position: relative; width: 64px; height: 64px; border-radius: 50%; background: radial-gradient(circle at 32% 28%, #eafff4 0%, #a7f0cc 14%, #4fdcb9 38%, #168f76 66%, #0a3235 100%); box-shadow: inset -10px -10px 18px rgba(0,0,0,.5), inset 6px 6px 12px rgba(255,255,255,.12), 0 0 22px rgba(79,220,185,.55); }
.orb-sphere-highlight { position: absolute; top: 14%; left: 24%; width: 26%; height: 22%; border-radius: 50%; background: radial-gradient(circle, rgba(255,255,255,.85) 0%, rgba(255,255,255,0) 70%); filter: blur(1px); }
.orb-glow { position: absolute; width: 92px; height: 92px; border-radius: 50%; background: radial-gradient(circle, rgba(58,206,171,.32) 0%, rgba(58,206,171,0) 70%); filter: blur(8px); animation: pulse 3.5s ease-in-out infinite; }
.orb-ring { position: absolute; width: 96px; height: 96px; border-radius: 50%; border: 1px solid rgba(110,231,165,.28); box-shadow: 0 0 12px rgba(110,231,165,.18); animation: orbSpin 14s linear infinite; }
.orb-orbit { position: absolute; inset: 0; pointer-events: none; }
.orb-satellite { position: absolute; z-index: 2; display: flex; flex-direction: column; align-items: center; min-width: 47px; padding: 2px 5px; border: 1px solid rgba(93,180,160,.42); border-radius: 6px; background: rgba(14,28,29,.22); backdrop-filter: blur(6px); -webkit-backdrop-filter: blur(6px); line-height: 1.1; box-shadow: 0 2px 10px rgba(0,0,0,.35); }
.orb-satellite span { color: #9fcfc5; font-size: 8px; white-space: nowrap; }.orb-satellite strong { color: #eafff4; font: 11px var(--font-num, ui-monospace, monospace); }
/* 四个数据卡各自在原位轻微漂浮（不旋转，保持文字可读），错开相位营造环绕呼吸感。 */
.orb-satellite--top { top: 4px; left: 50%; transform: translateX(-50%); animation: orbDriftV 5s ease-in-out infinite; }
.orb-satellite--bottom { bottom: 4px; left: 50%; transform: translateX(-50%); animation: orbDriftV 5s ease-in-out infinite -2.5s; }
.orb-satellite--left { top: 50%; left: 4px; transform: translateY(-50%); animation: orbDriftH 5.5s ease-in-out infinite -1.4s; }
.orb-satellite--right { top: 50%; right: 4px; transform: translateY(-50%); animation: orbDriftH 5.5s ease-in-out infinite -4s; }
@keyframes breathe { 0%,100% { opacity: .55; box-shadow: 0 0 0 transparent; } 50% { opacity: 1; box-shadow: 0 0 7px currentColor; } }
@keyframes scan { from { transform: translateY(-12px); } to { transform: translateY(12px); } }
@keyframes orbFloat { 0%,100% { transform: translateY(-3px); } 50% { transform: translateY(3px); } }
@keyframes orbDriftV { 0%,100% { transform: translateX(-50%) translateY(-2px); } 50% { transform: translateX(-50%) translateY(2px); } }
@keyframes orbDriftH { 0%,100% { transform: translateY(-50%) translateX(-2px); } 50% { transform: translateY(-50%) translateX(2px); } }
@keyframes orbSpin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
@keyframes pulse { 0%,100% { opacity: .55; transform: scale(.92); } 50% { opacity: .9; transform: scale(1.06); } }

/* 极简实时数据条：收起时显示的缩略信息 */
.mini-bar { display: flex; align-items: center; justify-content: center; gap: 4px; height: auto; padding: 4px 8px; border-radius: 12px; background: rgba(24,24,24,.9); border: 1px solid rgba(52,52,52,.7); color: #9a9a9a; font: 600 10px var(--font-num, ui-monospace, monospace); letter-spacing: .02em; transition: opacity .3s ease; backdrop-filter: blur(8px); -webkit-backdrop-filter: blur(8px); }
.mini-rate { color: #cfcfcf; }
.mini-rate.is-good { color: var(--accent); }
.mini-sep { color: #555; }
.mini-tick { width: 5px; height: 5px; border-radius: 50%; background: #444; margin-left: 3px; transition: background .25s ease, box-shadow .25s ease; }
.mini-tick.is-on { background: var(--accent); box-shadow: 0 0 6px rgba(110,231,165,.8); }
.mini-bar.is-tick { animation: miniTick .5s ease; }
@keyframes miniTick { 0% { opacity: .5; } 50% { opacity: 1; } 100% { opacity: .82; } }

@media (prefers-reduced-motion: reduce) { *, *::before, *::after { animation-duration: .001ms !important; animation-iteration-count: 1 !important; transition-duration: .001ms !important; } }
</style>
