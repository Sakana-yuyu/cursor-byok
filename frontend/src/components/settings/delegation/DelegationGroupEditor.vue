<script setup>
import Input from "@/components/ui/Input.vue";
import ModelTreeSelect from "@/components/ui/ModelTreeSelect.vue";
import Select from "@/components/ui/Select.vue";
import Switch from "@/components/ui/Switch.vue";
import DelegationIconButton from "@/components/settings/delegation/DelegationIconButton.vue";
import { computed } from "vue";

const props = defineProps({
  group: {
    type: Object,
    required: true,
  },
  groupIndex: {
    type: Number,
    required: true,
  },
  nameDraft: {
    type: String,
    default: "",
  },
  totalGroups: {
    type: Number,
    required: true,
  },
  modelAdapters: {
    type: Array,
    default: () => [],
  },
  modeOptions: {
    type: Array,
    default: () => [],
  },
  permissionGroups: {
    type: Array,
    default: () => [],
  },
  busy: {
    type: Boolean,
    default: false,
  },
  queued: {
    type: Boolean,
    default: false,
  },
  error: {
    type: String,
    default: "",
  },
  expanded: {
    type: Boolean,
    default: false,
  },
});

const emit = defineEmits([
  "change:default-model",
  "change:execution-mode",
  "delete",
  "flush:name",
  "move:down",
  "move:up",
  "retry",
  "toggle:enabled",
  "toggle:expanded",
  "toggle:model",
  "toggle:permission",
  "update:name",
]);

const defaultModelAdapters = computed(() => {
  const selectedIDs = new Set(props.group.modelIDs || []);
  return props.modelAdapters.filter((adapter) => selectedIDs.has(adapter.id));
});

const defaultModelOptions = computed(() => {
  return defaultModelAdapters.value.map((adapter) => ({
    value: adapter.id,
    label: adapter.displayName || adapter.modelID,
  }));
});

const groupDisplayName = computed(() => {
  return props.nameDraft || props.group.name || `模型组 ${props.groupIndex + 1}`;
});

const selectedModelNames = computed(() => {
  const selectedIDs = new Set(props.group.modelIDs || []);
  return props.modelAdapters
    .filter((adapter) => selectedIDs.has(adapter.id))
    .map((adapter) => adapter.displayName || adapter.modelID)
    .filter(Boolean);
});

const defaultModelLabel = computed(() => {
  return defaultModelOptions.value.find((option) => option.value === props.group.defaultModelID)?.label || "未设置";
});

const executionModeLabel = computed(() => {
  return props.modeOptions.find((option) => option.value === props.group.executionMode)?.label || "自动选择";
});

const saveStatusLabel = computed(() => {
  if (props.busy) {
    return "正在保存";
  }
  if (props.queued) {
    return "等待保存";
  }
  if (props.error) {
    return "保存失败";
  }
  return "";
});

function permissionEnabled(permission) {
  return permission.tools.every((tool) => props.group.toolPermissions?.[tool] !== false);
}

function handleModelToggle(modelID, event) {
  emit("toggle:model", {
    modelID,
    enabled: Boolean(event?.target?.checked),
  });
}

function handlePermissionToggle(permission, enabled) {
  emit("toggle:permission", {
    permission,
    enabled: Boolean(enabled),
  });
}
</script>

