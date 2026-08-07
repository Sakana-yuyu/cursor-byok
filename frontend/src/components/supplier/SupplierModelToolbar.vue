<script setup>
import Button from "@/components/ui/Button.vue";

// 供应商模型列表工具栏：仅负责搜索/筛选/排序控件展示。
// 列表计算、选项常量与删除失败模型操作均由父页面持有。
defineProps({
  search: {
    type: String,
    default: "",
  },
  statusFilter: {
    type: String,
    default: "all",
  },
  sortMode: {
    type: String,
    default: "name",
  },
  statusFilterOptions: {
    type: Array,
    default: () => [],
  },
  sortModeOptions: {
    type: Array,
    default: () => [],
  },
  visibleCount: {
    type: Number,
    default: 0,
  },
  totalCount: {
    type: Number,
    default: 0,
  },
  failedCount: {
    type: Number,
    default: 0,
  },
});

const emit = defineEmits(["update:search", "update:statusFilter", "update:sortMode", "delete-failed"]);
</script>

<template>
  <div class="flex flex-wrap items-center gap-2">
    <div class="relative">
      <span class="icon-[mdi--magnify] pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 text-[16px] text-[#737373]"></span>
      <input
        :value="search"
        type="text"
        placeholder="搜索模型名 / 标识"
        class="h-8 w-52 rounded-[8px] border border-[#3f3f3f] bg-[#232323] pl-7 pr-7 text-[12px] text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
        @input="emit('update:search', $event.target.value)"
      />
      <button
        v-if="search"
        type="button"
        class="absolute right-2 top-1/2 -translate-y-1/2 text-[#737373] hover:text-white"
        @click="emit('update:search', '')"
      >
        <span class="icon-[mdi--close-circle] text-[14px]"></span>
      </button>
    </div>
    <div class="inline-flex rounded-[8px] border border-[#3f3f3f] bg-[#232323] p-0.5 text-[12px]" role="group" aria-label="状态过滤">
      <button
        v-for="opt in statusFilterOptions"
        :key="opt.value"
        type="button"
        class="rounded-[6px] px-2.5 py-1 transition-colors"
        :class="statusFilter === opt.value ? 'bg-[#10AD5D]/25 text-[#6ee7a5]' : 'text-[#a3a3a3] hover:text-white'"
        @click="emit('update:statusFilter', opt.value)"
      >
        {{ opt.label }}
      </button>
    </div>
    <div class="inline-flex rounded-[8px] border border-[#3f3f3f] bg-[#232323] p-0.5 text-[12px]" role="group" aria-label="排序方式">
      <button
        v-for="opt in sortModeOptions"
        :key="opt.value"
        type="button"
        class="rounded-[6px] px-2.5 py-1 transition-colors"
        :class="sortMode === opt.value ? 'bg-[#10AD5D]/25 text-[#6ee7a5]' : 'text-[#a3a3a3] hover:text-white'"
        @click="emit('update:sortMode', opt.value)"
      >
        {{ opt.label }}
      </button>
    </div>
    <span class="text-[12px] text-[#737373]">显示 {{ visibleCount }}/{{ totalCount }}</span>
    <Button
      v-if="failedCount > 0"
      variant="text"
      class="ml-auto text-[#f87171] hover:text-[#fca5a5]"
      @click="emit('delete-failed')"
    >
      <span class="icon-[mdi--trash-can-outline] mr-1 text-[14px]"></span>删除失败模型 ({{ failedCount }})
    </Button>
  </div>
</template>