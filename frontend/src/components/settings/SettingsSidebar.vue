<script setup>
import Select from "@/components/ui/Select.vue";
import { SETTINGS_GROUPS } from "@/components/settings/settingsCategories";
import { computed, onBeforeUnmount, onMounted, ref } from "vue";

// 设置分类顶栏：分类较多时按组折叠，悬浮组按钮展开菜单选择。
// 单分类组直接是 chip；多分类组悬浮/点击展开下拉菜单（描述在菜单项里展示）。
const props = defineProps({
  categories: {
    type: Array,
    default: () => [],
  },
  modelValue: { type: String, default: "general" },
});

const emit = defineEmits(["update:modelValue"]);

const CHIP_ACTIVE_CLASS = "bg-[var(--active-bg)] text-[var(--active-text)]";
const CHIP_IDLE_CLASS = "text-[#9a9a9a] hover:bg-[var(--bg-hover)] hover:text-[#e5e5e5]";

function updateValue(value) {
  emit("update:modelValue", value);
}

const categoryMap = computed(() => new Map(props.categories.map((category) => [category.id, category])));

const groups = computed(() =>
  SETTINGS_GROUPS.map((group) => ({
    ...group,
    items: group.categories.map((id) => categoryMap.value.get(id)).filter(Boolean),
  })).filter((group) => group.items.length > 0),
);

// 悬浮展开：关延迟 120ms，给指针移入菜单留出路径；点击切换兜底（触屏/键盘）。
const openGroupKey = ref("");
let closeTimer = null;

function cancelClose() {
  if (closeTimer) {
    clearTimeout(closeTimer);
    closeTimer = null;
  }
}

function hoverOpen(key) {
  cancelClose();
  openGroupKey.value = key;
}

function hoverClose() {
  cancelClose();
  closeTimer = setTimeout(() => {
    openGroupKey.value = "";
  }, 120);
}

function toggleGroup(key) {
  // 点击始终展开（hover 可能已先展开，此时点击不应把菜单又关掉）；
  // 关闭路径：指针移出、点外部、选中项。
  openGroupKey.value = key;
}

function selectFromMenu(categoryID) {
  updateValue(categoryID);
  openGroupKey.value = "";
}

function groupActive(group) {
  return group.items.some((category) => category.id === props.modelValue);
}

onBeforeUnmount(() => {
  if (closeTimer) clearTimeout(closeTimer);
  document.removeEventListener("pointerdown", handleOutsidePointerDown);
});

function handleOutsidePointerDown(event) {
  if (!(event.target instanceof Element)) return;
  if (!event.target.closest("[data-settings-group]")) {
    openGroupKey.value = "";
  }
}

onMounted(() => {
  document.addEventListener("pointerdown", handleOutsidePointerDown);
});
</script>

<template>
  <div class="w-full min-w-0">
    <div class="px-4 pt-3 sm:hidden">
      <Select
        :model-value="modelValue"
        :options="categories"
        aria-label="设置分类"
        button-class="h-10 w-full min-w-0"
        @update:model-value="updateValue"
      />
    </div>

    <nav
      aria-label="设置分类"
      class="hidden items-center gap-1 border-b border-[#242424] px-4 py-2 sm:flex"
    >
      <template v-for="(group, groupIndex) in groups" :key="group.key">
        <div v-if="groupIndex > 0" class="mx-2 h-4 w-px shrink-0 bg-[#333]" aria-hidden="true"></div>

        <!-- 单分类组：直接 chip -->
        <button
          v-if="group.items.length === 1"
          type="button"
          class="flex h-[28px] shrink-0 items-center gap-[6px] rounded-full px-3 text-[12px] transition-colors"
          :class="groupActive(group) ? CHIP_ACTIVE_CLASS : CHIP_IDLE_CLASS"
          :aria-current="groupActive(group) ? 'page' : undefined"
          :title="group.items[0].description"
          @click="updateValue(group.items[0].id)"
        >
          <span :class="group.items[0].icon" class="shrink-0 text-[15px]" aria-hidden="true" />
          <span class="whitespace-nowrap leading-none">{{ group.items[0].label }}</span>
        </button>

        <!-- 多分类组：悬浮/点击展开菜单 -->
        <div
          v-else
          class="relative"
          data-settings-group
          @mouseenter="hoverOpen(group.key)"
          @mouseleave="hoverClose()"
        >
          <button
            type="button"
            class="flex h-[28px] shrink-0 items-center gap-[6px] rounded-full px-3 text-[12px] transition-colors"
            :class="[
              groupActive(group) ? CHIP_ACTIVE_CLASS : CHIP_IDLE_CLASS,
              openGroupKey === group.key ? 'ring-1 ring-[#10AD5D]/40' : '',
            ]"
            :aria-haspopup="true"
            :aria-expanded="openGroupKey === group.key ? 'true' : 'false'"
            @click="toggleGroup(group.key)"
          >
            <span class="whitespace-nowrap leading-none">{{ group.label }}</span>
            <span
              v-if="groupActive(group)"
              class="h-[5px] w-[5px] rounded-full bg-current opacity-80"
              :title="group.items.find((c) => c.id === modelValue)?.label"
            ></span>
            <span
              class="text-[13px] opacity-70 transition-transform"
              :class="[
                openGroupKey === group.key ? 'icon-[mdi--chevron-up]' : 'icon-[mdi--chevron-down]',
              ]"
              aria-hidden="true"
            ></span>
          </button>

          <div
            v-if="openGroupKey === group.key"
            class="absolute left-0 top-full z-30 mt-1 w-[240px] rounded-[10px] border border-white/10 bg-[#292929] p-1.5 shadow-[0_12px_32px_rgba(0,0,0,0.35)]"
            role="menu"
            :aria-label="group.label"
            @mouseenter="cancelClose"
            @mouseleave="hoverClose()"
          >
            <button
              v-for="category in group.items"
              :key="category.id"
              type="button"
              role="menuitem"
              class="flex w-full items-start gap-2 rounded-[7px] px-2.5 py-2 text-left transition-colors"
              :class="category.id === modelValue
                ? 'bg-[var(--active-bg)] text-[var(--active-text)]'
                : 'text-[#a8a8a8] hover:bg-white/[0.06] hover:text-white'"
              :aria-current="category.id === modelValue ? 'page' : undefined"
              @click="selectFromMenu(category.id)"
            >
              <span :class="category.icon" class="mt-0.5 shrink-0 text-[16px]" aria-hidden="true" />
              <span class="min-w-0">
                <span class="block text-sm font-medium leading-5">{{ category.label }}</span>
                <span class="mt-0.5 block text-xs leading-4 text-[#8f8f8f]">{{ category.description }}</span>
              </span>
            </button>
          </div>
        </div>
      </template>
    </nav>
  </div>
</template>
