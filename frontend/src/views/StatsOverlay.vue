<script setup>
import StatsOverlayChart from "@/components/charts/StatsOverlayChart.vue";
import { getHomeMetricsSummary, fetchLocalCacheStats, setStatsOverlayAlwaysOnTop, updateStatsOverlayLayout } from "@/services/clientApi";
import { getStatsOverlayPreferences, setStatsOverlayPreferences, hideStatsOverlay, closeApplication, appState } from "@/state/appState";
import { usePolling } from "@/composables/usePolling";
import { formatCompactInteger } from "@/utils/numberFormat";
import { computed, onMounted, onUnmounted, ref, watch } from "vue";

const summary = ref({});
const localCache = ref({});
const preferences = ref({ style: "card" });
const updated = ref(false);
const chartRefreshKey = ref(0);
const isCollapsed = ref(false); // 收缩状态
const isHovering = ref(false); // 鼠标悬停状态
const isMorphing = ref(false); // 胶囊与面板之间的过渡状态
const morphDirection = ref(null); // 'in' | 'out'
const snapEdge = ref(null); // 吸附边缘: 'left' | 'right' | 'top' | 'bottom' | null
let updatedTimer = null;
let hoverCollapseTimer = null;
let morphTimer = null;
let lastSavedX = null;
let lastSavedY = null;

const REFRESH_MS = 10000;
const POSITION_CHECK_MS = 300;       // 轮询提速，响应更灵敏
const SNAP_ENTER_THRESHOLD = 40;     // 进入吸附区的距离（像素）
const SNAP_LEAVE_THRESHOLD = 80;     // 离开吸附区的距离——比进入阈值大，形成迟滞防抖动
const HOVER_COLLAPSE_DELAY_MS = 220;  // 给原生窗口完成展开/定位留出时间，避免悬停反馈循环
const MORPH_MS = 300;
const MORPH_OUT_MS = 360;
const NATIVE_RESIZE_SETTLE_MS = 90;

// snapCollapse 是否启用贴边自动收缩（来自 preferences）
const snapCollapse = computed(() => preferences.value.snapCollapse !== false);
const alwaysOnTop = computed(() => preferences.value.alwaysOnTop !== false);
// dockLocked：锁定为收缩胶囊，鼠标悬停不再展开，且窗口不可拖动。
// 同时由设置开关和浮窗内锁按钮控制（同一字段）。
const dockLocked = computed(() => preferences.value.dockLocked === true);
// wails 窗口拖拽：锁定时整体禁用拖动。drag.js 在每次 mousedown 实时读取此 CSS 变量。
const draggable = computed(() => (dockLocked.value ? "no-drag" : "drag"));

async function toggleDockLock() {
  const next = !dockLocked.value;
  await setStatsOverlayPreferences({ dockLocked: next });
}

async function toggleSnapCollapse() {
  const next = !snapCollapse.value;
  await setStatsOverlayPreferences({ snapCollapse: next });
}

async function handleHideOverlay() {
  finishMorph();
  await hideStatsOverlay();
}

async function handleCloseApplication() {
  finishMorph();
  await closeApplication();
}

function clearMorphTimer() {
  if (morphTimer) {
    clearTimeout(morphTimer);
    morphTimer = null;
  }
}

function clearHoverCollapseTimer() {
  if (hoverCollapseTimer) {
    clearTimeout(hoverCollapseTimer);
    hoverCollapseTimer = null;
  }
}

function finishMorph() {
  clearMorphTimer();
  isMorphing.value = false;
  morphDirection.value = null;
}

// 展开前先把原生窗口扩到目标尺寸，动画期间保持胶囊停在贴边位置。
function startExpandMorph() {
  if (dockLocked.value || !snapEdge.value || !snapCollapse.value) return;
  if (!isCollapsed.value && !isMorphing.value) return;
  if (isMorphing.value && morphDirection.value === "in") return;
  clearMorphTimer();
  morphDirection.value = "in";
  isMorphing.value = true;
  if (!isCollapsed.value) {
    morphTimer = setTimeout(finishMorph, MORPH_MS);
    return;
  }
  morphTimer = setTimeout(() => {
    morphTimer = null;
    isCollapsed.value = false;
    morphTimer = setTimeout(finishMorph, MORPH_MS);
  }, NATIVE_RESIZE_SETTLE_MS);
}

// 展开态先播放反向动画，动画完成后才缩小原生窗口，避免窗口位置瞬移。
function startCollapseMorph() {
  if (dockLocked.value || !snapEdge.value || !snapCollapse.value || isCollapsed.value) return;
  if (isMorphing.value && morphDirection.value === "out") return;
  clearMorphTimer();
  morphDirection.value = "out";
  isMorphing.value = true;
  morphTimer = setTimeout(() => {
    morphTimer = null;
    isCollapsed.value = true;
    // 先保持完整原生窗口，让胶囊稳定在最终屏幕坐标，再切换到窄窗口。
    morphTimer = setTimeout(() => {
      morphTimer = null;
      morphDirection.value = null;
      isMorphing.value = false;
    }, NATIVE_RESIZE_SETTLE_MS);
  }, MORPH_OUT_MS);
}

