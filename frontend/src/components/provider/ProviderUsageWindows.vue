<script setup>
import {
  formatProviderUsageWindowPercent,
  formatProviderUsageWindowReset,
  normalizeProviderUsageWindows,
} from "@/utils/providerUsageWindows";
import { useLocale } from "@/i18n/runtime";
import { computed } from "vue";

const props = defineProps({
  windows: { type: Array, default: () => [] },
  variant: { type: String, default: "stacked" },
  maxItems: { type: Number, default: 0 },
});
const { locale } = useLocale();

const normalizedWindows = computed(() => normalizeProviderUsageWindows(props.windows));
const visibleWindows = computed(() => {
  const maximum = Number(props.maxItems);
  if (!Number.isFinite(maximum) || maximum <= 0 || normalizedWindows.value.length <= maximum) {
    return normalizedWindows.value;
  }
  const severity = { exhausted: 3, warning: 2, unknown: 1, ok: 0 };
  return [...normalizedWindows.value]
    .sort((left, right) => (severity[right.status] || 0) - (severity[left.status] || 0))
    .slice(0, maximum);
});
const hiddenWindowCount = computed(() => normalizedWindows.value.length - visibleWindows.value.length);

function remainingLabel(window) {
  if (window.remainingPercent === null) return "用量未知";
  return `剩余 ${formatProviderUsageWindowPercent(window.remainingPercent)}`;
}

function hiddenWindowsLabel() {
  return `另有 ${hiddenWindowCount.value} 个额度窗口`;
}

function resetLabel(window) {
  const reset = formatProviderUsageWindowReset(window.resetsAt, locale.value);
  return reset ? `重置 ${reset}` : "";
}

function windowTitle(window) {
  const parts = [window.label];
  if (window.usedPercent !== null) parts.push(`已用 ${formatProviderUsageWindowPercent(window.usedPercent)}`);
  if (window.remainingPercent !== null) parts.push(`剩余 ${formatProviderUsageWindowPercent(window.remainingPercent)}`);
  const reset = resetLabel(window);
  if (reset) parts.push(reset);
  return parts.join(" · ");
}

function barWidth(window) {
  const value = window.usedPercent === null ? 0 : Math.min(100, Math.max(0, window.usedPercent));
  return `${value}%`;
}

function statusDotClass(status) {
  if (status === "exhausted") return "bg-[#f87171]";
  if (status === "warning") return "bg-[#fbbf24]";
  if (status === "ok") return "bg-[#6ee7a5]";
  return "bg-[#737373]";
}

function statusTextClass(status) {
  if (status === "exhausted") return "text-[#fca5a5]";
  if (status === "warning") return "text-[#fcd34d]";
  if (status === "ok") return "text-[#a7f3d0]";
  return "text-[#a3a3a3]";
}

function statusBarClass(status) {
  if (status === "exhausted") return "bg-[#f87171]";
  if (status === "warning") return "bg-[#fbbf24]";
  if (status === "ok") return "bg-[#10AD5D]";
  return "bg-[#737373]";
}
</script>

<template>
  <span
    v-if="variant === 'inline' && visibleWindows.length"
    class="contents"
  >
    <span
      v-for="window in visibleWindows"
      :key="window.id"
      class="center-row shrink-0 gap-1 whitespace-nowrap rounded-full border border-[#3a3a3a] bg-[#252525]/70 px-2 py-0.5 text-[11px]"
      :class="statusTextClass(window.status)"
      :title="windowTitle(window)"
      data-testid="provider-usage-window"
    >
      <span
        class="size-1.5 shrink-0 rounded-full"
        :class="statusDotClass(window.status)"
      />
      <span>{{ window.label }}</span>
      <span
        class="font-medium"
        style="font-family: var(--font-num)"
      >
        {{ remainingLabel(window) }}
      </span>
      <span
        v-if="resetLabel(window)"
        class="sr-only"
      >
        · {{ resetLabel(window) }}
      </span>
    </span>
  </span>

  <div
    v-else-if="visibleWindows.length"
    class="mt-1.5 grid gap-1"
    data-testid="provider-usage-windows"
  >
    <div
      v-for="window in visibleWindows"
      :key="window.id"
      class="min-w-0"
      :title="windowTitle(window)"
    >
      <div class="mb-0.5 flex items-center justify-between gap-2 text-[10px] leading-none">
        <span class="truncate text-[#8f8f8f]">{{ window.label }}</span>
        <span
          class="shrink-0 font-medium"
          :class="statusTextClass(window.status)"
          style="font-family: var(--font-num)"
        >
          {{ remainingLabel(window) }}
        </span>
      </div>
      <div
        class="h-1 overflow-hidden rounded-full bg-[#343434]"
        role="progressbar"
        :aria-label="`${window.label}已用比例`"
        aria-valuemin="0"
        aria-valuemax="100"
        :aria-valuenow="window.usedPercent === null ? undefined : Math.round(window.usedPercent)"
      >
        <div
          class="h-full rounded-full transition-[width] duration-300"
          :class="statusBarClass(window.status)"
          :style="{ width: barWidth(window) }"
        />
      </div>
      <div
        v-if="resetLabel(window)"
        class="mt-0.5 truncate text-[10px] text-[#a3a3a3]"
      >
        {{ resetLabel(window) }}
      </div>
    </div>
    <div
      v-if="hiddenWindowCount > 0"
      class="text-[10px] text-[#a3a3a3]"
    >
      {{ hiddenWindowsLabel() }}
    </div>
  </div>
</template>
