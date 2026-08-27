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
    </div>
  </header>
</template>
