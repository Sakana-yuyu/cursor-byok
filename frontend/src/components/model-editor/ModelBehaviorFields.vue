<script setup>
import Tooltip from "@/components/ui/Tooltip.vue";
import { resolveModelCapabilities } from "@/utils/modelContext";
import { computed, nextTick, ref } from "vue";

const props = defineProps({
  type: { type: String, default: "openai" },
  modelID: { type: String, default: "" },
  maxCompletionTokens: { type: String, default: "" },
  anthropicMaxTokens: { type: String, default: "" },
  reasoningEffort: { type: String, default: "medium" },
  anthropicThinkingEffort: { type: String, default: "" },
  customThinkingEfforts: { type: Array, default: () => [] },
  reasoningEffortOptions: { type: Array, default: () => [] },
  anthropicThinkingEffortOptions: { type: Array, default: () => [] },
  fieldTips: { type: Object, default: () => ({}) },
});

const emit = defineEmits([
  "update:maxCompletionTokens",
  "update:anthropicMaxTokens",
  "update:reasoningEffort",
  "update:anthropicThinkingEffort",
  "update:customThinkingEfforts",
]);

const usesOpenAIStyleBehavior = (type) => type === "openai" || type === "gemini";
// 自定义档位仅 OpenAI 兼容渠道支持（reasoning_effort 原样透传）。
const supportsCustomEfforts = computed(() => props.type === "openai");

const STANDARD_EFFORTS = new Set(["disabled", "low", "medium", "high", "xhigh", "max"]);
// 与后端 normalizeRuntimeThinkingEffort 的别名表对齐：这些值会被归并为标准档位
//（none→disabled 等），作为自定义档位只会造成语义二义，不从目录推荐。
const EFFORT_ALIASES = new Set([
  "disabled", "disable", "off", "none", "false", "no", "0",
  "very_high", "very-high", "veryhigh", "x-high", "extra_high", "extra-high", "extrahigh", "maximum",
  "low", "medium", "high", "xhigh", "max",
]);

const customEfforts = computed(() =>
  props.customThinkingEfforts
    .filter((value) => typeof value === "string")
    .map((value) => value.trim().toLowerCase())
    .filter(Boolean),
);

// 目录推荐：内置模型目录记录的厂商原生档位中，非标准且未添加的值（如 minimal），
// 一键点击加入自定义档位，无需手动输入。
const suggestedCustomEfforts = computed(() => {
  if (!supportsCustomEfforts.value) return [];
  const capabilities = resolveModelCapabilities(props.modelID);
  const values = Array.isArray(capabilities?.reasoningEfforts) ? capabilities.reasoningEfforts : [];
  return values
    .map((value) => String(value).trim().toLowerCase())
    .filter((value) => value && !EFFORT_ALIASES.has(value) && !customEfforts.value.includes(value))
    .slice(0, 4);
});

const addInputVisible = ref(false);
const addInputValue = ref("");
const addInputError = ref("");
const addInputRef = ref(null);

const openAddInput = async () => {
  addInputVisible.value = true;
  addInputValue.value = "";
  addInputError.value = "";
  await nextTick();
  addInputRef.value?.focus();
};

const closeAddInput = () => {
  addInputVisible.value = false;
  addInputValue.value = "";
  addInputError.value = "";
};

const isValidCustomEffort = (value) =>
  /^[a-z0-9][a-z0-9_-]{0,31}$/.test(value) && !STANDARD_EFFORTS.has(value);

const addCustomEffort = () => {
  const value = addInputValue.value.trim().toLowerCase();
  if (!supportsCustomEfforts.value || !value) {
    closeAddInput();
    return;
  }
  if (!isValidCustomEffort(value)) {
    addInputError.value = "仅支持字母/数字开头，可含 - 和 _，且不与标准档位同名";
    return;
  }
  if (customEfforts.value.includes(value)) {
    addInputError.value = "该档位已存在";
    return;
  }
  if (customEfforts.value.length >= 8) {
    addInputError.value = "自定义档位最多 8 个";
    return;
  }
  emit("update:customThinkingEfforts", [...customEfforts.value, value]);
  closeAddInput();
};

