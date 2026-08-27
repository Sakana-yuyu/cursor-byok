<script setup>
import { computed } from "vue";

// 纯 SVG 半圆环：原实现依赖 chart.js（仅此一处使用，却拖入整套引擎与依赖）。
const RADIUS = 46;
const HALF_CIRCUMFERENCE = Math.PI * RADIUS;

const props = defineProps({
  rate: {
    type: Number,
    default: 0,
  },
});

const percentage = computed(() => {
  const rate = Number(props.rate);
  if (!Number.isFinite(rate)) {
    return 0;
  }
  return Math.max(0, Math.min(100, rate * 100));
});

const label = computed(() => {
  const rate = Number(props.rate);
  if (!Number.isFinite(rate)) {
    return "--";
  }
  return `${percentage.value.toFixed(2)}%`;
});

const hitDash = computed(
  () => (percentage.value / 100) * HALF_CIRCUMFERENCE,
);
</script>

<template>
  <div class="flex flex-col items-center gap-2">
    <div
      class="relative h-[64px] w-[100px] shrink-0"
      role="img"
      :aria-label="`缓存命中率 ${label}`"
    >
      <svg
        class="h-full w-full"
        viewBox="0 0 100 64"
        aria-hidden="true"
      >
        <path
          d="M 4 58 A 46 46 0 0 1 96 58"
          fill="none"
          stroke="#373737"
          stroke-width="9"
          stroke-linecap="round"
        />
        <path
          class="cache-hit-arc"
          d="M 4 58 A 46 46 0 0 1 96 58"
          fill="none"
          stroke="#4ade80"
          stroke-width="9"
          stroke-linecap="round"
          :stroke-dasharray="`${hitDash} ${HALF_CIRCUMFERENCE}`"
        />
      </svg>
      <div class="pointer-events-none absolute inset-x-0 bottom-[8px] flex justify-center">
        <div
          class="text-[16px] leading-none text-white"
          style="font-family: var(--font-num)"
        >
          {{ label }}
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.cache-hit-arc {
  transition: stroke-dasharray 0.45s ease;
}
</style>
