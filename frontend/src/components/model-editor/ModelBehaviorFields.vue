<script setup>
import Select from "@/components/ui/Select.vue";
import Tooltip from "@/components/ui/Tooltip.vue";

defineProps({
  type: { type: String, default: "openai" },
  maxCompletionTokens: { type: String, default: "" },
  anthropicMaxTokens: { type: String, default: "" },
  reasoningEffort: { type: String, default: "medium" },
  anthropicThinkingEffort: { type: String, default: "" },
  reasoningEffortOptions: { type: Array, default: () => [] },
  anthropicThinkingEffortOptions: { type: Array, default: () => [] },
  fieldTips: { type: Object, default: () => ({}) },
});

const emit = defineEmits([
  "update:maxCompletionTokens",
  "update:anthropicMaxTokens",
  "update:reasoningEffort",
  "update:anthropicThinkingEffort",
]);

const usesOpenAIStyleBehavior = (type) => type === "openai" || type === "gemini";
</script>

<template>
  <label v-if="usesOpenAIStyleBehavior(type)" class="flex flex-col gap-1">
    <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
      <Tooltip :content="fieldTips.maxCompletionTokens" />
      <span>最大输出 Token</span>
    </span>
    <input
      :value="maxCompletionTokens"
      type="text"
      inputmode="numeric"
      placeholder="例如：65536（留空用默认值）"
      class="h-9 min-w-0 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
      @input="emit('update:maxCompletionTokens', $event.target.value)"
    />
  </label>

  <label v-if="type === 'anthropic'" class="flex flex-col gap-1">
    <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
      <Tooltip :content="fieldTips.anthropicMaxTokens" />
      <span>最大输出 Token</span>
    </span>
    <input
      :value="anthropicMaxTokens"
      type="text"
      inputmode="numeric"
      placeholder="例如：65536（留空用默认值）"
      class="h-9 min-w-0 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
      @input="emit('update:anthropicMaxTokens', $event.target.value)"
    />
  </label>

  <label v-if="usesOpenAIStyleBehavior(type)" class="flex flex-col gap-1">
    <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
      <Tooltip :content="fieldTips.reasoningEffort" />
      <span>推理强度上限</span>
    </span>
    <Select
      :model-value="reasoningEffort"
      :options="reasoningEffortOptions"
      @update:model-value="emit('update:reasoningEffort', $event)"
    />
  </label>

  <label v-if="type === 'anthropic'" class="flex flex-col gap-1">
    <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
      <Tooltip :content="fieldTips.anthropicThinkingEffort" />
      <span>思考强度上限</span>
    </span>
    <Select
      :model-value="anthropicThinkingEffort"
      :options="anthropicThinkingEffortOptions"
      @update:model-value="emit('update:anthropicThinkingEffort', $event)"
    />
  </label>
</template>
