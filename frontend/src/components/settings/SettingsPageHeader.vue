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
    <div class="flex min-w-0 items-start gap-3">
      <button
        type="button"
        aria-label="返回"
        title="返回"
        class="mt-[1px] flex h-8 w-8 shrink-0 items-center justify-center rounded-[6px] text-[#9a9a9a] transition-colors hover:bg-[#292929] hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35"
        @click="emit('back')"
      >
        <span class="icon-[mdi--arrow-left] text-[18px]" aria-hidden="true"></span>
      </button>

      <div class="min-w-0">
        <h1 class="text-[18px] font-medium text-white">{{ title }}</h1>
        <p v-if="description" class="mt-1 text-sm leading-6 text-[#8f8f8f]">{{ description }}</p>
      </div>
    </div>

    <div class="shrink-0 pt-1 text-xs font-medium" :class="statusClass">
      {{ statusLabel }}
    </div>
  </header>
</template>
