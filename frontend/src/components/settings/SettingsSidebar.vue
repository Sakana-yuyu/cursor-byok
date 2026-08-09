<script setup>
import Select from "@/components/ui/Select.vue";
import { SETTINGS_GROUPS } from "@/components/settings/settingsCategories";
import { computed, ref, watch } from "vue";

const props = defineProps({
  categories: {
    type: Array,
    default: () => [],
  },
  modelValue: { type: String, default: "general" },
  // 桌面端「更多设置」展开状态由父组件持有，便于深链进入时自动展开。
  moreExpanded: { type: Boolean, default: false },
  // 侧边栏收缩为图标窄栏；由父组件持有并持久化，刷新后保持。
  collapsed: { type: Boolean, default: false },
});

const emit = defineEmits(["update:modelValue", "update:moreExpanded", "update:collapsed"]);

function updateValue(value) {
  emit("update:modelValue", value);
}

function toggleMore() {
  emit("update:moreExpanded", !props.moreExpanded);
}

function toggleCollapsed() {
  emit("update:collapsed", !props.collapsed);
}

const categoryMap = computed(() => new Map(props.categories.map((category) => [category.id, category])));

// 分组标题图标：视觉上强化顶层与子项的层级。
const GROUP_ICONS = {
  core: "icon-[mdi--tune-variant]",
  service: "icon-[mdi--server-outline]",
  view: "icon-[mdi--monitor-dashboard]",
  more: "icon-[mdi--dots-horizontal]",
};

function groupIcon(key) {
  return GROUP_ICONS[key] || "icon-[mdi--dots-horizontal]";
}

// 收缩态「更多设置」popover 浮层显隐：hover/focus 弹出，点击选中后关闭。
const moreFlyoutOpen = ref(false);
let moreFlyoutTimer = null;

function openMoreFlyout() {
  if (moreFlyoutTimer) {
    clearTimeout(moreFlyoutTimer);
    moreFlyoutTimer = null;
  }
  moreFlyoutOpen.value = true;
}

function scheduleCloseMoreFlyout() {
  if (moreFlyoutTimer) {
    clearTimeout(moreFlyoutTimer);
  }
  moreFlyoutTimer = setTimeout(() => {
    moreFlyoutOpen.value = false;
    moreFlyoutTimer = null;
  }, 150);
}

function selectFromFlyout(categoryID) {
  updateValue(categoryID);
  moreFlyoutOpen.value = false;
}

// 桌面端按 nav 分区：common 常驻，more 收纳到可展开区。
const commonGroups = computed(() =>
  SETTINGS_GROUPS.filter((group) => group.nav === "common")
    .map((group) => ({
      ...group,
      items: group.categories.map((id) => categoryMap.value.get(id)).filter(Boolean),
    }))
    .filter((group) => group.items.length > 0),
);

const moreGroup = computed(() => {
  const group = SETTINGS_GROUPS.find((item) => item.nav === "more");
  if (!group) {
    return null;
  }

  const items = group.categories.map((id) => categoryMap.value.get(id)).filter(Boolean);
  return items.length > 0 ? { ...group, items } : null;
});

// 当前选中项位于「更多设置」时，父组件会同步展开，这里保持视觉一致。
watch(
  () => props.modelValue,
  (value) => {
    const category = categoryMap.value.get(value);
    if (category?.nav === "more" && !props.moreExpanded) {
      emit("update:moreExpanded", true);
    }
  },
);
</script>

