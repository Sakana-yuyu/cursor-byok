<script setup>
import { autoUpdate, computePosition, flip, offset, shift, size } from "@floating-ui/dom";
import { computed, nextTick, onBeforeUnmount, ref, watch, watchPostEffect } from "vue";

const props = defineProps({
  // modelValue 是已选中的模型 ID 数组。
  modelValue: {
    type: Array,
    default: () => [],
  },
  adapters: {
    type: Array,
    default: () => [],
  },
  placeholder: { type: String, default: "选择可用模型" },
  disabled: { type: Boolean, default: false },
  ariaLabel: { type: String, default: "" },
});

const emit = defineEmits(["toggle"]);

const rootRef = ref(null);
const buttonRef = ref(null);
const menuRef = ref(null);
const scrollRef = ref(null);
const optionRefs = ref([]);
const groupHeaderRefs = ref([]);
const isOpen = ref(false);
const activeIndex = ref(-1);
const menuStyle = ref({});
const expandedGroupKeys = ref(new Set());
const initializedGroups = ref(false);

// selectedSet 用 Set 加速 includes 判断，避免每次 toggle 都 O(n) 扫数组。
const selectedSet = computed(() => new Set(props.modelValue));

function isSelected(value) {
  return selectedSet.value.has(value);
}

// normalizedAdapters 把扁平的 adapters 规整为 { value, label, groupNameRaw, groupLabel }。
// 复用 ModelTreeSelect 的分组逻辑：按 groupName 分桶，空名兜底「默认分组」。
const normalizedAdapters = computed(() => props.adapters
  .map((adapter, index) => {
    const value = String(adapter?.id || "").trim();
    if (!value) {
      return null;
    }

    const groupNameRaw = String(adapter?.groupName || "").trim();
    return {
      value,
      label: String(adapter?.displayName || adapter?.modelID || adapter.id || `模型 ${index + 1}`),
      groupNameRaw,
      groupLabel: groupNameRaw || "默认分组",
    };
  })
  .filter(Boolean));

// modelGroups 按 groupName 分桶并排序，与 ModelTreeSelect 完全一致。
const modelGroups = computed(() => {
  const buckets = new Map();
  for (const adapter of normalizedAdapters.value) {
    const key = `group::${adapter.groupNameRaw}`;
    if (!buckets.has(key)) {
      buckets.set(key, {
        key,
        label: adapter.groupLabel,
        models: [],
      });
    }
    buckets.get(key).models.push(adapter);
  }

  let selectableIndex = 0;
  return Array.from(buckets.values())
    .sort((left, right) => left.label.localeCompare(right.label, "zh-CN", { numeric: true, sensitivity: "base" }))
    .map((group) => ({
      ...group,
      models: [...group.models]
        .sort((left, right) => left.label.localeCompare(right.label, "zh-CN", { numeric: true, sensitivity: "base" }))
        .map((model) => ({
          ...model,
          selectableIndex: selectableIndex++,
        })),
    }));
});

// selectableItems 是扁平化的可导航项列表，用于键盘方向键。
const selectableItems = computed(() => {
  const items = [];
  for (const group of modelGroups.value) {
    for (const model of group.models) {
      items.push({ ...model, kind: "model", groupKey: group.key });
    }
  }
  return items;
});

// groupState 计算每个组的选中状态：all / partial / none，用于组头 checkbox 显示。
const groupStates = computed(() => {
  const states = new Map();
  for (const group of modelGroups.value) {
    const total = group.models.length;
    let selected = 0;
    for (const model of group.models) {
      if (isSelected(model.value)) selected++;
    }
    states.set(group.key, {
      total,
      selected,
      all: total > 0 && selected === total,
      partial: selected > 0 && selected < total,
      none: selected === 0,
    });
  }
  return states;
});

const selectedCount = computed(() => selectedSet.value.size);
const selectedLabel = computed(() => {
  if (selectedCount.value === 0) return props.placeholder;
  return `已选 ${selectedCount.value} 个`;
});

function setOptionRef(el, index) {
  if (el) {
    optionRefs.value[index] = el;
    return;
  }

  delete optionRefs.value[index];
}

function setGroupHeaderRef(el, groupKey) {
  if (el) {
    groupHeaderRefs.value[groupKey] = el;
  } else {
    delete groupHeaderRefs.value[groupKey];
  }
}