// 目录推荐档位一键加入（无需手动输入），并直接选为渠道默认档位。
const addSuggestedEffort = (value) => {
  if (!supportsCustomEfforts.value || !isValidCustomEffort(value)) return;
  if (customEfforts.value.includes(value) || customEfforts.value.length >= 8) return;
  emit("update:customThinkingEfforts", [...customEfforts.value, value]);
  emit("update:reasoningEffort", value);
};

const removeCustomEffort = (value) => {
  emit(
    "update:customThinkingEfforts",
    customEfforts.value.filter((item) => item !== value),
  );
  if (props.reasoningEffort === value) {
    emit("update:reasoningEffort", "medium");
  }
};
</script>

<template>
  <label
    v-if="usesOpenAIStyleBehavior(type)"
    class="flex flex-col gap-1"
  >
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
    >
  </label>

  <label
    v-if="type === 'anthropic'"
    class="flex flex-col gap-1"
  >
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
    >
  </label>

  <div
    v-if="usesOpenAIStyleBehavior(type)"
    class="flex flex-col gap-1.5"
  >
    <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
      <Tooltip :content="fieldTips.reasoningEffort" />
      <span>思考档位</span>
      <span class="text-xs text-[#737373]">点击选择默认档位，客户端更高选择会被限制到这里</span>
    </span>
    <div class="flex flex-wrap items-center gap-1.5">
      <button
        v-for="option in reasoningEffortOptions"
        :key="option.value"
        type="button"
        class="center-row h-8 gap-1 rounded-full border px-3 text-xs transition-colors focus:outline-none focus-visible:ring-1 focus-visible:ring-[#10AD5D]"
        :class="
          reasoningEffort === option.value
            ? 'border-[#10AD5D] bg-[#123f26] text-[#86efac]'
            : 'border-[#3f3f3f] bg-[#232323] text-[#a3a3a3] hover:border-[#4f4f4f] hover:text-[#d4d4d4]'
        "
        @click="emit('update:reasoningEffort', option.value)"
      >
        <span
          :class="option.icon"
          class="text-[14px]"
        />
        <span>{{ option.label }}</span>
      </button>

      <template v-if="supportsCustomEfforts">
        <span
          v-if="customEfforts.length"
          class="h-5 w-px bg-[#3f3f3f]"
        />
        <span
          v-for="value in customEfforts"
          :key="value"
          class="center-row h-8 gap-1 overflow-hidden rounded-full border pr-1.5 text-xs transition-colors"
          :class="
            reasoningEffort === value
              ? 'border-[#10AD5D] bg-[#123f26] text-[#86efac]'
              : 'border-[#3f3f3f] bg-[#232323] text-[#a3a3a3] hover:border-[#4f4f4f]'
          "
        >
          <button
            type="button"
            class="center-row h-full pl-3 pr-1 focus:outline-none"
            :title="`使用自定义档位 ${value}`"
            @click="emit('update:reasoningEffort', value)"
          >
            <span class="icon-[mdi--cube-outline] text-[14px]" />
            <span class="font-mono">{{ value }}</span>
          </button>
          <button
            type="button"
            class="center-row h-5 w-5 rounded-full text-[#737373] transition-colors hover:bg-white/10 hover:text-[#f87171]"
            :title="`删除自定义档位 ${value}`"
            @click="removeCustomEffort(value)"
          >
            <span class="icon-[mdi--close] text-[13px]" />
          </button>
        </span>

        <button
          v-if="!addInputVisible"
          type="button"
          class="center-row h-8 gap-1 rounded-full border border-dashed border-[#4f4f4f] px-3 text-xs text-[#8f8f8f] transition-colors hover:border-[#10AD5D] hover:text-[#86efac] focus:outline-none focus-visible:ring-1 focus-visible:ring-[#10AD5D]"
          title="添加厂商自定义思考档位（如 adaptive），出现在 Cursor 的 Effort 下拉中"
          @click="openAddInput"
        >
          <span class="icon-[mdi--plus] text-[14px]" />
          <span>自定义档位</span>
        </button>
        <span
          v-else
          class="center-row gap-1.5"
        >
          <input
            ref="addInputRef"
            v-model="addInputValue"
            type="text"
            placeholder="如 adaptive"
            class="h-8 w-36 rounded-full border border-[#3f3f3f] bg-[#232323] px-3 font-mono text-xs text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
            @keydown.enter.prevent="addCustomEffort"
            @keydown.esc.prevent="closeAddInput"
            @blur="addInputError = ''"
          >
          <button
            type="button"
            class="center-row h-8 rounded-full border border-[#10AD5D] bg-[#123f26] px-3 text-xs text-[#86efac] transition-colors hover:bg-[#175231]"
            @click="addCustomEffort"
          >
            添加
          </button>
          <button
            type="button"
            class="center-row h-8 w-8 rounded-full border border-[#3f3f3f] bg-[#232323] text-xs text-[#8f8f8f] transition-colors hover:text-[#d4d4d4]"
            @click="closeAddInput"
          >
            <span class="icon-[mdi--close] text-[14px]" />
          </button>
        </span>
      </template>
    </div>
    <div
      v-if="addInputVisible && addInputError"
      class="text-xs text-[#f87171]"
    >
      {{ addInputError }}
    </div>
    <div
      v-else-if="supportsCustomEfforts && customEfforts.length"
      class="text-xs leading-4 text-[#737373]"
    >
      自定义档位会出现在 Cursor 的 Effort 下拉，选中后原样发送给服务端（reasoning_effort）。
    </div>
    <div
      v-if="suggestedCustomEfforts.length"
      class="center-row flex-wrap justify-start gap-1.5 text-xs text-[#737373]"
    >
      <span>目录检测到该模型的原生档位，点击添加：</span>
      <button
        v-for="value in suggestedCustomEfforts"
        :key="value"
        type="button"
        class="center-row h-6 gap-1 rounded-full border border-[#3f5a46] bg-[#1a2b20] px-2.5 font-mono text-[11px] text-[#86efac] transition-colors hover:border-[#10AD5D] focus:outline-none focus-visible:ring-1 focus-visible:ring-[#10AD5D]"
        :title="`把厂商原生档位 ${value} 加入自定义档位并设为默认`"
        @click="addSuggestedEffort(value)"
      >
        <span class="icon-[mdi--plus] text-[12px]" />
        <span>{{ value }}</span>
      </button>
    </div>
  </div>

  <div
    v-if="type === 'anthropic'"
    class="flex flex-col gap-1.5"
  >
    <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
      <Tooltip :content="fieldTips.anthropicThinkingEffort" />
      <span>思考档位</span>
      <span class="text-xs text-[#737373]">点击选择默认档位，客户端更高选择会被限制到这里</span>
    </span>
    <div class="flex flex-wrap items-center gap-1.5">
      <button
        v-for="option in anthropicThinkingEffortOptions"
        :key="option.value"
        type="button"
        class="center-row h-8 gap-1 rounded-full border px-3 text-xs transition-colors focus:outline-none focus-visible:ring-1 focus-visible:ring-[#10AD5D]"
        :class="
          anthropicThinkingEffort === option.value
            ? 'border-[#10AD5D] bg-[#123f26] text-[#86efac]'
            : 'border-[#3f3f3f] bg-[#232323] text-[#a3a3a3] hover:border-[#4f4f4f] hover:text-[#d4d4d4]'
        "
        @click="emit('update:anthropicThinkingEffort', option.value)"
      >
        <span
          :class="option.icon"
          class="text-[14px]"
        />
        <span>{{ option.label }}</span>
      </button>
    </div>
  </div>
</template>
