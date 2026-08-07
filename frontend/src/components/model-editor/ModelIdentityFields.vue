<script setup>
import Button from "@/components/ui/Button.vue";
import Select from "@/components/ui/Select.vue";
import Tooltip from "@/components/ui/Tooltip.vue";

defineProps({
  displayName: { type: String, default: "" },
  groupName: { type: String, default: "" },
  modelID: { type: String, default: "" },
  supplierModelOptions: { type: Array, default: () => [] },
  supplierPresetOptions: { type: Array, default: () => [] },
  manualAddMode: { type: Boolean, default: false },
  quickMode: { type: Boolean, default: false },
  canFetchCatalog: { type: Boolean, default: false },
  catalogError: { type: String, default: "" },
  fieldTips: { type: Object, default: () => ({}) },
});

const emit = defineEmits([
  "update:displayName",
  "update:groupName",
  "update:modelID",
  "select-preset",
  "fetch-catalog",
]);
</script>

<template>
  <label class="flex flex-col gap-1">
    <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
      <Tooltip :content="fieldTips.displayName" />
      <span>显示名称</span>
    </span>
    <input
      :value="displayName"
      type="text"
      class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
      @input="emit('update:displayName', $event.target.value)"
    />
  </label>

  <label class="flex flex-col gap-1">
    <span class="text-sm text-[#d4d4d4]">用户分组名称</span>
    <input
      :value="groupName"
      type="text"
      placeholder="可选，用于「按渠道」列表汇总"
      class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
      @input="emit('update:groupName', $event.target.value)"
    />
    <span class="text-xs text-[#8f8f8f]">留空时列表里显示为「默认分组」；与模型配置页的名称分组对应。</span>
  </label>

  <label class="flex flex-col gap-1">
    <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
      <Tooltip :content="fieldTips.modelID" />
      <span>模型标识</span>
    </span>
    <div class="flex gap-2">
      <input
        :value="modelID"
        type="text"
        placeholder="例如：gpt-4.1"
        class="h-9 min-w-0 flex-1 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
        @input="emit('update:modelID', $event.target.value)"
      />
      <Select
        v-if="supplierModelOptions.length"
        class="w-48 shrink-0"
        :model-value="modelID"
        :options="supplierModelOptions"
        @update:model-value="emit('update:modelID', $event)"
      />
      <Select
        v-if="supplierPresetOptions.length"
        class="w-36 shrink-0"
        :model-value="modelID"
        :options="supplierPresetOptions"
        @update:model-value="emit('select-preset', $event)"
      />
      <Button
        v-if="!manualAddMode && !quickMode"
        variant="default"
        :disabled="!canFetchCatalog"
        @click="emit('fetch-catalog')"
      >拉取模型</Button>
    </div>
    <div v-if="catalogError" class="mt-1 text-xs text-[#fca5a5]">{{ catalogError }}</div>
  </label>
</template>