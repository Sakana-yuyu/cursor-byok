<script setup>
import SettingsRow from "@/components/settings/SettingsRow.vue";
import SettingsSection from "@/components/settings/SettingsSection.vue";
import ModelTreeSelect from "@/components/ui/ModelTreeSelect.vue";
import Select from "@/components/ui/Select.vue";
import Switch from "@/components/ui/Switch.vue";

// 视觉委派面板：仅负责展示与上报用户意图。
// 配置真相、加载/字段状态、模型有效性校验、保存与字段级回滚全部由父组件持有。
defineProps({
  config: {
    type: Object,
    required: true,
  },
  fieldStates: {
    type: Object,
    required: true,
  },
  loadState: {
    type: Object,
    required: true,
  },
  loaded: {
    type: Boolean,
    default: false,
  },
  modelAdapters: {
    type: Array,
    default: () => [],
  },
  modeOptions: {
    type: Array,
    default: () => [],
  },
});

const emit = defineEmits(["toggle-field", "select-field", "retry-field"]);

function fieldBusy(field) {
  const state = fieldStates[field];
  return loadState.busy || Boolean(state?.busy);
}

function fieldError(field) {
  return fieldStates[field]?.error || "";
}

function fieldRetry(field) {
  emit("retry-field", field);
}
</script>

<template>
  <SettingsSection
    title="视觉委派（统一读图入口）"
    description="当主模型不支持识图时，自动把图片转发给已配置的识图模型，返回画面描述和文字（OCR），让纯文本模型也能“看图”。启用后会自动把识图模型的网关地址 / API Key / 模型名同步到读图 MCP（vision-reader）作为兜底：委派失败或不可用时，纯文本主模型仍可通过该 MCP 工具读取图片；无需在单个模型中重复配置读图 MCP。"
  >
    <SettingsRow
      label="启用视觉委派"
      description="开启后，主模型不支持图片输入时，后端会把每张图片委派给下方识图模型，并把识图结果注入回对话；识图失败时自动回退为带图片路径的占位说明，模型可调用已同步的读图 MCP（vision-reader）兜底读取。不需要为当前主模型单独配置读图 MCP。"
      :busy="fieldBusy('enabled')"
      :error="fieldError('enabled')"
    >
      <Switch
        compact
        label=""
        enabled-text="已开启"
        disabled-text="已关闭"
        :enabled="config.enabled"
        :disabled="fieldBusy('enabled') || !loaded || !config.visionModelID"
        aria-label="启用视觉委派"
        @change="(value) => emit('toggle-field', 'enabled', value)"
      />
    </SettingsRow>

    <SettingsRow
      label="识图模型"
      description="选择一个已配置且明确支持视觉输入的模型适配器。连接地址、API Key 和模型标识都来自模型配置页；未选择时视觉委派自动关闭。"
      :busy="fieldBusy('visionModelID')"
      :error="fieldError('visionModelID')"
      @retry="fieldRetry('visionModelID')"
    >
      <div class="w-[280px] max-w-full">
        <ModelTreeSelect
          :model-value="config.visionModelID"
          :adapters="modelAdapters"
          :fallback-option="{ value: '', label: '未配置（回退占位文字）' }"
          :disabled="fieldBusy('visionModelID') || !loaded"
          aria-label="识图模型"
          @change="(value) => emit('select-field', 'visionModelID', value)"
        />
      </div>
    </SettingsRow>

    <SettingsRow
      label="识图模式"
      description="识图模型返回内容的形式。描述 + OCR 最通用；仅文字抄录适合票据 / 截图。"
      :busy="fieldBusy('mode')"
      :error="fieldError('mode')"
      @retry="fieldRetry('mode')"
    >
      <div class="w-[280px] max-w-full">
        <Select
          :model-value="config.mode"
          :options="modeOptions"
          :disabled="fieldBusy('mode') || !loaded"
          aria-label="识图模式"
          @change="(value) => emit('select-field', 'mode', value)"
        />
      </div>
    </SettingsRow>
  </SettingsSection>
</template>