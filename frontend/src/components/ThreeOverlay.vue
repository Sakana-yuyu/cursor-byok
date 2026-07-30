<script setup>
import { ref, watch, onMounted, onBeforeUnmount, computed } from 'vue';
import { useThreeScene } from '@/composables/useThreeScene';
import { useThreeCard } from '@/composables/useThreeCard';
import { useThreeEngine } from '@/composables/useThreeEngine';
import { useThreeOrb } from '@/composables/useThreeOrb';
import { usePerformanceMonitor } from '@/composables/usePerformanceMonitor';
import { QUALITY_PRESETS } from '@/config/threeConfig';

const props = defineProps({
  style: {
    type: String,
    default: 'card',
    validator: (value) => ['card', 'engine', 'orb'].includes(value),
  },
  isCollapsed: {
    type: Boolean,
    default: false,
  },
  data: {
    type: Object,
    default: () => ({
      cacheHitRate: 0,
      totalTokens: 0,
      turnsTotal: 0,
      localCacheHits: 0,
      promptTokens: 0,
    }),
  },
  loading: {
    type: Boolean,
    default: false,
  },
  updated: {
    type: Boolean,
    default: false,
  },
});

const containerRef = ref(null);
const currentLayout = ref(null);
const isInitialized = ref(false);

// 初始化场景基础设施
const {
  scene,
  camera,
  getRenderer,
  initRenderer,
  startLoop,
  stopLoop,
  handleResize,
  setPixelRatio,
  dispose: disposeScene,
} = useThreeScene(containerRef);

// 性能监控
const {
  fps,
  qualityLevel,
  measureFrame,
  onQualityChange,
  setQualityLevel,
} = usePerformanceMonitor();

// 检测移动设备
const isMobile = computed(() => {
  return /Android|webOS|iPhone|iPad|iPod|BlackBerry|IEMobile|Opera Mini/i.test(
    navigator.userAgent
  );
});

// 初始化布局
function initLayout(layoutType, quality) {
  // 清理旧布局
  if (currentLayout.value) {
    currentLayout.value.dispose();
    currentLayout.value = null;
  }

  // 创建新布局
  let layout;
  switch (layoutType) {
    case 'engine':
      layout = useThreeEngine(scene, camera);
      break;
    case 'orb':
      layout = useThreeOrb(scene, camera);
      break;
    case 'card':
    default:
      layout = useThreeCard(scene, camera);
      break;
  }

  layout.init(quality);
  currentLayout.value = layout;
}

// 更新循环
function updateLoop(time) {
  measureFrame();

  if (currentLayout.value) {
    const dataWithFlags = {
      ...props.data,
      updated: props.updated,
      loading: props.loading,
    };
    currentLayout.value.update(dataWithFlags, time);
  }
}

// 响应质量等级变化
const unsubscribe = onQualityChange((level, reason) => {
  console.log(`[ThreeOverlay] Quality changed to ${level} (${reason})`);
  
  if (currentLayout.value) {
    currentLayout.value.adjustQuality(level);
  }

  // 调整渲染器 pixelRatio
  const preset = getQualityPreset(level);
  const dpr = window.devicePixelRatio;
  const ratio = Math.min(dpr, preset.pixelRatioMax);
  setPixelRatio(ratio);
});

function getQualityPreset(level) {
  if (level === 2) return QUALITY_PRESETS.high;
  if (level === 1) return QUALITY_PRESETS.medium;
  return QUALITY_PRESETS.low;
}

// 监听布局切换
watch(() => props.style, (newStyle, oldStyle) => {
  if (!isInitialized.value || newStyle === oldStyle) return;
  
  console.log(`[ThreeOverlay] Switching layout: ${oldStyle} → ${newStyle}`);
  initLayout(newStyle, qualityLevel.value);
});

// 监听收缩状态
watch(() => props.isCollapsed, (collapsed) => {
  if (currentLayout.value) {
    currentLayout.value.setCollapsed(collapsed);
  }
});

// 窗口 resize 监听
let resizeObserver = null;

onMounted(() => {
  // 移动设备默认低质量
  if (isMobile.value) {
    setQualityLevel(0);
  }

  // 初始化渲染器
  const initialQuality = qualityLevel.value;
  const preset = getQualityPreset(initialQuality);
  initRenderer(preset.antialias);

  // 设置 pixelRatio
  const dpr = window.devicePixelRatio;
  const ratio = Math.min(dpr, preset.pixelRatioMax);
  setPixelRatio(ratio);

  // 初始化布局
  initLayout(props.style, initialQuality);

  // 启动渲染循环
  startLoop(updateLoop);

  isInitialized.value = true;

  // 监听容器尺寸变化
  resizeObserver = new ResizeObserver(() => {
    handleResize();
  });

  if (containerRef.value) {
    resizeObserver.observe(containerRef.value);
  }
});

onBeforeUnmount(() => {
  unsubscribe();

  if (resizeObserver) {
    resizeObserver.disconnect();
    resizeObserver = null;
  }

  stopLoop();

  if (currentLayout.value) {
    currentLayout.value.dispose();
    currentLayout.value = null;
  }

  disposeScene();
  isInitialized.value = false;
});
</script>

<template>
  <div ref="containerRef" class="three-overlay">
    <!-- Canvas 由 Three.js 渲染器自动插入 -->
  </div>
</template>

<style scoped>
.three-overlay {
  position: absolute;
  inset: 0;
  z-index: -1;
  pointer-events: none;
  overflow: hidden;
}

.three-overlay :deep(canvas) {
  display: block;
  width: 100%;
  height: 100%;
}
</style>