<script setup>
import Select from "@/components/ui/Select.vue";
import { SETTINGS_GROUPS } from "@/components/settings/settingsCategories";
import { computed } from "vue";

const props = defineProps({
  categories: {
    type: Array,
    default: () => [],
  },
  modelValue: { type: String, default: "general" },
});

const emit = defineEmits(["update:modelValue"]);

function updateValue(value) {
  emit("update:modelValue", value);
}

const categoryMap = computed(() => new Map(props.categories.map((category) => [category.id, category])));

// 每个分组渲染「标题 + 组内分类」；无分类的分组自动跳过。
const visibleGroups = computed(() =>
  SETTINGS_GROUPS.map((group) => ({
    ...group,
    items: group.categories.map((id) => categoryMap.value.get(id)).filter(Boolean),
  })).filter((group) => group.items.length > 0),
);
</script>

<template>
  <div class="w-full min-w-0 sm:w-[192px]">
    <div class="sm:hidden">
      <Select
        :model-value="modelValue"
        :options="categories"
        aria-label="设置分类"
        button-class="h-10 w-full min-w-0"
        @update:model-value="updateValue"
      />
    </div>

    <nav aria-label="设置分类" class="hidden w-full shrink-0 sm:block">
      <div class="space-y-4">
        <section v-for="group in visibleGroups" :key="group.key">
          <div class="mb-1 px-3 text-[10px] font-medium uppercase tracking-wider text-[#666]">
            {{ group.label }}
          </div>
          <div class="space-y-1">
            <button
              v-for="category in group.items"
              :key="category.id"
              type="button"
              class="flex w-full items-start justify-between gap-3 rounded-[6px] px-3 py-2.5 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35"
              :class="category.id === modelValue ? 'bg-[#292929] text-white' : 'text-[#9a9a9a] hover:bg-[#252525] hover:text-white'"
              :aria-current="category.id === modelValue ? 'page' : undefined"
              @click="updateValue(category.id)"
            >
              <span class="min-w-0">
                <span class="block text-sm font-medium">{{ category.label }}</span>
                <span class="mt-1 block text-xs leading-5 text-[#777]">{{ category.description }}</span>
              </span>
            </button>
          </div>
        </section>
      </div>
    </nav>
  </div>
</template>