function focusActiveOption() {
  nextTick(() => {
    const option = optionRefs.value[activeIndex.value];
    if (!option) {
      return;
    }
    option.focus({ preventScroll: true });
    option.scrollIntoView({ block: "nearest" });
  });
}

function ensureSelectedGroupExpanded() {
  // 打开时展开含已选模型的组，让用户立刻看到当前选择。
  const next = new Set(expandedGroupKeys.value);
  for (const group of modelGroups.value) {
    if (group.models.some((model) => isSelected(model.value))) {
      next.add(group.key);
    }
  }
  expandedGroupKeys.value = next;
}

function initializeGroups() {
  if (!initializedGroups.value) {
    expandedGroupKeys.value = new Set(modelGroups.value.map((group) => group.key));
    initializedGroups.value = true;
  }
  ensureSelectedGroupExpanded();
}

function isGroupExpanded(groupKey) {
  return expandedGroupKeys.value.has(groupKey);
}

function toggleGroup(groupKey) {
  const next = new Set(expandedGroupKeys.value);
  if (next.has(groupKey)) {
    next.delete(groupKey);
  } else {
    next.add(groupKey);
  }
  expandedGroupKeys.value = next;
  nextTick(updatePosition);
}

function openMenu() {
  if (props.disabled || isOpen.value) {
    return;
  }

  initializeGroups();
  isOpen.value = true;
  // 打开时聚焦第一个已选项，没有则第一项。
  const firstSelected = selectableItems.value.findIndex((item) => isSelected(item.value));
  activeIndex.value = firstSelected >= 0 ? firstSelected : 0;
  nextTick(() => {
    updatePosition();
    focusActiveOption();
  });
}

function closeMenu({ restoreFocus = false } = {}) {
  if (!isOpen.value) {
    return;
  }

  isOpen.value = false;
  activeIndex.value = -1;
  optionRefs.value = [];
  groupHeaderRefs.value = [];
  menuStyle.value = {};
  if (restoreFocus) {
    nextTick(() => buttonRef.value?.focus());
  }
}

function toggleMenu() {
  if (isOpen.value) {
    closeMenu();
    return;
  }
  openMenu();
}

// toggleModel 切换单个模型的选中状态。多选不关闭菜单。
function toggleModel(model) {
  if (!model) {
    return;
  }
  emit("toggle", { modelID: model.value, enabled: !isSelected(model.value) });
}

// toggleGroupAll 切换整组的选中状态：全选时取消全选，否则全选。
function toggleGroupAll(group) {
  const state = groupStates.value.get(group.key);
  if (!state) return;
  const enableAll = !state.all;
  // 逐个 emit，保持与单选一致的 toggle:model 契约，父组件的 autosave/rollback 照常工作。
  for (const model of group.models) {
    const currentlySelected = isSelected(model.value);
    if (enableAll && !currentlySelected) {
      emit("toggle", { modelID: model.value, enabled: true });
    } else if (!enableAll && currentlySelected) {
      emit("toggle", { modelID: model.value, enabled: false });
    }
  }
}

function moveActiveIndex(step) {
  if (!selectableItems.value.length) {
    return;
  }

  if (!isOpen.value) {
    openMenu();
    return;
  }

  const total = selectableItems.value.length;
  const current = activeIndex.value >= 0 ? activeIndex.value : 0;
  activeIndex.value = (current + step + total) % total;
  const nextItem = selectableItems.value[activeIndex.value];
  if (nextItem?.kind === "model") {
    const nextGroups = new Set(expandedGroupKeys.value);
    nextGroups.add(nextItem.groupKey);
    expandedGroupKeys.value = nextGroups;
  }
  focusActiveOption();
}

function handleButtonKeydown(event) {
  if (props.disabled) {
    return;
  }

  switch (event.key) {
    case "ArrowDown":
      event.preventDefault();
      moveActiveIndex(1);
      break;
    case "ArrowUp":
      event.preventDefault();
      moveActiveIndex(-1);
      break;
    case "Enter":
    case " ":
      event.preventDefault();
      toggleMenu();
      break;
    case "Escape":
      if (isOpen.value) {
        event.preventDefault();
        closeMenu();
      }
      break;
    default:
      break;
  }
}

