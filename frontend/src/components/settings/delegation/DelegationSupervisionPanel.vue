<script setup>
import SettingsRow from "@/components/settings/SettingsRow.vue";
import SettingsSection from "@/components/settings/SettingsSection.vue";
import Input from "@/components/ui/Input.vue";
import ModelTreeSelect from "@/components/ui/ModelTreeSelect.vue";
import Select from "@/components/ui/Select.vue";
import Switch from "@/components/ui/Switch.vue";

// 监督策略面板：仅负责展示与上报用户意图。
// 配置真相、字段级忙碌/错误/重试状态、autosave 调度与串行保存队列全部由父组件持有。
const props = defineProps({
  config: {
    type: Object,
    required: true,
  },
  numberDrafts: {
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
  saveState: {
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
  workerGroupOptions: {
    type: Array,
    default: () => [],
  },
});

const emit = defineEmits([
  "toggle-field",
  "select-field",
  "update-limit-draft",
  "queue-limit",
  "flush-limit",
  "retry-field",
  "retry",
]);

function fieldBusy(field) {
  const state = props.fieldStates[field];
  return props.loadState.busy || Boolean(state?.busy || state?.queued);
}

function fieldError(field) {
  return props.fieldStates[field]?.error || "";
}

function fieldRetry(field) {
  emit("retry-field", field);
}

function hasFieldError() {
  return Object.values(props.fieldStates).some((state) => state?.error);
}

function hasSaveError() {
  return Boolean(props.saveState.error) || hasFieldError();
}
</script>

<template>
  <SettingsSection
    title="监督策略"
    description="由更强的模型负责规划、检查和纠偏，委派模型负责执行。仅对 Multitask 生效，关闭后保持原有委派流程。"
  >
    <SettingsRow
      label="启用监督委派"
      description="监督模型会检查子任务进度，在发现循环、偏离范围或缺少证据时进行纠偏。"
      :busy="fieldBusy('enabled')"
      :error="loadState.error || fieldError('enabled')"
      @retry="fieldRetry('enabled')"
    >
      <Switch
        compact
        label=""
        enabled-text="已开启"
        disabled-text="已关闭"
        :enabled="config.enabled"
        :disabled="fieldBusy('enabled') || !loaded"
        aria-label="启用监督委派"
        @change="(value) => emit('toggle-field', 'enabled', value)"
      />
    </SettingsRow>

    <SettingsRow
      label="监督模型"
      description="默认跟随主模型；也可以指定一个更强的已配置模型作为顾问。"
      :busy="fieldBusy('supervisorModelID')"
      :error="fieldError('supervisorModelID')"
      @retry="fieldRetry('supervisorModelID')"
    >
      <div class="w-[280px] max-w-full">
        <ModelTreeSelect
          :model-value="config.supervisorModelID"
          :adapters="modelAdapters"
          :fallback-option="{ value: '', label: '跟随主模型' }"
          :disabled="fieldBusy('supervisorModelID') || !config.enabled"
          aria-label="监督模型"
          @change="(value) => emit('select-field', 'supervisorModelID', value)"
        />
      </div>
    </SettingsRow>

    <SettingsRow
      label="复核模型"
      description="监督模型完成初审后使用的复核模型，默认跟随监督模型。"
      :busy="fieldBusy('reviewerModelID')"
      :error="fieldError('reviewerModelID')"
      @retry="fieldRetry('reviewerModelID')"
    >
      <div class="w-[280px] max-w-full">
        <ModelTreeSelect
          :model-value="config.reviewerModelID"
          :adapters="modelAdapters"
          :fallback-option="{ value: '', label: '跟随 Supervisor' }"
          :disabled="fieldBusy('reviewerModelID') || !config.enabled"
          aria-label="复核模型"
          @change="(value) => emit('select-field', 'reviewerModelID', value)"
        />
      </div>
    </SettingsRow>

    <SettingsRow
      label="执行模型组"
      description="指定监督模式优先使用的执行组；留空时按现有委派组选择逻辑运行。"
      :busy="fieldBusy('workerGroupID')"
      :error="fieldError('workerGroupID')"
      @retry="fieldRetry('workerGroupID')"
    >
      <div class="w-[280px] max-w-full">
        <Select
          :model-value="config.workerGroupID"
          :options="workerGroupOptions"
          :disabled="fieldBusy('workerGroupID') || !config.enabled"
          aria-label="监督执行模型组"
          @change="(value) => emit('select-field', 'workerGroupID', value)"
        />
      </div>
    </SettingsRow>

    <SettingsRow
      label="监督上限"
      description="限制单个子任务可纠偏、重试和循环监督的次数，防止异常任务长期占用资源。"
    >
      <div class="grid w-full max-w-[520px] grid-cols-1 gap-3 lg:grid-cols-3">
        <div class="min-w-0 space-y-1">
          <label class="block space-y-1 text-xs text-[#8f8f8f]">
            <span>最大纠偏</span>
            <Input
              :model-value="numberDrafts.maxCorrections"
              type="number"
              min="1"
              :disabled="fieldBusy('maxCorrections') || !config.enabled"
              aria-label="最大纠偏次数"
              @update:model-value="(value) => emit('update-limit-draft', 'maxCorrections', value)"
              @blur="emit('queue-limit', 'maxCorrections'); emit('flush-limit', 'maxCorrections')"
            />
          </label>
          <button
            v-if="fieldError('maxCorrections')"
            type="button"
            class="text-left text-xs leading-5 text-[#f2a7a7]"
            @click="fieldRetry('maxCorrections')"
          >
            {{ fieldError('maxCorrections') }} · 重试
          </button>
        </div>
        <div class="min-w-0 space-y-1">
          <label class="block space-y-1 text-xs text-[#8f8f8f]">
            <span>最大重试</span>
            <Input
              :model-value="numberDrafts.maxRetries"
              type="number"
              min="1"
              :disabled="fieldBusy('maxRetries') || !config.enabled"
              aria-label="最大重试次数"
              @update:model-value="(value) => emit('update-limit-draft', 'maxRetries', value)"
              @blur="emit('queue-limit', 'maxRetries'); emit('flush-limit', 'maxRetries')"
            />
          </label>
          <button
            v-if="fieldError('maxRetries')"
            type="button"
            class="text-left text-xs leading-5 text-[#f2a7a7]"
            @click="fieldRetry('maxRetries')"
          >
            {{ fieldError('maxRetries') }} · 重试
          </button>
        </div>
        <div class="min-w-0 space-y-1">
          <label class="block space-y-1 text-xs text-[#8f8f8f]">
            <span>最大监督轮次</span>
            <Input
              :model-value="numberDrafts.maxRounds"
              type="number"
              min="1"
              :disabled="fieldBusy('maxRounds') || !config.enabled"
              aria-label="最大监督轮次"
              @update:model-value="(value) => emit('update-limit-draft', 'maxRounds', value)"
              @blur="emit('queue-limit', 'maxRounds'); emit('flush-limit', 'maxRounds')"
            />
          </label>
          <button
            v-if="fieldError('maxRounds')"
            type="button"
            class="text-left text-xs leading-5 text-[#f2a7a7]"
            @click="fieldRetry('maxRounds')"
          >
            {{ fieldError('maxRounds') }} · 重试
          </button>
        </div>
      </div>
    </SettingsRow>

    <SettingsRow
      label="监督处置"
      description="允许监督模型在执行偏离时改派模型、升级复核，或在监督服务不可用时阻止任务继续。"
    >
      <div class="grid w-full max-w-[560px] grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
        <div class="min-h-[68px] min-w-0 rounded-[6px] border border-white/8 bg-black/10 px-3 py-2.5">
          <Switch
            compact
            class="w-full"
            label="允许改派"
            :enabled="config.allowReassign"
            :disabled="fieldBusy('allowReassign') || !config.enabled"
            aria-label="允许监督模型改派任务"
            @change="(value) => emit('toggle-field', 'allowReassign', value)"
          />
          <button
            v-if="fieldError('allowReassign')"
            type="button"
            class="text-left text-xs leading-5 text-[#f2a7a7]"
            @click="fieldRetry('allowReassign')"
          >
            {{ fieldError('allowReassign') }} · 重试
          </button>
        </div>
        <div class="min-h-[68px] min-w-0 rounded-[6px] border border-white/8 bg-black/10 px-3 py-2.5">
          <Switch
            compact
            class="w-full"
            label="允许升级"
            :enabled="config.allowEscalate"
            :disabled="fieldBusy('allowEscalate') || !config.enabled"
            aria-label="允许监督模型升级复核"
            @change="(value) => emit('toggle-field', 'allowEscalate', value)"
          />
          <button
            v-if="fieldError('allowEscalate')"
            type="button"
            class="text-left text-xs leading-5 text-[#f2a7a7]"
            @click="fieldRetry('allowEscalate')"
          >
            {{ fieldError('allowEscalate') }} · 重试
          </button>
        </div>
        <div class="min-h-[68px] min-w-0 rounded-[6px] border border-white/8 bg-black/10 px-3 py-2.5">
          <Switch
            compact
            class="w-full"
            label="严格不可用处理"
            :enabled="config.strictUnavailable"
            :disabled="fieldBusy('strictUnavailable') || !config.enabled"
            aria-label="监督模型不可用时停止任务"
            @change="(value) => emit('toggle-field', 'strictUnavailable', value)"
          />
          <button
            v-if="fieldError('strictUnavailable')"
            type="button"
            class="text-left text-xs leading-5 text-[#f2a7a7]"
            @click="fieldRetry('strictUnavailable')"
          >
            {{ fieldError('strictUnavailable') }} · 重试
          </button>
        </div>
      </div>
    </SettingsRow>

    <div v-if="saveState.success && !hasSaveError()" class="mt-3 text-xs text-[#10AD5D]">
      监督策略已保存
    </div>
  </SettingsSection>
</template>