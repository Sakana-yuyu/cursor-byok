<script setup>
import { computed } from "vue";

const props = defineProps({
  title: { type: String, default: "" },
  description: { type: String, default: "" },
  status: {
    type: String,
    default: "saved",
    validator: (value) => ["saved", "saving", "error"].includes(value),
  },
});

const emit = defineEmits(["back"]);

const statusLabel = computed(() => {
  if (props.status === "saving") {
    return "正在保存";
  }
  if (props.status === "error") {
    return "保存失败";
  }
  return "已保存";
});

const statusClass = computed(() => {
  if (props.status === "saving") {
    return "text-[#d5d5d5]";
  }
  if (props.status === "error") {
    return "text-[#f2a7a7]";
  }
  return "text-[#8ddcb3]";
});
</script>

<template>
  <header class="flex min-w-0 items-start justify-between gap-4 border-b border-[#343434] pb-5">
    <div class="min-w-0 flex-1">
      <h1 class="text-[18px] font-medium text-white">{{ title }}</h1>
      <p v-if="description" class="mt-1 text-sm leading-6 text-[#8f8f8f]">{{ description }}</p>
    </div>

    <div class="flex shrink-0 items-start gap-2">
      <div class="pt-1 text-xs font-medium" :class="statusClass">
        {{ statusLabel }}
      </div>

      <button
        type="button"
        aria-label="返回"
        title="返回"
        class="flex h-8 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-[6px] border border-white/10 bg-black/15 px-2 text-sm font-medium text-[#9a9a9a] shadow-[0_1px_2px_rgba(0,0,0,0.2)] transition-colors hover:border-white/15 hover:bg-[#292929] hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35"
        @click="emit('back')"
      >
        <span class="icon-[mdi--keyboard-return] text-[16px]" aria-hidden="true"></span>
        <span>返回</span>
      </button>
    </div>
  </header>
</template>
