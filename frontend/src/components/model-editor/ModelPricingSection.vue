<script setup>
// 定价编辑区：仅负责价格/币种输入展示与上报用户意图。
// draft、价格规范化（updatePricingRate）与持久化保留在父页面，
// 父组件通过 v-model 绑定自有 computed（setter 写回 draft）。
defineProps({
  input: { type: String, default: "" },
  output: { type: String, default: "" },
  cacheRead: { type: String, default: "" },
  cacheWrite: { type: String, default: "" },
  currency: { type: String, default: "USD" },
  known: { type: Boolean, default: false },
  source: { type: String, default: "" },
});

defineEmits(["update:input", "update:output", "update:cacheRead", "update:cacheWrite", "update:currency", "clear"]);
</script>

<template>
  <div class="rounded-[8px] border border-[#343434] bg-[#252525] p-3 md:col-span-2">
    <div class="flex items-center justify-between gap-3">
      <div>
        <div class="text-sm text-[#d4d4d4]">价格（每百万 token）</div>
        <div class="mt-1 text-xs text-[#8f8f8f]">
          用于请求明细和首页成本估算；留空时自动使用内置官方价。
          <span v-if="source === 'manual'" class="text-[#6ee7a5]">· 手动配价</span>
          <span v-else-if="source === 'catalog'" class="text-[#6ee7a5]">· 中转站探测价</span>
          <span v-else-if="source" class="text-[#6ee7a5]">· {{ source }}</span>
          <span v-else class="text-[#8f8f8f]">· 未配置（将使用内置官方价）</span>
        </div>
      </div>
      <button
        v-if="known"
        type="button"
        class="shrink-0 text-xs text-[#fca5a5] hover:text-white"
        @click="$emit('clear')"
      >
        清除价格
      </button>
    </div>
    <div class="mt-3 grid grid-cols-2 gap-3 md:grid-cols-5">
      <label class="flex flex-col gap-1">
        <span class="text-xs text-[#a3a3a3]">输入</span>
        <input
          :value="input"
          type="text"
          inputmode="decimal"
          placeholder="0.00"
          class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
          @input="$emit('update:input', $event.target.value)"
        />
      </label>
      <label class="flex flex-col gap-1">
        <span class="text-xs text-[#a3a3a3]">输出</span>
        <input
          :value="output"
          type="text"
          inputmode="decimal"
          placeholder="0.00"
          class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
          @input="$emit('update:output', $event.target.value)"
        />
      </label>
      <label class="flex flex-col gap-1">
        <span class="text-xs text-[#a3a3a3]">缓存读取</span>
        <input
          :value="cacheRead"
          type="text"
          inputmode="decimal"
          placeholder="0.00"
          class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
          @input="$emit('update:cacheRead', $event.target.value)"
        />
      </label>
      <label class="flex flex-col gap-1">
        <span class="text-xs text-[#a3a3a3]">缓存写入</span>
        <input
          :value="cacheWrite"
          type="text"
          inputmode="decimal"
          placeholder="0.00"
          class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
          @input="$emit('update:cacheWrite', $event.target.value)"
        />
      </label>
      <label class="flex flex-col gap-1">
        <span class="text-xs text-[#a3a3a3]">币种</span>
        <input
          :value="currency"
          type="text"
          maxlength="8"
          placeholder="USD"
          class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm uppercase text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
          @input="$emit('update:currency', $event.target.value)"
        />
      </label>
    </div>
  </div>
</template>