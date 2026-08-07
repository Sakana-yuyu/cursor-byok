<script setup>
import Input from "@/components/ui/Input.vue";
import SettingsRow from "@/components/settings/SettingsRow.vue";
import SettingsSection from "@/components/settings/SettingsSection.vue";
import Switch from "@/components/ui/Switch.vue";

// 全局委派控制面板只展示当前状态并上报用户操作。
// 启用/并发草稿、延迟保存、串行持久化、失败恢复和重试均由父组件持有。
defineProps({
  enabled: { type: Boolean, default: false },
  enabledState: { type: Object, required: true },
  maxConcurrencyDraft: { type: String, default: "" },
  maxConcurrencyState: { type: Object, required: true },
  configReady: { type: Boolean, default: false },
});

const emit = defineEmits([
  "change:enabled",
  "retry:enabled",
  "update:max-concurrency",
  "flush:max-concurrency",
  "retry:max-concurrency",
]);
</script>

<template>
  <SettingsSection
    title="委派配置"
    description="全局开关和并发限制会立即写回本地配置，页面顶部会显示统一的保存状态。"
  >
    <SettingsRow
      label="启用 Multitask 委派"
      description="使用已配置模型并行处理子任务，失败的子任务不会阻塞其他任务。"
      :busy="enabledState.busy"
      :error="enabledState.error"
      @retry="emit('retry:enabled')"
    >
      <Switch
        compact
        label=""
        enabled-text="已开启"
        disabled-text="已关闭"
        :enabled="enabled"
        :disabled="enabledState.busy || !configReady"
        aria-label="启用 Multitask 委派"
        @change="emit('change:enabled', $event)"
      />
    </SettingsRow>

    <SettingsRow
      label="最大并发数"
      description="限制同一时刻可运行的委派任务数量。输入后 500ms 自动保存，失焦或回车会立即提交。"
      :busy="maxConcurrencyState.busy || maxConcurrencyState.queued"
      :error="maxConcurrencyState.error"
      @retry="emit('retry:max-concurrency')"
    >
      <div class="w-[220px] max-w-full">
        <Input
          :model-value="maxConcurrencyDraft"
          type="number"
          min="1"
          :disabled="maxConcurrencyState.busy || !configReady"
          aria-label="最大并发数"
          @update:model-value="emit('update:max-concurrency', $event)"
          @blur="emit('flush:max-concurrency')"
          @keydown.enter.prevent="emit('flush:max-concurrency')"
        />
      </div>
    </SettingsRow>
  </SettingsSection>
</template>