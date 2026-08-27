<script setup>
import ModelAdapterTestCard from "@/components/ModelAdapterTestCard.vue";
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import { providerIcon } from "@/utils/providerMeta";
import {
  formatHost,
  formatOpenAIRequestGroup,
  maskSecret,
  resolvedOpenAIEndpoint,
  resolvedOpenAIRequestGroup,
} from "@/utils/supplierDetail";
import { SUPPLIER_MODEL_SOURCE_CURSOR_ACCOUNT } from "@/utils/supplierGrouping";

// 供应商模型卡片：仅负责单模型展示与上报用户意图。
// 健康状态、测试结果、选中状态、保存/测试忙碌由父页面派生传入，
// 测试、编辑、复制、删除、多选等操作全部以事件上送，父页面执行。
defineProps({
  adapter: {
    type: Object,
    required: true,
  },
  health: {
    type: String,
    default: "untested",
  },
  result: {
    type: Object,
    default: null,
  },
  testing: {
    type: Boolean,
    default: false,
  },
  saving: {
    type: Boolean,
    default: false,
  },
  selectionMode: {
    type: Boolean,
    default: false,
  },
  selected: {
    type: Boolean,
    default: false,
  },
});

const emit = defineEmits(["toggle-select", "test", "edit", "duplicate", "delete", "enable"]);
const isCursorAccountModel = (adapter) => adapter?.source === SUPPLIER_MODEL_SOURCE_CURSOR_ACCOUNT;
</script>

<template>
  <Card :class="selectionMode && selected ? 'border-[#10AD5D]/50' : ''" data-testid="model-card">
    <div class="flex h-full min-h-[154px] flex-col justify-between gap-3">
      <div class="flex flex-col gap-2.5">
        <div class="flex items-start justify-between gap-3">
          <label v-if="selectionMode" class="mt-1 shrink-0 cursor-pointer">
            <input
              type="checkbox"
              class="size-4 accent-[#10AD5D]"
              :checked="selected"
              @change="emit('toggle-select')"
            />
          </label>
          <div class="min-w-0 flex-1">
            <div class="center-row gap-1.5">
              <span class="truncate text-base font-medium text-white">{{ adapter.displayName }}</span>
              <span
                v-if="health === 'ok'"
                class="shrink-0 rounded-full bg-[#10AD5D]/15 px-1.5 py-0.5 text-[10px] text-[#6ee7a5]"
              >可用</span>
              <span
                v-else-if="health === 'fail'"
                class="shrink-0 rounded-full bg-[#f87171]/15 px-1.5 py-0.5 text-[10px] text-[#fca5a5]"
              >失败</span>
              <span
                v-if="adapter.disabled"
                title="该模型不会进入 Cursor 模型列表"
                class="shrink-0 rounded-full bg-[#f59e0b]/15 px-1.5 py-0.5 text-[10px] text-[#fcd34d]"
              >已停用</span>
            </div>
            <div class="mt-1 truncate text-sm text-[#8f8f8f]">{{ adapter.modelID }}</div>
            <div v-if="adapter.type === 'openai'" class="mt-1 flex flex-wrap gap-1.5 text-xs text-[#737373]">
              <span class="rounded-full border border-[#3a3a3a] bg-[#232323] px-2 py-0.5">
                {{ resolvedOpenAIEndpoint(adapter) }}
              </span>
              <span class="rounded-full border border-[#24553c] bg-[#173524] px-2 py-0.5 text-[#86efac]">
                {{ formatOpenAIRequestGroup(resolvedOpenAIRequestGroup(adapter), resolvedOpenAIEndpoint(adapter)) }}
              </span>
            </div>
          </div>
          <span :class="[providerIcon(adapter.type), 'text-[20px] shrink-0']"></span>
        </div>
        <div class="grid grid-cols-2 gap-2 text-sm text-[#a3a3a3]">
          <div class="rounded-[8px] bg-[#232323] px-3 py-2">
            <div class="text-[12px] uppercase tracking-[0.08em] text-[#666]">Host</div>
            <div class="mt-1 truncate text-[#d4d4d4]">{{ isCursorAccountModel(adapter) ? '账户通道' : formatHost(adapter.baseURL) }}</div>
          </div>
          <div class="rounded-[8px] bg-[#232323] px-3 py-2">
            <div class="text-[12px] uppercase tracking-[0.08em] text-[#666]">{{ isCursorAccountModel(adapter) ? '执行状态' : 'API Key' }}</div>
            <div class="mt-1 truncate text-[#d4d4d4]">{{ isCursorAccountModel(adapter) ? '待真实协议验证' : maskSecret(adapter.apiKey) }}</div>
          </div>
        </div>
        <ModelAdapterTestCard
          compact
          :title="isCursorAccountModel(adapter) ? '账户模型状态' : '测试'"
          :empty-text="isCursorAccountModel(adapter) ? '执行通道待真实协议验证；保存配置不会发起模型调用。' : '未测试'"
          :result="result"
        />
      </div>
      <div class="center-row flex-wrap justify-end gap-2 border-t border-[#343434] pt-3">
        <Button variant="default" :disabled="saving || testing || isCursorAccountModel(adapter)" @click="emit('test')">
          {{ isCursorAccountModel(adapter) ? "账户通道待验证" : (testing ? "测试中..." : "测试") }}
        </Button>
        <Button
          v-if="adapter.disabled"
          variant="default"
          :disabled="saving"
          @click="emit('enable')"
        >启用</Button>
        <Button variant="default" :disabled="saving" @click="emit('edit')">编辑</Button>
        <Button variant="default" :disabled="saving" @click="emit('duplicate')">复制</Button>
        <Button variant="text" :disabled="saving" @click="emit('delete')">删除</Button>
      </div>
    </div>
  </Card>
</template>