// 监听窗口位置变化并持久化。窗口通过原生拖动移动，
// window.screenX/screenY 反映窗口在屏幕上的坐标。
function checkPosition() {
  // 锁定胶囊：保持收缩，不参与吸附判定与位置持久化
  if (dockLocked.value) {
    isCollapsed.value = true;
    return;
  }

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
        if (!isMorphing.value) isCollapsed.value = true;
      } else if (!newSnapEdge) {
        finishMorph();
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
function handleMouseEnter(event) {
  isHovering.value = true;
  clearHoverCollapseTimer();
  // 锁定胶囊时，悬停不展开
  if (dockLocked.value) return;
  if (snapEdge.value && snapCollapse.value) {
    startExpandMorph();
  }
}

function pointerIsWithinCurrentTarget(event) {
  const target = event?.currentTarget;
  if (!(target instanceof Element)) return false;
  if (event.relatedTarget instanceof Node && target.contains(event.relatedTarget)) return true;

  const clientX = Number.isFinite(event.screenX) && Number.isFinite(window.screenX)
    ? event.screenX - window.screenX
    : event.clientX;
  const clientY = Number.isFinite(event.screenY) && Number.isFinite(window.screenY)
    ? event.screenY - window.screenY
    : event.clientY;
  if (!Number.isFinite(clientX) || !Number.isFinite(clientY)) return false;

  const bounds = target.getBoundingClientRect();
  return clientX >= bounds.left && clientX < bounds.right
    && clientY >= bounds.top && clientY < bounds.bottom;
}

// 鼠标离开浮窗
function handleMouseLeave(event) {
  // 原生窗口 resize/reposition 可能合成 mouseleave；用当前窗口位置和当前目标边界重新判定指针是否真的离开。
  if (pointerIsWithinCurrentTarget(event)) return;
  isHovering.value = false;
  if (dockLocked.value) {
    return;
  }
  if (snapEdge.value && snapCollapse.value) {
    clearHoverCollapseTimer();
    hoverCollapseTimer = setTimeout(() => {
      hoverCollapseTimer = null;
      if (!isHovering.value && !dockLocked.value && snapEdge.value && snapCollapse.value) {
        startCollapseMorph();
      }
    }, HOVER_COLLAPSE_DELAY_MS);
    return;
  }
}

// syncNativeWindowSize 把当前折叠/贴边/样式状态同步到原生窗口尺寸。
//
// 关键：原生 Wails 窗口本身有固定矩形，即使 Vue 层用 pointer-events:none 让空白穿透，
// 窗口矩形仍会阻挡下方页面的点击。必须让窗口尺寸紧贴实际内容：
//   - 收缩胶囊时只缩短贴边方向的另一条轴，保持恢复态的轴尺寸不变；
//   - 展开面板时恢复到对应样式的面板尺寸。
//
// 通过布局 DSL（layout|collapsed/expanded|edge|style|x|y|screen...）传给后端，
// parseStatsOverlayLayout 解析后按 collapsed 切换 dockSize / 面板尺寸并重定位贴边。
function syncNativeWindowSize() {
  const currentStyle = style.value || "card";
  const nativeCollapsed = isCollapsed.value && !isMorphing.value;
  const collapsed = nativeCollapsed ? "collapsed" : "expanded";
  const edge = snapEdge.value || "none";
  const x = typeof window.screenX === "number" ? Math.round(window.screenX) : 0;
  const y = typeof window.screenY === "number" ? Math.round(window.screenY) : 0;
  const screenLeft = Math.round(window.screen.availLeft || 0);
  const screenTop = Math.round(window.screen.availTop || 0);
  const screenWidth = Math.round(window.screen.availWidth || window.screen.width || 0);
  const screenHeight = Math.round(window.screen.availHeight || window.screen.height || 0);
  // layout|collapsed/expanded|edge|style|x|y|screenLeft|screenTop|screenWidth|screenHeight
  const dsl = `layout|${collapsed}|${edge}|${currentStyle}|${x}|${y}|${screenLeft}|${screenTop}|${screenWidth}|${screenHeight}`;
  void updateStatsOverlayLayout(dsl);
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

// ===== 外观：透明度 / 磨砂 / 主题色 =====
// 主题色表：accent 强调色、glow 辉光色（含 alpha 分量，供 rgba() 直接拼装）。
const ACCENT_PALETTE = {
  mint:  { accent: "#6ee7a5", glow: "110,231,165" },
  cyan:  { accent: "#5eead4", glow: "94,234,212" },
  amber: { accent: "#fcd34d", glow: "252,211,77" },
  violet:{ accent: "#c4b5fd", glow: "196,181,253" },
  rose:  { accent: "#fda4af", glow: "253,164,175" },
  blue:  { accent: "#93c5fd", glow: "147,197,253" },
};
const overlayOpacity = computed(() => {
  const value = Number(preferences.value.opacity);
  return Number.isFinite(value) ? Math.min(1, Math.max(0.3, value)) : 0.85;
});
const overlayFrosted = computed(() => {
  const blur = Number(preferences.value.frostBlur);
  return Number.isFinite(blur) ? blur > 0 : preferences.value.frosted !== false;
});
const overlayFrostBlur = computed(() => {
  const blur = Number(preferences.value.frostBlur);
  return Number.isFinite(blur) ? Math.min(30, Math.max(0, blur)) : 18;
});
// custom：自定义色；rainbow：流动炫彩动画（CSS 变量由动画驱动）；其余走预设色板。
const overlayPalette = computed(() => {
  const accent = preferences.value.accent;
  if (accent === "custom") {
    const hex = String(preferences.value.accentCustom || "").trim();
    if (/^#[0-9a-fA-F]{6}$/.test(hex)) {
      const rgb = [1, 3, 5].map((index) => parseInt(hex.slice(index, index + 2), 16));
      return { accent: hex, glow: rgb.join(",") };
    }
    return ACCENT_PALETTE.mint;
  }
  if (accent === "rainbow") {
    return { accent: "var(--rainbow-accent)", glow: "var(--rainbow-glow)" };
  }
  return ACCENT_PALETTE[accent] || ACCENT_PALETTE.mint;
});
// 浮窗外观 CSS 变量：透明度作用于背景层，内容文字保持不透明以保证可读性。
const overlayStyleVars = computed(() => ({
  "--accent": overlayPalette.value.accent,
  "--accent-glow": overlayPalette.value.glow,
  "--overlay-bg-alpha": String(overlayOpacity.value),
  "--overlay-blur": overlayFrosted.value ? `${overlayFrostBlur.value}px` : "0px",
  "--overlay-saturate": overlayFrosted.value ? "140%" : "100%",
  "--pill-blur": overlayFrosted.value ? `${Math.max(8, overlayFrostBlur.value)}px` : "0px",
  "--pill-saturate": overlayFrosted.value ? "160%" : "100%",
}));
// 流动炫彩：动画切换主色与辉光 RGB。
const overlayRainbowActive = computed(() => preferences.value.accent === "rainbow");

// ===== 球体样式：中心球轮换显示 4 项指标，每 3 秒切换一次 =====
const orbCycleMs = 3000;
let orbCycleTimer = null;
const orbIndex = ref(0);
// 轮换项：命中率（进度环跟随填充）/ Token / 轮次 / 本地缓存（绝对值，环显示满圈装饰）
const orbMetrics = computed(() => [
  { label: "命中率", value: formatRate(displayRate.value), ratio: displayRate.value, good: displayRate.value != null && displayRate.value > 0.3 },
  { label: "Token", value: formatCompactInteger(totalTokens.value), ratio: null, good: false },
  { label: "轮次", value: formatCompactInteger(turnsTotal.value), ratio: null, good: false },
  { label: "本地缓存", value: formatCompactInteger(localCacheHits.value), ratio: null, good: false },
]);
const orbCurrent = computed(() => orbMetrics.value[orbIndex.value % orbMetrics.value.length]);
// 当前列表项的进度环 dasharray：命中率按比例，其余指标满圈展示
const orbDash = computed(() => {
  const r = orbCurrent.value.ratio;
  const fill = r == null ? 100 : Math.max(0, Math.min(1, Number(r || 0))) * 100;
  return `${fill} 100`;
});
function startOrbCycle() {
  stopOrbCycle();
  orbCycleTimer = setInterval(() => { orbIndex.value = (orbIndex.value + 1) % 4; }, orbCycleMs);
}
function stopOrbCycle() {
  if (orbCycleTimer) { clearInterval(orbCycleTimer); orbCycleTimer = null; }
}

// ===== 收缩态轮换：竖向贴边时一次只显示一个数值，每 3 秒切换 =====
let pillCycleTimer = null;
const pillIndex = ref(0);
const pillMetrics = computed(() => [
  { label: "命中率", value: formatRate(displayRate.value), good: displayRate.value != null && displayRate.value > 0.3 },
  { label: "Token", value: formatCompactInteger(totalTokens.value), good: false },
  { label: "轮次", value: formatCompactInteger(turnsTotal.value), good: false },
]);
const pillCurrent = computed(() => pillMetrics.value[pillIndex.value % 3]);
function startPillCycle() {
  stopPillCycle();
  pillCycleTimer = setInterval(() => { pillIndex.value = (pillIndex.value + 1) % 3; }, 3000);
}
function stopPillCycle() {
  if (pillCycleTimer) { clearInterval(pillCycleTimer); pillCycleTimer = null; }
}

// 动态 title（国际化）：tooltip 文案由 $ls 在模板渲染，这里只拼装数值部分。
const localCacheTitle = computed(() => {
  const rate = localCacheRate.value != null ? ` · ${(localCacheRate.value * 100).toFixed(1)}%` : "";
  return `${localCacheHits.value} · ${localCacheMisses.value}${rate}`;
});
const promptTitle = computed(() => promptTokens.value.toLocaleString());
const turnsTitle = computed(() => `${validTurns.value} / ${invalidTurns.value}`);

function markUpdated() {
  updated.value = true;
  chartRefreshKey.value += 1;
  if (updatedTimer) clearTimeout(updatedTimer);
  updatedTimer = setTimeout(() => { updated.value = false; }, 900);
}

async function load() {
  try {
    const [s, c] = await Promise.allSettled([getHomeMetricsSummary(), fetchLocalCacheStats()]);
    if (s.status === "fulfilled") summary.value = s.value || {};
    if (c.status === "fulfilled") localCache.value = c.value || {};
    markUpdated();
  } catch (_) {
    // 浮窗静默失败，不弹错误
  }
}

function syncPreferences() {
  const next = getStatsOverlayPreferences();
  preferences.value = { ...next };
}
watch(() => appState.statsOverlayPreferences, (next) => {
  if (next) preferences.value = { ...next };
}, { deep: true });

// 偏好可能来自主窗口或浮窗自身；统一在这里立即协调胶囊状态，避免两条交互路径漂移。
watch([snapCollapse, dockLocked], ([collapseEnabled, locked], [wasCollapseEnabled, wasLocked]) => {
  clearHoverCollapseTimer();

  if (locked) {
    finishMorph();
    isCollapsed.value = true;
    return;
  }

  if ((wasCollapseEnabled && !collapseEnabled)
    || (wasLocked && !locked && (!snapEdge.value || !collapseEnabled))) {
    finishMorph();
    isCollapsed.value = false;
    return;
  }

  if (wasLocked && !locked && snapEdge.value && collapseEnabled && isHovering.value) {
    startExpandMorph();
    return;
  }

  if (!wasCollapseEnabled && collapseEnabled && snapEdge.value && !isHovering.value) {
    startCollapseMorph();
  }
}, { flush: "sync" });

// 折叠/展开、贴边、样式变化时同步原生窗口尺寸，使窗口矩形紧贴内容、不阻挡下方页面。
watch(isCollapsed, () => syncNativeWindowSize());
watch(isMorphing, () => syncNativeWindowSize());
watch(snapEdge, () => syncNativeWindowSize());
watch(style, () => syncNativeWindowSize());
watch(alwaysOnTop, (next) => { void setStatsOverlayAlwaysOnTop(next); }, { flush: "sync" });

onMounted(() => {
  syncPreferences();
  void setStatsOverlayPreferences({ visible: true });
  // 用已保存坐标初始化，防止首次轮询把窗口初始位置（可能是屏幕中心）误覆盖掉已存的正确坐标
  const saved = getStatsOverlayPreferences();
  if (typeof saved.x === "number") lastSavedX = saved.x;
  if (typeof saved.y === "number") lastSavedY = saved.y;
  void load();
  startOrbCycle();
  startPillCycle();
  // 初始同步一次原生窗口尺寸，确保窗口矩形紧贴实际内容
  syncNativeWindowSize();
});
usePolling(load, { intervalMs: REFRESH_MS });
usePolling(checkPosition, { intervalMs: POSITION_CHECK_MS });

onUnmounted(() => {
  if (updatedTimer) clearTimeout(updatedTimer);
  clearHoverCollapseTimer();
  clearMorphTimer();
  stopOrbCycle();
  stopPillCycle();
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
        'is-morphing': isMorphing,
        [`is-morphing-${morphDirection}`]: morphDirection,
        [`is-snap-${snapEdge}`]: snapEdge,
        'overlay-rainbow': overlayRainbowActive,
      }
    ]" 
    :style="{ '--wails-draggable': draggable, ...overlayStyleVars }"
    @mouseenter="handleMouseEnter"
    @mouseleave="handleMouseLeave"
  >
    <!-- 收缩时的胶囊：默认横向（图标+数据并排），贴左/右边时自动转竖向 -->
    <div
      v-if="isCollapsed || isMorphing"
      class="float-pill"
      :class="{ 'is-vertical': snapEdge === 'left' || snapEdge === 'right' }"
      :style="{ '--wails-draggable': draggable }"
    >
      <div class="pill-icon">
        <svg class="pill-pulse" viewBox="0 0 24 24" aria-hidden="true">
          <circle cx="12" cy="12" r="9" class="pill-pulse-ring" />
          <circle cx="12" cy="12" r="4.5" class="pill-pulse-ring" />
          <circle cx="12" cy="12" r="1.6" class="pill-pulse-dot" />
        </svg>
      </div>
      <div class="pill-data" :key="pillIndex">
        <span class="pill-rate" :class="{ 'is-good': pillCurrent.good }">{{ pillCurrent.value }}</span>
      </div>
      <span class="pill-tick" :class="{ 'is-on': updated }"></span>
      <!-- 锁定按钮：点击锁定为收缩胶囊（悬停不再展开、不可拖动） -->
      <button
        v-if="snapEdge || dockLocked"
        class="pill-lock"
        :class="{ 'is-locked': dockLocked }"
        :title="dockLocked ? '已锁定为胶囊（点击解锁可展开）' : '锁定为胶囊（悬停不再展开）'"
        style="--wails-draggable: no-drag"
        @click.stop="toggleDockLock"
      >
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <!-- 锁定：闭合挂锁 -->
          <path v-if="dockLocked" class="lock-body" d="M7 11V8a5 5 0 0 1 10 0v3" />
          <path v-else class="lock-body" d="M7 11V8a5 5 0 0 1 9-3" />
          <rect class="lock-body" x="5" y="11" width="14" height="9" rx="2" />
        </svg>
      </button>
    </div>

    <!-- 完整面板 -->
    <div v-show="!isCollapsed" class="overlay-content">
    <header class="overlay-header">
      <button
        class="snap-toggle"
        :class="{ 'is-off': !snapCollapse }"
        :title="snapCollapse ? '贴边自动收缩：开启（点击关闭）' : '贴边自动收缩：关闭（点击开启）'"
        style="--wails-draggable: no-drag"
        @click.stop="toggleSnapCollapse"
      ><span class="icon-[mdi--dock-window]" aria-hidden="true"></span></button>
      <button
        type="button"
        class="overlay-action"
        title="隐藏浮窗"
        aria-label="隐藏浮窗"
        style="--wails-draggable: no-drag"
        @click.stop="handleHideOverlay"
      ><span class="icon-[mdi--eye-off-outline]" aria-hidden="true"></span></button>
      <button
        type="button"
        class="overlay-action overlay-action--danger"
        title="关闭应用"
        aria-label="关闭应用"
        style="--wails-draggable: no-drag"
        @click.stop="handleCloseApplication"
      ><span class="icon-[mdi--close]" aria-hidden="true"></span></button>
    </header>

    <section v-if="style === 'card'" class="card-panel" aria-label="实时统计指标">
      <div class="metric-card" :title="localCacheTitle">
        <div class="metric-label">缓存命中</div><div class="metric-value" :class="{ 'is-good': cacheHitRate != null && cacheHitRate > 0.3 }">{{ formatRate(cacheHitRate) }}</div>
      </div>
      <div class="metric-card" :title="promptTitle">
        <div class="metric-label">Token 消耗</div><div class="metric-value">{{ formatCompactInteger(totalTokens) }}</div>
      </div>
      <div class="metric-card" :title="turnsTitle">
        <div class="metric-label">对话轮次</div><div class="metric-value">{{ formatCompactInteger(turnsTotal) }}</div>
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
        <div><span>Tokens</span><strong>{{ formatCompactInteger(totalTokens) }}</strong></div>
        <div><span>Turns</span><strong>{{ formatCompactInteger(turnsTotal) }}</strong></div>
        <div><span>Local hits</span><strong>{{ formatCompactInteger(localCacheHits) }}</strong></div>
        <div><span>Prompt</span><strong>{{ formatCompactInteger(promptTokens) }}</strong></div>
      </div>
    </section>

    <section v-else class="orb-panel" aria-label="实时统计球体">
      <div class="orb-stage">
        <!-- 中心球轮换显示 4 项指标，每 3 秒切换；进度环按当前指标填充 -->
        <div class="orb-core">
          <div class="orb-glow"></div>
          <svg class="orb-arc" viewBox="0 0 120 120" aria-hidden="true">
            <circle class="orb-arc-track" cx="60" cy="60" r="52" />
            <circle class="orb-arc-value" cx="60" cy="60" r="52" pathLength="100" :stroke-dasharray="orbDash" :class="{ 'is-full': orbCurrent.ratio == null }" />
            <!-- 标志性动效：环上一颗匀速运行的小点，象征实时采集 -->
            <circle class="orb-arc-sat" cx="60" cy="8" r="2.6" />
          </svg>
          <div class="orb-readout" :key="orbIndex">
            <strong :class="{ 'is-good': orbCurrent.good }">{{ orbCurrent.value }}</strong>
            <span>{{ orbCurrent.label }}</span>
          </div>
          <!-- 指标切换指示点 -->
          <div class="orb-dots">
            <i v-for="(m, i) in orbMetrics" :key="i" :class="{ 'is-active': i === orbIndex % 4 }"></i>
          </div>
        </div>
      </div>
    </section>
    <StatsOverlayChart :refresh-key="chartRefreshKey" />
    </div>
  </div>
</template>

<style scoped>
.stats-overlay { --accent: #6ee7a5; --accent-glow: 110,231,165; --overlay-bg-alpha: 0.85; --overlay-blur: 18px; --overlay-saturate: 140%; --pill-blur: 16px; --pill-saturate: 160%; --muted: #777; width: 100vw; height: 100vh; display: flex; align-items: center; justify-content: center; overflow: hidden; background: transparent; color: #e5e5e5; font-family: inherit; pointer-events: none; }
/* 流动炫彩：主色与辉光 RGB 周期性渐变，供 --accent / --accent-glow 引用 */
.overlay-rainbow { --rainbow-accent: #6ee7a5; --rainbow-glow: 110,231,165; animation: overlayRainbowCycle 8s linear infinite; }
@keyframes overlayRainbowCycle {
  0%   { --rainbow-accent: #6ee7a5; --rainbow-glow: 110,231,165; }
  17%  { --rainbow-accent: #5eead4; --rainbow-glow: 94,234,212; }
  34%  { --rainbow-accent: #93c5fd; --rainbow-glow: 147,197,253; }
  51%  { --rainbow-accent: #c4b5fd; --rainbow-glow: 196,181,253; }
  68%  { --rainbow-accent: #fda4af; --rainbow-glow: 253,164,175; }
  85%  { --rainbow-accent: #fcd34d; --rainbow-glow: 252,211,77; }
  100% { --rainbow-accent: #6ee7a5; --rainbow-glow: 110,231,165; }
}
.stats-overlay,
.stats-overlay * { user-select: none; -webkit-user-select: none; }
.stats-overlay > * { pointer-events: auto; }

/* 悬浮球样式 */
/* 收缩态胶囊：左侧脉冲图标 + 右侧实时数据，一体紧凑。
   背景不透明 + 强边框/投影，确保在任意（含白色）背景下都可读。 */
.float-pill { position: relative; display: flex; align-items: center; gap: 6px; height: 30px; padding: 0 10px; border-radius: 15px; background: linear-gradient(135deg, rgba(26,32,31,.78), rgba(14,18,18,.82)); border: 1px solid rgba(var(--accent-glow),.55); box-shadow: 0 4px 14px rgba(0,0,0,.5), 0 0 0 1px rgba(0,0,0,.2), inset 0 1px 0 rgba(255,255,255,.08); backdrop-filter: blur(var(--pill-blur)) saturate(var(--pill-saturate)); -webkit-backdrop-filter: blur(var(--pill-blur)) saturate(var(--pill-saturate)); cursor: pointer; transition: border-color .3s ease; }
.float-pill:hover { border-color: rgba(var(--accent-glow),.85); }
/* 竖向贴边：贴左/右边时图标在上、轮换数值在下，竖排不旋转 */
.float-pill.is-vertical { flex-direction: column; gap: 4px; width: 30px; height: auto; padding: 7px 0; border-radius: 15px; }
.float-pill.is-vertical .pill-data { line-height: 1.2; }
.float-pill.is-vertical .pill-rate { font-size: 10px; }
.float-pill.is-vertical .pill-tick { margin: 2px 0 0; }
.pill-icon { position: relative; width: 16px; height: 16px; flex-shrink: 0; display: grid; place-items: center; }
.pill-pulse { width: 16px; height: 16px; overflow: visible; }
.pill-pulse-ring { fill: none; stroke: rgba(var(--accent-glow),.85); stroke-width: 1.4; }
.pill-pulse-dot { fill: var(--accent); filter: drop-shadow(0 0 3px rgba(var(--accent-glow),.95)); }
.pill-data { display: flex; align-items: center; gap: 5px; font: 600 11px var(--font-num, ui-monospace, monospace); letter-spacing: .01em; white-space: nowrap; }
.pill-rate { color: #eafff4; }
.pill-rate.is-good { color: var(--accent); }
.pill-sep { color: #5a5a5a; }
.pill-tokens, .pill-turns { color: #d2d2d2; }
.pill-tick { width: 5px; height: 5px; border-radius: 50%; background: #444; margin-left: 2px; flex-shrink: 0; transition: background .25s ease, box-shadow .25s ease; }
.pill-tick.is-on { background: var(--accent); box-shadow: 0 0 6px rgba(var(--accent-glow),.85); }
/* 锁定按钮：迷你锁形，融入胶囊；锁定态高亮 */
.pill-lock { width: 14px; height: 14px; flex-shrink: 0; display: grid; place-items: center; padding: 0; border: none; background: none; cursor: pointer; opacity: .55; transition: opacity .2s ease; }
.pill-lock:hover { opacity: 1; }
.pill-lock:disabled { cursor: default; opacity: .8; }
.pill-lock svg { width: 14px; height: 14px; }
.lock-body { fill: none; stroke: #c8c8c8; stroke-width: 1.8; stroke-linecap: round; stroke-linejoin: round; }
.pill-lock.is-locked .lock-body { stroke: var(--accent); }
.pill-lock.is-locked { opacity: .95; }

@keyframes pillMorphOut {
  from { opacity: 1; scale: 1; }
  to { opacity: 0; scale: 1; }
}
@keyframes pillMorphIn {
  from { opacity: 0; scale: 1; }
  to { opacity: 1; scale: 1; }
}
@keyframes overlayMorphIn {
  from { opacity: 0; scale: .82; }
  to { opacity: 1; scale: 1; }
}
@keyframes overlayMorphOut {
  from { opacity: 1; scale: 1; }
  to { opacity: 0; scale: .72; }
}

/* 完整面板容器 */
.overlay-content {
  display: flex;
  flex-direction: column;
  gap: 6px;
  width: 100%;
  min-width: 0;
  min-height: 0;
  padding: 6px 8px;
  border: 1px solid rgba(var(--accent-glow), 0.4);
  border-radius: 10px;
  background: linear-gradient(135deg, rgba(20, 27, 25, var(--overlay-bg-alpha)), rgba(10, 15, 15, var(--overlay-bg-alpha)));
  backdrop-filter: blur(var(--overlay-blur)) saturate(var(--overlay-saturate));
  -webkit-backdrop-filter: blur(var(--overlay-blur)) saturate(var(--overlay-saturate));
  box-shadow: 0 8px 22px rgba(0, 0, 0, 0.48), 0 0 0 1px rgba(0, 0, 0, 0.24);
  overflow: visible;
}
.stats-overlay--card .overlay-content,
.stats-overlay--engine .overlay-content { width: 240px; }
.stats-overlay--orb .overlay-content { width: 176px; }

/* 收缩状态：完整面板隐藏 */
.stats-overlay.is-collapsed .overlay-content { display: none; }

/* 收缩状态：容器收缩到悬浮球大小，避免大块背景 */
.stats-overlay.is-collapsed { width: auto; height: auto; }

/* 吸附态只缩短远离边缘的轴，保持另一轴与恢复态一致。 */
.stats-overlay.is-collapsed.is-snap-left,
.stats-overlay.is-collapsed.is-snap-right,
.stats-overlay.is-collapsed.is-snap-top,
.stats-overlay.is-collapsed.is-snap-bottom { width: 100vw; height: 100vh; }

/* 收缩完成后仍保持与形变阶段相同的边缘坐标，避免切回 flex 居中造成一帧闪烁。 */
.stats-overlay.is-collapsed.is-snap-left .float-pill { position: absolute; left: 7px; top: 50%; transform: translateY(-50%); }
.stats-overlay.is-collapsed.is-snap-right .float-pill { position: absolute; right: 7px; top: 50%; transform: translateY(-50%); }
/* 上下贴边的原生胶囊窗口高度只有 36px，必须以窗口中心定位，避免 18px 偏移把胶囊裁掉。 */
.stats-overlay.is-collapsed:not(.is-morphing).is-snap-top .float-pill { position: absolute; left: 50%; top: 50%; transform: translate(-50%, -50%); }
.stats-overlay.is-collapsed:not(.is-morphing).is-snap-bottom .float-pill { position: absolute; left: 50%; top: 50%; bottom: auto; transform: translate(-50%, -50%); }

/* 展开态沿贴边方向保持锚定，面板只向屏幕内部扩展，鼠标始终落在面板内。 */
.stats-overlay:not(.is-collapsed).is-snap-left { justify-content: flex-start; }
.stats-overlay:not(.is-collapsed).is-snap-right { justify-content: flex-end; }
.stats-overlay:not(.is-collapsed).is-snap-top { align-items: flex-start; }
.stats-overlay:not(.is-collapsed).is-snap-bottom { align-items: flex-end; }

/* 原生窗口换尺寸期间保持完整视口，让胶囊和面板在同一坐标系内形变。 */
.stats-overlay.is-collapsed.is-morphing { width: 100vw; height: 100vh; }
.stats-overlay.is-morphing .float-pill { position: absolute; z-index: 2; margin: 0; }
.stats-overlay.is-morphing.is-snap-left .float-pill { left: 7px; top: 50%; transform: translateY(-50%); }
.stats-overlay.is-morphing.is-snap-right .float-pill { right: 7px; top: 50%; transform: translateY(-50%); }
.stats-overlay.is-morphing.is-snap-top .float-pill { left: 50%; top: 18px; transform: translateX(-50%); }
.stats-overlay.is-morphing.is-snap-bottom .float-pill { left: 50%; bottom: 18px; transform: translateX(-50%); }
.stats-overlay.is-morphing .overlay-content {
  position: absolute;
  z-index: 1;
  margin: 0;
  opacity: 0;
  transform-origin: center;
  will-change: opacity, scale;
}
.stats-overlay.is-morphing.is-snap-left .overlay-content { left: 0; top: 50%; transform: translateY(-50%); transform-origin: left center; }
.stats-overlay.is-morphing.is-snap-right .overlay-content { right: 0; top: 50%; transform: translateY(-50%); transform-origin: right center; }
.stats-overlay.is-morphing.is-snap-top .overlay-content { left: 50%; top: 0; transform: translateX(-50%); transform-origin: center top; }
.stats-overlay.is-morphing.is-snap-bottom .overlay-content { left: 50%; bottom: 0; transform: translateX(-50%); transform-origin: center bottom; }
.stats-overlay.is-morphing .float-pill { will-change: opacity, scale; }
.stats-overlay.is-morphing-in .overlay-content { animation: overlayMorphIn .3s cubic-bezier(.22,.8,.24,1) both; }
.stats-overlay.is-morphing-out .overlay-content { animation: overlayMorphOut .36s cubic-bezier(.22,.8,.24,1) both; }
.stats-overlay.is-morphing-in .float-pill { animation: pillMorphOut .3s cubic-bezier(.22,.8,.24,1) .09s both; }
.stats-overlay.is-morphing-out .float-pill { animation: pillMorphIn .36s cubic-bezier(.22,.8,.24,1) both; }

.overlay-header { height: 15px; flex: 0 0 15px; display: flex; align-items: center; justify-content: flex-end; gap: 3px; padding: 0 2px; transition: opacity 0.3s ease; }

/* 贴标收缩开关按钮 */
.snap-toggle { width: 10px; height: 10px; border: 1.5px solid rgba(var(--accent-glow),.55); border-radius: 2px; background: none; cursor: pointer; padding: 0; flex-shrink: 0; position: relative; transition: border-color .2s, opacity .2s; }
.snap-toggle::after { content: ''; position: absolute; top: 50%; left: 50%; transform: translate(-50%, -50%); width: 4px; height: 4px; border-radius: 50%; background: rgba(var(--accent-glow),.75); transition: background .2s; }
.snap-toggle.is-off { border-color: rgba(100,100,100,.45); }
.snap-toggle.is-off::after { background: rgba(100,100,100,.45); }
.snap-toggle:hover { border-color: rgba(var(--accent-glow),.9); opacity: 1; }
.snap-toggle.is-off:hover { border-color: rgba(150,150,150,.7); }
.snap-toggle > span { font-size: 9px; line-height: 1; color: rgba(190,220,205,.85); }
.overlay-action { width: 13px; height: 13px; display: grid; place-items: center; border: 0; border-radius: 3px; padding: 0; color: rgba(180,180,180,.75); background: transparent; cursor: pointer; transition: color .2s ease, background .2s ease; }
.overlay-action span { font-size: 12px; line-height: 1; }
.overlay-action:hover { color: #fff; background: rgba(255,255,255,.12); }
.overlay-action--danger:hover { color: #ffb4b4; background: rgba(180,60,60,.2); }

.card-panel { display: grid; flex: 0 0 auto; min-height: 0; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 4px; padding: 4px; border: 1px solid rgba(var(--accent-glow),.4); border-radius: 8px; background: linear-gradient(135deg, rgba(24,24,24,var(--overlay-bg-alpha)), rgba(16,16,16,var(--overlay-bg-alpha))); backdrop-filter: blur(var(--overlay-blur)) saturate(var(--overlay-saturate)); -webkit-backdrop-filter: blur(var(--overlay-blur)) saturate(var(--overlay-saturate)); transition: opacity 0.3s ease; box-shadow: 0 4px 16px rgba(0,0,0,.4), 0 0 0 1px rgba(0,0,0,.2); }
.metric-card { min-width: 0; padding: 3px 6px; border: 1px solid rgba(var(--accent-glow),.18); border-radius: 5px; background: rgba(36,36,36,.9); transition: border-color .3s ease, transform .3s ease; }
.is-updated .metric-card { border-color: rgba(var(--accent-glow),.55); }
.metric-label { color: #777; font-size: 9px; white-space: nowrap; }
.metric-value { color: #fff; font-family: var(--font-num, ui-monospace, monospace); font-size: 14px; line-height: 1.15; transition: color .35s ease, text-shadow .35s ease; }
.metric-value.is-good, .metric-value--good { color: var(--accent); }
.is-updated .metric-value { text-shadow: 0 0 6px rgba(var(--accent-glow),.7); }

/* ===== 卡片样式动效：四卡错峰入场 + 刷新时数值高亮微跳 ===== */
.card-panel .metric-card { animation: cardEnter .4s ease backwards; }
.card-panel .metric-card:nth-child(1) { animation-delay: .02s; }
.card-panel .metric-card:nth-child(2) { animation-delay: .09s; }
.card-panel .metric-card:nth-child(3) { animation-delay: .16s; }
.card-panel .metric-card:nth-child(4) { animation-delay: .23s; }
.is-updated .card-panel .metric-value { animation: valueBlink .9s ease; }
@keyframes cardEnter { from { opacity: 0; transform: translateY(4px); } to { opacity: 1; transform: translateY(0); } }
@keyframes valueBlink { 0%,100% { transform: scale(1); } 35% { transform: scale(1.08); } }

/* ===== 引擎样式动效：刷新时仪表脉动 + 遥测格流光 ===== */
.engine-gauge { transition: filter .4s ease; }
.is-updated .engine-gauge { animation: gaugeBeat .9s ease; }
.is-updated .telemetry-grid div { animation: telemetryFlash .9s ease; }
.telemetry-grid div:nth-child(2) { animation-delay: .08s; }
.telemetry-grid div:nth-child(3) { animation-delay: .16s; }
.telemetry-grid div:nth-child(4) { animation-delay: .24s; }
@keyframes gaugeBeat { 0%,100% { filter: drop-shadow(0 0 0 rgba(var(--accent-glow),0)); } 45% { filter: drop-shadow(0 0 5px rgba(var(--accent-glow),.7)); } }
@keyframes telemetryFlash { 0%,100% { box-shadow: inset 2px 0 0 transparent; } 45% { box-shadow: inset 2px 0 0 rgba(var(--accent-glow),.9); } }

/* ===== 球体样式动效：刷新时核心涟漪扩散 ===== */
.is-updated .orb-core::before { animation: orbRipple .9s ease; }
.orb-core::before { content: ''; position: absolute; inset: 30%; border-radius: 50%; border: 1px solid rgba(var(--accent-glow),.6); opacity: 0; pointer-events: none; }
@keyframes orbRipple { 0% { opacity: .8; inset: 30%; } 100% { opacity: 0; inset: -10%; } }
.engine-panel { position: relative; display: flex; flex: 0 0 86px; align-items: center; gap: 8px; height: 86px; min-height: 86px; padding: 5px 7px; border: 1px solid rgba(var(--accent-glow),.45); border-radius: 9px; background: linear-gradient(135deg, rgba(18,39,32,var(--overlay-bg-alpha)), rgba(12,21,25,var(--overlay-bg-alpha))); backdrop-filter: blur(var(--overlay-blur)) saturate(var(--overlay-saturate)); -webkit-backdrop-filter: blur(var(--overlay-blur)) saturate(var(--overlay-saturate)); overflow: hidden; transition: opacity 0.3s ease; box-shadow: 0 4px 16px rgba(0,0,0,.4), 0 0 0 1px rgba(0,0,0,.2); }
.engine-panel::after { content: ''; position: absolute; inset: 0; background: repeating-linear-gradient(0deg, transparent 0 11px, rgba(var(--accent-glow),.07) 12px); animation: scan 8s linear infinite; pointer-events: none; }
.engine-gauge { position: relative; flex: 0 0 74px; height: 74px; }
.engine-gauge svg { width: 100%; height: 100%; transform: rotate(-90deg); }
.gauge-track, .gauge-value { fill: none; stroke-width: 7; }
.gauge-track { stroke: rgba(120,170,165,.2); }
.gauge-value { stroke: var(--accent); stroke-linecap: round; transition: stroke-dasharray .7s ease; }
.gauge-center { position: absolute; inset: 0; display: flex; flex-direction: column; align-items: center; justify-content: center; }
.gauge-center strong { color: #f5fff9; font: 600 14px var(--font-num, ui-monospace, monospace); }
.gauge-center span { color: #8da9a4; font-size: 8px; }
.telemetry-grid { z-index: 1; display: grid; flex: 1; grid-template-columns: repeat(2, minmax(0,1fr)); gap: 5px; }
.telemetry-grid div { padding: 3px 5px; border-left: 2px solid rgba(var(--accent-glow),.6); background: rgba(5,15,17,.36); }
.telemetry-grid span { display: block; color: #77918e; font-size: 8px; white-space: nowrap; }
.telemetry-grid strong { color: #d8eee8; font: 12px var(--font-num, ui-monospace, monospace); }
.orb-panel { position: relative; width: 100%; height: 106px; min-height: 106px; flex: 0 0 106px; overflow: visible; display: grid; place-items: center; }
/* 球体舞台：去掉四角卡片，中心球独占舞台，轮换展示指标 */
.orb-stage { position: relative; display: grid; place-items: center; width: 100%; height: 100%; }
.orb-core { position: relative; width: 104px; height: 104px; display: grid; place-items: center; border-radius: 50%; background: linear-gradient(135deg, rgba(18,28,26,var(--overlay-bg-alpha)), rgba(10,16,16,var(--overlay-bg-alpha))); backdrop-filter: blur(14px) saturate(160%); -webkit-backdrop-filter: blur(14px) saturate(160%); box-shadow: 0 4px 16px rgba(0,0,0,.35), inset 0 1px 0 rgba(255,255,255,.06); }
.orb-glow { position: absolute; inset: -10px; border-radius: 50%; background: radial-gradient(circle, rgba(var(--accent-glow),.3) 0%, rgba(var(--accent-glow),0) 70%); filter: blur(8px); animation: orbPulse 4s ease-in-out infinite; }
.orb-arc { position: absolute; inset: 0; width: 100%; height: 100%; transform: rotate(-90deg); }
.orb-arc-track { fill: none; stroke: rgba(120,170,165,.2); stroke-width: 2.5; }
.orb-arc-value { fill: none; stroke: var(--accent); stroke-width: 2.5; stroke-linecap: round; transition: stroke-dasharray .6s ease; }
/* 非比例指标（Token/轮次/缓存）进度环满圈展示，并降低亮度作为装饰 */
.orb-arc-value.is-full { stroke: rgba(var(--accent-glow),.32); }
/* 标志性动效：让环上的小点绕中心匀速运行 */
.orb-arc-sat { fill: var(--accent); transform-origin: 60px 60px; animation: orbSatSpin 6s linear infinite; }
/* 读数区：key 变化时触发淡入切换 */
.orb-readout { position: relative; z-index: 1; display: flex; flex-direction: column; align-items: center; gap: 2px; animation: orbSwap .45s ease; }
.orb-readout strong { color: #eafff4; font: 600 18px var(--font-num, ui-monospace, monospace); letter-spacing: -.02em; line-height: 1; }
.orb-readout strong.is-good { color: var(--accent); }
.orb-readout span { color: #8da9a4; font-size: 8px; letter-spacing: .08em; }
/* 指标切换指示点：当前项高亮 */
.orb-dots { position: absolute; bottom: 22%; left: 50%; transform: translateX(-50%); display: flex; gap: 4px; z-index: 1; }
.orb-dots i { width: 4px; height: 4px; border-radius: 50%; background: rgba(141,169,164,.45); transition: background .3s ease, box-shadow .3s ease; }
.orb-dots i.is-active { background: var(--accent); box-shadow: 0 0 5px rgba(var(--accent-glow),.8); }
@keyframes scan { from { transform: translateY(-12px); } to { transform: translateY(12px); } }
@keyframes orbSatSpin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
@keyframes orbPulse { 0%,100% { opacity: .55; transform: scale(.94); } 50% { opacity: .85; transform: scale(1.04); } }
@keyframes orbSwap { from { opacity: 0; transform: translateY(4px); } to { opacity: 1; transform: translateY(0); } }

@media (prefers-reduced-motion: reduce) { *, *::before, *::after { animation-duration: .001ms !important; animation-iteration-count: 1 !important; transition-duration: .001ms !important; } }
</style>