function handleOptionKeydown(event, item, index) {
  switch (event.key) {
    case "ArrowDown":
      event.preventDefault();
      activeIndex.value = index;
      moveActiveIndex(1);
      break;
    case "ArrowUp":
      event.preventDefault();
      activeIndex.value = index;
      moveActiveIndex(-1);
      break;
    case "Enter":
    case " ":
      event.preventDefault();
      toggleModel(item);
      break;
    case "Escape":
      event.preventDefault();
      closeMenu({ restoreFocus: true });
      break;
    case "Tab":
      closeMenu();
      break;
    default:
      break;
  }
}

function handleGroupKeydown(event, group) {
  if (event.key !== "Enter" && event.key !== " ") {
    return;
  }
  event.preventDefault();
  toggleGroupAll(group);
}

function handlePointerDown(event) {
  if (rootRef.value?.contains(event.target) || menuRef.value?.contains(event.target)) {
    return;
  }
  closeMenu();
}

function updatePosition() {
  if (!buttonRef.value || !menuRef.value) {
    return;
  }

  computePosition(buttonRef.value, menuRef.value, {
    placement: "bottom-start",
    middleware: [
      offset(6),
      flip({ padding: 12 }),
      shift({ padding: 12 }),
      size({
        apply({ rects, elements, availableHeight }) {
          Object.assign(elements.floating.style, {
            minWidth: `${rects.reference.width}px`,
            maxHeight: `${Math.max(availableHeight, 140)}px`,
            "--model-tree-max-height": `${Math.max(availableHeight - 8, 132)}px`,
          });
        },
        padding: 12,
      }),
    ],
  }).then(({ x, y }) => {
    menuStyle.value = {
      left: `${x}px`,
      top: `${y}px`,
    };
  });
}

watchPostEffect((cleanup) => {
  if (!isOpen.value || !buttonRef.value || !menuRef.value) {
    return;
  }

  const stopAutoUpdate = autoUpdate(buttonRef.value, menuRef.value, updatePosition);
  cleanup(() => stopAutoUpdate());
});

watch(
  () => props.modelValue,
  () => {
    if (!isOpen.value) {
      return;
    }
    ensureSelectedGroupExpanded();
  },
);

watch(isOpen, (open) => {
  if (open) {
    document.addEventListener("pointerdown", handlePointerDown);
    return;
  }
  document.removeEventListener("pointerdown", handlePointerDown);
});

onBeforeUnmount(() => {
  document.removeEventListener("pointerdown", handlePointerDown);
});
</script>