<template>
  <div
    class="w-full min-w-0 transition-[width] duration-200"
    :class="collapsed ? 'sm:w-[56px]' : 'sm:w-[192px]'"
  >
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
      <!-- 收缩切换按钮：仅桌面端，收缩/展开侧边栏 -->
      <button
        type="button"
        class="mb-2 flex w-full items-center rounded-[6px] py-1.5 text-[#8f8f8f] transition-colors hover:bg-[#252525] hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35"
        :class="collapsed ? 'justify-center' : 'justify-end px-3'"
        :title="collapsed ? '展开侧边栏' : '收起侧边栏'"
        :aria-label="collapsed ? '展开侧边栏' : '收起侧边栏'"
        :aria-expanded="!collapsed"
        @click="toggleCollapsed"
      >
        <span
          class="transition-transform duration-200"
          :class="collapsed ? 'rotate-180' : ''"
          aria-hidden="true"
        >
          <span class="icon-[mdi--chevron-double-left] text-[16px]" />
        </span>
      </button>

      <div class="space-y-4">
        <section v-for="group in commonGroups" :key="group.key">
          <div
            class="mb-1.5 flex items-center gap-1.5 px-3 text-[11px] font-semibold uppercase tracking-wider text-[#8f8f8f]"
            :class="collapsed ? 'justify-center px-0' : ''"
          >
            <span :class="groupIcon(group.key)" class="text-[13px] text-[#6f6f6f]" aria-hidden="true" />
            <span v-if="!collapsed">{{ group.label }}</span>
          </div>
          <div class="space-y-1">
            <button
              v-for="category in group.items"
              :key="category.id"
              type="button"
              class="flex w-full items-start justify-between gap-3 rounded-[6px] border-l-2 py-2.5 pl-4 pr-3 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35"
              :class="[
                category.id === modelValue
                  ? 'border-[#10AD5D] bg-[#262626] text-white'
                  : 'border-transparent text-[#9a9a9a] hover:bg-[#252525] hover:text-white',
                collapsed ? 'justify-center pl-0 pr-0' : '',
              ]"
              :aria-current="category.id === modelValue ? 'page' : undefined"
              :title="collapsed ? category.label : undefined"
              @click="updateValue(category.id)"
            >
              <span v-if="collapsed" :class="category.icon" class="text-[18px]" aria-hidden="true" />
              <span v-else class="min-w-0">
                <span class="block text-sm font-medium">{{ category.label }}</span>
                <span class="mt-0.5 block text-xs leading-5 text-[#777]">{{ category.description }}</span>
              </span>
            </button>
          </div>
        </section>

        <section v-if="moreGroup">
          <!-- 展开态：原有的「更多设置」可折叠二级菜单 -->
          <template v-if="!collapsed">
            <button
              type="button"
              class="mb-1.5 flex w-full items-center justify-between gap-1.5 rounded-[6px] px-3 py-1 text-[11px] font-semibold uppercase tracking-wider text-[#8f8f8f] transition-colors hover:bg-[#252525] hover:text-[#e5e5e5] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35"
              :aria-expanded="moreExpanded"
              @click="toggleMore"
            >
              <span class="flex items-center gap-1.5">
                <span :class="groupIcon('more')" class="text-[13px] text-[#6f6f6f]" aria-hidden="true" />
                {{ moreGroup.label }}
              </span>
              <span
                class="transition-transform duration-200"
                :class="moreExpanded ? 'rotate-180' : ''"
                aria-hidden="true"
              >
                <span class="icon-[mdi--chevron-down] text-[13px]" />
              </span>
            </button>
            <Transition name="settings-more">
              <div v-if="moreExpanded" class="space-y-1 pl-2">
                <button
                  v-for="category in moreGroup.items"
                  :key="category.id"
                  type="button"
                  class="flex w-full items-start justify-between gap-3 rounded-[6px] border-l-2 py-2.5 pl-4 pr-3 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35"
                  :class="category.id === modelValue
                    ? 'border-[#10AD5D] bg-[#262626] text-white'
                    : 'border-transparent text-[#9a9a9a] hover:bg-[#252525] hover:text-white'"
                  :aria-current="category.id === modelValue ? 'page' : undefined"
                  @click="updateValue(category.id)"
                >
                  <span class="min-w-0">
                    <span class="block text-sm font-medium">{{ category.label }}</span>
                    <span class="mt-0.5 block text-xs leading-5 text-[#777]">{{ category.description }}</span>
                  </span>
                </button>
              </div>
            </Transition>
          </template>

          <!-- 收缩态：more icon + hover/focus 弹出 popover 浮层 -->
          <div
            v-else
            class="relative"
            @mouseenter="openMoreFlyout"
            @mouseleave="scheduleCloseMoreFlyout"
          >
            <button
              type="button"
              class="flex w-full items-center justify-center rounded-[6px] py-2.5 text-[#9a9a9a] transition-colors hover:bg-[#252525] hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35"
              :class="moreGroup.items.some((category) => category.id === modelValue) ? 'text-white' : ''"
              :title="moreGroup.label"
              :aria-expanded="moreFlyoutOpen"
              :aria-haspopup="true"
              @focus="openMoreFlyout"
              @blur="scheduleCloseMoreFlyout"
              @click="openMoreFlyout"
            >
              <span :class="groupIcon('more')" class="text-[18px]" aria-hidden="true" />
            </button>
            <Transition name="settings-more">
              <div
                v-if="moreFlyoutOpen"
                class="absolute left-full top-0 z-30 ml-2 w-[200px] rounded-[8px] border border-[#3f3f3f] bg-[#262626] p-1.5 shadow-lg"
                @mouseenter="openMoreFlyout"
                @mouseleave="scheduleCloseMoreFlyout"
              >
                <div class="mb-1 px-2 py-1 text-[11px] font-semibold uppercase tracking-wider text-[#8f8f8f]">
                  {{ moreGroup.label }}
                </div>
                <button
                  v-for="category in moreGroup.items"
                  :key="category.id"
                  type="button"
                  class="flex w-full items-start gap-2 rounded-[6px] px-2 py-2 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35"
                  :class="category.id === modelValue
                    ? 'bg-[#333] text-white'
                    : 'text-[#9a9a9a] hover:bg-[#333] hover:text-white'"
                  :aria-current="category.id === modelValue ? 'page' : undefined"
                  @mousedown.prevent="selectFromFlyout(category.id)"
                >
                  <span :class="category.icon" class="mt-0.5 shrink-0 text-[16px]" aria-hidden="true" />
                  <span class="min-w-0">
                    <span class="block text-sm font-medium">{{ category.label }}</span>
                    <span class="mt-0.5 block text-xs leading-5 text-[#777]">{{ category.description }}</span>
                  </span>
                </button>
              </div>
            </Transition>
          </div>
        </section>
      </div>
    </nav>
  </div>
</template>

<style scoped>
.settings-more-enter-active,
.settings-more-leave-active {
  transition: opacity 150ms ease, transform 150ms ease;
}

.settings-more-enter-from,
.settings-more-leave-to {
  opacity: 0;
  transform: translateY(-2px);
}
</style>
