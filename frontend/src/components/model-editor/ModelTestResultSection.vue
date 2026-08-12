<script setup>
import ModelAdapterTestCard from "@/components/ModelAdapterTestCard.vue";

// 模型测试结果区：仅负责测试结果展示与错误提示。
// 测试结果、过期判定、保存后测试流程与错误来源全部保留在父页面。
defineProps({
  result: {
    type: Object,
    default: null,
  },
  stale: {
    type: Boolean,
    default: false,
  },
  error: {
    type: String,
    default: "",
  },
  title: {
    type: String,
    default: "模型测试",
  },
  emptyText: {
    type: String,
    default: "尚未测试",
  },
});
</script>

<template>
  <ModelAdapterTestCard
    :result="result"
    :stale="stale"
    :show-metrics="true"
    :title="title"
    :empty-text="emptyText"
  />

  <div
    v-if="error"
    class="rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-sm text-[#fca5a5]"
  >
    {{ error }}
  </div>
</template>
