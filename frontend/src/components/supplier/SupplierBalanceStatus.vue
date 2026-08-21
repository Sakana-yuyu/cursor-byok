<script setup>
import ProviderUsageWindows from "@/components/provider/ProviderUsageWindows.vue";

// 余额状态仅负责根据父页面提供的派生值渲染，并把刷新意图上送。
// 请求、缓存、旧值保留、告警阈值与服务端错误处理均由父页面保持。
defineProps({
  loading: { type: Boolean, default: false },
  loaded: { type: Boolean, default: false },
  data: { type: Object, default: null },
  stale: { type: Boolean, default: false },
  alert: { type: Object, default: null },
  primaryText: { type: String, default: "" },
  secondaryText: { type: String, default: "" },
  canRefresh: { type: Boolean, default: false },
});

const emit = defineEmits(["refresh"]);
</script>

<template>
  <span v-if="loading" class="center-row gap-1 text-[#8f8f8f]">
    <span class="icon-[mdi--loading] animate-spin text-[13px]"></span>查询余额…
  </span>
  <template v-else-if="data && data.supported">
    <span
      class="center-row gap-1 rounded-full px-2 py-0.5"
      :class="alert ? 'bg-[#f87171]/15 text-[#fca5a5]' : (stale ? 'bg-[#a3a3a3]/15 text-[#c9c9c9]' : 'bg-[#10AD5D]/15 text-[#6ee7a5]')"
      :title="data.unlimited ? '该账户额度不限' : (alert ? alert.text : secondaryText)"
    ><span v-if="alert" class="text-[11px]">⚠</span>{{ primaryText }}<span v-if="stale" class="text-[11px] text-[#8f8f8f]">（可能过期）</span></span>
    <span v-if="!data.unlimited && secondaryText" class="hidden text-[#666] sm:inline">{{ secondaryText }}</span>
    <ProviderUsageWindows
      v-if="!data.unlimited && data.windows?.length"
      :windows="data.windows"
      variant="inline"
    />
  </template>
  <span
    v-else-if="loaded"
    class="text-[#737373]"
    :title="(data && data.message) || '余额不可用'"
  >{{ data?.source === 'none' || data?.message === '暂无自动查询' ? '暂无自动查询' : '余额不可用' }}</span>
  <button
    v-if="canRefresh && !loading"
    type="button"
    class="center-row text-[#8f8f8f] transition-colors hover:text-white"
    title="刷新余额"
    @click="emit('refresh')"
  >
    <span class="icon-[mdi--refresh] text-[13px]"></span>
  </button>
</template>