<template>
  <div ref="rootRef" class="relative">
    <button
      ref="buttonRef"
      type="button"
      :disabled="disabled"
      class="flex h-9 w-full items-center justify-between gap-2 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-left text-sm text-[#e5e5e5] outline-none transition-colors focus:border-[#10AD5D] disabled:cursor-not-allowed disabled:opacity-60"
      :aria-expanded="isOpen"
      :aria-label="ariaLabel || undefined"
      aria-haspopup="listbox"
      @click="toggleMenu"
      @keydown="handleButtonKeydown"
    >
      <span class="min-w-0 flex-1 truncate" :class="selectedCount > 0 ? 'text-[#e5e5e5]' : 'text-[#7b7b7b]'">
        {{ selectedLabel }}
      </span>
      <span
        class="pointer-events-none center-row shrink-0 text-[#8f8f8f] transition-transform duration-200"
        :class="isOpen ? 'rotate-180' : ''"
      >
        <span class="icon-[mdi--chevron-down] text-[18px]"></span>
      </span>
    </button>
  </div>

  <Teleport to="body">
    <Transition
      enter-active-class="transition duration-150 ease-out"
      enter-from-class="translate-y-1 opacity-0"
      enter-to-class="translate-y-0 opacity-100"
      leave-active-class="transition duration-100 ease-in"
      leave-from-class="translate-y-0 opacity-100"
      leave-to-class="translate-y-1 opacity-0"
    >
      <div
        v-if="isOpen"
        ref="menuRef"
        class="fixed z-[10010] overflow-hidden rounded-[8px] border border-[#3f3f3f] bg-[#232323] p-1 shadow-[0_16px_30px_-12px_rgba(0,0,0,0.7)]"
        :style="menuStyle"
      >
        <div
          ref="scrollRef"
          role="listbox"
          aria-multiselectable="true"
          class="model-tree-scroll max-h-[min(60vh,360px)] overflow-y-auto overscroll-contain py-1"
          @wheel.stop
        >
          <div
            v-for="group in modelGroups"
            :key="group.key"
            class="mt-1 first:mt-0"
            role="group"
            :aria-label="group.label"
          >
            <div
              :ref="(el) => setGroupHeaderRef(el, group.key)"
              class="flex w-full items-center gap-2 rounded-[6px] px-2.5 py-1.5 text-left text-[11px] font-medium text-[#8f8f8f] outline-none transition-colors hover:bg-[#303030] hover:text-[#d4d4d4]"
            >
              <button
                type="button"
                class="flex min-w-0 flex-1 items-center gap-2 outline-none"
                :aria-expanded="isGroupExpanded(group.key)"
                :aria-label="`${isGroupExpanded(group.key) ? '收起' : '展开'} ${group.label}`"
                @click="toggleGroup(group.key)"
              >
                <span
                  class="shrink-0 text-[14px] transition-transform duration-150"
                  :class="isGroupExpanded(group.key) ? 'rotate-90' : ''"
                >
                  <span class="icon-[mdi--chevron-right]"></span>
                </span>
                <span class="min-w-0 flex-1 truncate">{{ group.label }}</span>
                <span class="shrink-0 text-[10px] text-[#666]">{{ groupStates.get(group.key)?.selected || 0 }}/{{ group.models.length }}</span>
              </button>
              <button
                type="button"
                class="center-row size-4 shrink-0 text-[16px] outline-none transition-colors hover:text-[#d4d4d4]"
                :class="groupStates.get(group.key)?.all ? 'text-[#10d06f]' : (groupStates.get(group.key)?.partial ? 'text-[#fbbf24]' : 'text-[#666]')"
                :aria-label="groupStates.get(group.key)?.all ? `取消全选 ${group.label}` : `全选 ${group.label}`"
                @click="toggleGroupAll(group)"
                @keydown="handleGroupKeydown($event, group)"
              >
                <span
                  :class="[
                    groupStates.get(group.key)?.all
                      ? 'icon-[mdi--check-box]'
                      : groupStates.get(group.key)?.partial
                        ? 'icon-[mdi--minus-box-outline]'
                        : 'icon-[mdi--checkbox-blank-outline]',
                  ]"
                ></span>
              </button>
            </div>

            <div v-show="isGroupExpanded(group.key)" class="pl-4">
              <button
                v-for="model in group.models"
                :key="model.value"
                :ref="(el) => setOptionRef(el, model.selectableIndex)"
                type="button"
                role="option"
                class="flex w-full min-w-0 items-center gap-2 rounded-[6px] px-3 py-2 text-left text-sm outline-none transition-colors"
                :class="[
                  isSelected(model.value)
                    ? 'bg-[#10AD5D]/15 text-[#10d06f]'
                    : 'text-[#e5e5e5] hover:bg-[#303030]',
                  activeIndex === model.selectableIndex ? 'bg-[#303030]' : '',
                ]"
                :aria-selected="isSelected(model.value)"
                tabindex="0"
                :title="model.label"
                @click="toggleModel(model)"
                @mouseenter="activeIndex = model.selectableIndex"
                @keydown="handleOptionKeydown($event, { ...model, kind: 'model', groupKey: group.key }, model.selectableIndex)"
              >
                <span
                  class="shrink-0 text-[16px]"
                  :class="isSelected(model.value) ? 'icon-[mdi--check-box] text-[#10d06f]' : 'icon-[mdi--checkbox-blank-outline] text-[#666]'"
                ></span>
                <span class="min-w-0 truncate">{{ model.label }}</span>
              </button>
            </div>
          </div>

          <div
            v-if="!modelGroups.length"
            class="px-3 py-3 text-xs text-[#858585]"
          >
            暂无可用模型，请先在模型配置中添加模型适配器。
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.model-tree-scroll {
  scrollbar-gutter: stable;
  overscroll-behavior: contain;
  touch-action: pan-y;
}

.model-tree-scroll::-webkit-scrollbar {
  width: 8px;
}

.model-tree-scroll::-webkit-scrollbar-thumb {
  border: 2px solid transparent;
  border-radius: 999px;
  background: #4a4a4a;
  background-clip: padding-box;
}

.model-tree-scroll::-webkit-scrollbar-thumb:hover {
  background: #626262;
  background-clip: padding-box;
}

.model-tree-scroll::-webkit-scrollbar-track {
  background: transparent;
}
</style>