<template>
  <article class="rounded-[8px] border border-white/10 bg-black/15 p-4">
    <div class="flex flex-wrap items-center gap-3">
      <DelegationIconButton
        :icon="expanded ? 'icon-[mdi--chevron-up]' : 'icon-[mdi--chevron-down]'"
        :label="expanded ? `收起 ${groupDisplayName}` : `展开 ${groupDisplayName}`"
        :aria-expanded="expanded"
        @click="emit('toggle:expanded')"
      />

      <div class="min-w-0 flex-1">
        <div class="flex min-w-0 items-center gap-2">
          <span class="truncate text-sm font-medium text-white">{{ groupDisplayName }}</span>
          <span
            class="shrink-0 rounded-[4px] px-1.5 py-0.5 text-[10px]"
            :class="group.enabled ? 'bg-[#10AD5D]/15 text-[#42d487]' : 'bg-white/8 text-[#8f8f8f]'"
          >
            {{ group.enabled ? "已启用" : "已停用" }}
          </span>
        </div>
        <div class="mt-1 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-[#8f8f8f]">
          <span>{{ selectedModelNames.length }} 个可用模型</span>
          <span>默认：{{ defaultModelLabel }}</span>
          <span>{{ executionModeLabel }}</span>
          <span v-if="saveStatusLabel" :class="error ? 'text-[#f2a7a7]' : 'text-[#8f8f8f]'">{{ saveStatusLabel }}</span>
        </div>
      </div>

      <div class="flex items-center gap-2">
        <DelegationIconButton
          icon="icon-[mdi--arrow-up]"
          :label="`上移 ${groupDisplayName}`"
          :disabled="busy || groupIndex === 0"
          @click="emit('move:up')"
        />
        <DelegationIconButton
          icon="icon-[mdi--arrow-down]"
          :label="`下移 ${groupDisplayName}`"
          :disabled="busy || groupIndex >= totalGroups - 1"
          @click="emit('move:down')"
        />
        <DelegationIconButton
          icon="icon-[mdi--trash-can-outline]"
          :label="`删除 ${groupDisplayName}`"
          :disabled="busy"
          danger
          @click="emit('delete')"
        />
      </div>

      <div class="w-full sm:w-[220px]">
        <Switch
          compact
          label="启用该模型组"
          enabled-text="已参与委派"
          disabled-text="已停用"
          :enabled="group.enabled"
          :disabled="busy"
          @change="(value) => emit('toggle:enabled', value)"
        />
      </div>
    </div>

    <div v-if="expanded" class="mt-4 space-y-4 border-t border-white/10 pt-4">
      <Input
        :model-value="nameDraft"
        :disabled="busy"
        :aria-label="`模型组 ${groupIndex + 1} 名称`"
        placeholder="输入模型组名称"
        @update:model-value="(value) => emit('update:name', value)"
        @blur="emit('flush:name')"
        @keydown.enter.prevent="emit('flush:name')"
      />

      <div class="grid gap-3 lg:grid-cols-2">
        <div class="space-y-2">
          <div class="text-[11px] font-medium text-[#8f8f8f]">执行模式</div>
          <Select
            :model-value="group.executionMode"
            :options="modeOptions"
            aria-label="委派执行模式"
            :disabled="busy"
            @change="(value) => emit('change:execution-mode', value)"
          />
        </div>

        <div class="space-y-2">
          <div class="text-[11px] font-medium text-[#8f8f8f]">默认模型</div>
          <ModelTreeSelect
            :model-value="group.defaultModelID"
            :adapters="defaultModelAdapters"
            :disabled="busy || !defaultModelOptions.length"
            placeholder="请选择默认模型"
            aria-label="默认委派模型"
            @change="(value) => emit('change:default-model', value)"
          />
        </div>
      </div>

      <div class="space-y-3 border-t border-white/10 pt-3">
        <div class="text-[11px] font-medium text-[#8f8f8f]">可用模型</div>
        <div
          v-if="!modelAdapters.length"
          class="rounded-[6px] border border-dashed border-[#444] px-3 py-4 text-xs text-[#858585]"
        >
          暂无可用模型，请先在模型配置中添加模型适配器。
        </div>
        <div v-else class="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
          <label
            v-for="adapter in modelAdapters"
            :key="adapter.id"
            class="flex min-w-0 items-center gap-2 rounded-[6px] border border-white/6 bg-black/10 px-2.5 py-2 text-xs text-[#d4d4d4]"
          >
            <input
              type="checkbox"
              :checked="group.modelIDs.includes(adapter.id)"
              :disabled="busy"
              @change="handleModelToggle(adapter.id, $event)"
            />
            <span class="min-w-0 truncate" :title="adapter.displayName || adapter.modelID">
              {{ adapter.displayName || adapter.modelID }}
            </span>
          </label>
        </div>
      </div>

      <div class="space-y-3 border-t border-white/10 pt-3">
        <div class="text-[11px] font-medium text-[#8f8f8f]">工具权限</div>
        <div class="grid gap-2 lg:grid-cols-2">
          <Switch
            v-for="permission in permissionGroups"
            :key="permission.key"
            compact
            :label="permission.label"
            :description="permission.description"
            :enabled="permissionEnabled(permission)"
            :disabled="busy"
            @change="(value) => handlePermissionToggle(permission, value)"
          />
        </div>
      </div>

      <div
        v-if="error"
        class="flex min-w-0 items-center gap-3 rounded-[6px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-xs text-[#f2a7a7]"
      >
        <span class="min-w-0 flex-1 break-words">{{ error }}</span>
        <button
          type="button"
          class="shrink-0 text-[#10AD5D] transition-colors hover:text-[#33c476]"
          @click="emit('retry')"
        >
          重试
        </button>
      </div>
    </div>
  </article>
</template>
