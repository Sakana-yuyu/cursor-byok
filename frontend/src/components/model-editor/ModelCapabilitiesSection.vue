<script setup>
import Tooltip from "@/components/ui/Tooltip.vue";

// 模型能力与上下文档位区：仅负责展示能力徽章、快捷档位与上下文输入。
// draft、能力/档位派生与持久化保留在父页面，子组件以事件上报用户意图。
defineProps({
  capabilities: {
    type: Object,
    default: null,
  },
  // covered=false 表示模型未命中内置能力目录（能力未知）。与 null capabilities
  // 的区别：capabilities 为 null 时可能是「还没输入模型 ID」，covered 是父级
  // 基于「已输入但未命中」的显式判定，避免空输入时误报。
  covered: {
    type: Boolean,
    default: true,
  },
  detectedContextWindow: {
    type: Object,
    default: null,
  },
  activeTier: {
    type: Object,
    default: null,
  },
  recommendedTier: {
    type: Object,
    default: null,
  },
  contextTiers: {
    type: Array,
    default: () => [],
  },
  fieldTips: {
    type: Object,
    default: () => ({}),
  },
  modelValue: {
    type: String,
    default: "",
  },
});

const emit = defineEmits(["update:modelValue", "select-tier", "open-vision-settings"]);
</script>

<template>
  <label class="flex flex-col gap-1 md:col-span-2">
    <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
      <Tooltip :content="fieldTips.contextWindowTokens" />
      <span>上下文窗口</span>
    </span>

    <!-- 模型能力徽章 -->
    <div v-if="capabilities" class="flex flex-wrap items-center gap-1.5">
      <span v-if="capabilities.supportsVision" class="inline-flex items-center gap-0.5 rounded-full border border-[#3f3f3f] bg-[#2a2a2a] px-2 py-0.5 text-[11px] text-[#86efac]">
        <span class="icon-[mdi--eye-outline] text-[12px]"></span>视觉
      </span>
      <span v-if="capabilities.supportsThinking" class="inline-flex items-center gap-0.5 rounded-full border border-[#3f3f3f] bg-[#2a2a2a] px-2 py-0.5 text-[11px] text-[#93c5fd]">
        <span class="icon-[mdi--brain] text-[12px]"></span>思考
      </span>
      <span v-if="capabilities.supportsTools" class="inline-flex items-center gap-0.5 rounded-full border border-[#3f3f3f] bg-[#2a2a2a] px-2 py-0.5 text-[11px] text-[#fcd34d]">
        <span class="icon-[mdi--tools] text-[12px]"></span>工具
      </span>
      <span v-if="capabilities.supportsAudio" class="inline-flex items-center gap-0.5 rounded-full border border-[#3f3f3f] bg-[#2a2a2a] px-2 py-0.5 text-[11px] text-[#c4b5fd]">
        <span class="icon-[mdi--microphone-outline] text-[12px]"></span>音频
      </span>
      <span class="inline-flex items-center gap-0.5 rounded-full border border-[#3f3f3f] bg-[#2a2a2a] px-2 py-0.5 text-[11px] text-[#a3a3a3]">
        上下文 {{ (capabilities.contextWindowTokens / 1000).toFixed(0) }}K
      </span>
      <span class="inline-flex items-center gap-0.5 rounded-full border border-[#3f3f3f] bg-[#2a2a2a] px-2 py-0.5 text-[11px] text-[#a3a3a3]">
        最大输出 {{ (capabilities.maxOutputTokens / 1000).toFixed(0) }}K
      </span>
    </div>

    <!-- 未覆盖模型提示：能力未知，保守运行 + 引导补填 -->
    <div v-if="!covered" class="rounded-[6px] border border-amber-800/40 bg-amber-900/20 px-3 py-2 text-[11px] leading-5 text-amber-200">
      该模型不在内置能力目录中，能力（视觉/工具/上下文窗口等）未知。当前按保守策略运行：图片不会直传给该模型。
      如需启用视觉等能力，请手动填写上方上下文窗口，并在「高级设置」中按需配置；也可在诊断页触发「自动配对」尝试从供应商目录补全。
    </div>

    <div v-if="capabilities && capabilities.supportsVision === false" class="rounded-[6px] border border-yellow-800/40 bg-yellow-900/20 px-3 py-1.5 text-[11px] text-yellow-200">
      此模型不支持图片输入。图片将按视觉委派设置转交给已配置的识图模型；未配置时保留文字占位说明。
    </div>

    <div v-if="capabilities && capabilities.supportsVision === false" class="mt-1.5 rounded-[6px] border border-[#343434] bg-[#252525] px-3 py-2 text-[11px] text-[#a3a3a3]">
      读图能力已统一到「设置 → 模型与委派 → 高级委派 → 视觉委派」。请在那里选择一个支持视觉输入的模型并启用视觉委派；无需在当前模型中重复配置读图 MCP。
      <button
        type="button"
        class="ml-1 text-[#93c5fd] underline decoration-[#93c5fd]/50 underline-offset-2 hover:text-white"
        @click="emit('open-vision-settings')"
      >
        打开视觉委派设置
      </button>
    </div>

    <!-- 快捷档位按钮 -->
    <div class="inline-flex flex-wrap rounded-[8px] border border-[#3f3f3f] bg-[#232323] p-0.5 text-[12px]" role="group" aria-label="上下文窗口档位">
      <button
        v-for="tier in contextTiers"
        :key="tier.tokens"
        type="button"
        class="relative rounded-[6px] px-2.5 py-1 font-medium transition-colors"
        :class="[
          activeTier?.tokens === tier.tokens
            ? 'bg-[#10AD5D]/25 text-[#6ee7a5]'
            : 'text-[#a3a3a3] hover:text-white',
        ]"
        @click="emit('select-tier', tier)"
      >
        {{ tier.label }}
        <span
          v-if="recommendedTier?.tokens === tier.tokens && activeTier?.tokens !== tier.tokens"
          class="absolute -right-0.5 -top-0.5 h-1.5 w-1.5 rounded-full bg-[#10AD5D]"
        ></span>
      </button>
    </div>

    <input
      :value="modelValue"
      type="text"
      inputmode="numeric"
      placeholder="例如：200000（留空用默认值）"
      class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
      @input="$emit('update:modelValue', $event.target.value)"
    />
    <div v-if="detectedContextWindow" class="text-xs text-[#6ee7a5]">
      已按模型名识别 {{ detectedContextWindow.tokens.toLocaleString() }} tokens（{{ detectedContextWindow.source }}），可直接覆盖。
    </div>
  </label>
</template>