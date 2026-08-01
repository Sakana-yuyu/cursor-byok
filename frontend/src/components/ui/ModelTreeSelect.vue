<script setup>
import { autoUpdate, computePosition, flip, offset, shift, size } from "@floating-ui/dom";
import { computed, nextTick, onBeforeUnmount, ref, watch, watchPostEffect } from "vue";

const props = defineProps({
  modelValue: { type: String, default: "" },
  adapters: {
    type: Array,
    default: () => [],
  },
  fallbackOption: {
    type: Object,
    default: null,
  },
  placeholder: { type: String, default: "请选择模型" },
  disabled: { type: Boolean, default: false },
  ariaLabel: { type: String, default: "" },
});

const emit = defineEmits(["update:modelValue", "change", "blur"]);

const rootRef = ref(null);
const buttonRef = ref(null);
const menuRef = ref(null);
const scrollRef = ref(null);
const optionRefs = ref([]);
const isOpen = ref(false);
const activeIndex = ref(-1);
const menuStyle = ref({});
const expandedGroupKeys = ref(new Set());
const initializedGroups = ref(false);

const normalizedFallback = computed(() => {
  if (!props.fallbackOption) {
    return null;
  }

  return {
    value: String(props.fallbackOption.value ?? ""),
    label: String(props.fallbackOption.label ?? "跟随主模型"),
  };
});

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

  let selectableIndex = normalizedFallback.value ? 1 : 0;
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

const selectableItems = computed(() => {
  const items = normalizedFallback.value ? [{ ...normalizedFallback.value, kind: "fallback" }] : [];
  for (const group of modelGroups.value) {
    for (const model of group.models) {
      items.push({ ...model, kind: "model", groupKey: group.key });
    }
  }
  return items;
});

const selectedItem = computed(() => selectableItems.value.find((item) => item.value === props.modelValue) ?? null);
const selectedLabel = computed(() => selectedItem.value?.label || props.placeholder);

function setOptionRef(el, index) {
  if (el) {
    optionRefs.value[index] = el;
    return;
  }

  delete optionRefs.value[index];
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
  const selected = selectedItem.value;
  if (!selected || selected.kind !== "model") {
    return;
  }

  const next = new Set(expandedGroupKeys.value);
  next.add(selected.groupKey);
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
  const selectedIndex = selectableItems.value.findIndex((item) => item.value === props.modelValue);
  activeIndex.value = selectedIndex >= 0 ? selectedIndex : 0;
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
  menuStyle.value = {};
  if (restoreFocus) {
    nextTick(() => buttonRef.value?.focus());
  }
  emit("blur");
}

function toggleMenu() {
  if (isOpen.value) {
    closeMenu();
    return;
  }
  openMenu();
}

function selectItem(item) {
  if (!item) {
    return;
  }
  if (item.value !== props.modelValue) {
    emit("update:modelValue", item.value);
    emit("change", item.value);
  }
  closeMenu({ restoreFocus: true });
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
      selectItem(item);
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

function handleGroupKeydown(event, groupKey) {
  if (event.key !== "Enter" && event.key !== " ") {
    return;
  }
  event.preventDefault();
  toggleGroup(groupKey);
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
    ensureSelectedGroupExpanded();
    if (!isOpen.value) {
      return;
    }
    const selectedIndex = selectableItems.value.findIndex((item) => item.value === props.modelValue);
    activeIndex.value = selectedIndex >= 0 ? selectedIndex : 0;
    focusActiveOption();
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
      <span class="min-w-0 flex-1 truncate" :class="selectedItem ? 'text-[#e5e5e5]' : 'text-[#7b7b7b]'">
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
          class="model-tree-scroll max-h-[min(60vh,360px)] overflow-y-auto overscroll-contain py-1"
          @wheel.stop
        >
          <button
            v-if="normalizedFallback"
            :ref="(el) => setOptionRef(el, 0)"
            type="button"
            role="option"
            class="flex w-full items-center rounded-[6px] px-3 py-2 text-left text-sm outline-none transition-colors"
            :class="[
              normalizedFallback.value === modelValue
                ? 'bg-[#10AD5D]/15 text-[#10d06f]'
                : 'text-[#e5e5e5] hover:bg-[#303030]',
              activeIndex === 0 ? 'bg-[#303030]' : '',
            ]"
            :aria-selected="normalizedFallback.value === modelValue"
            tabindex="0"
            @click="selectItem({ ...normalizedFallback, kind: 'fallback' })"
            @mouseenter="activeIndex = 0"
            @keydown="handleOptionKeydown($event, { ...normalizedFallback, kind: 'fallback' }, 0)"
          >
            <span class="truncate">{{ normalizedFallback.label }}</span>
          </button>

          <div v-for="group in modelGroups" :key="group.key" class="mt-1 first:mt-0">
            <button
              type="button"
              class="flex w-full items-center gap-2 rounded-[6px] px-2.5 py-1.5 text-left text-[11px] font-medium text-[#8f8f8f] outline-none transition-colors hover:bg-[#303030] hover:text-[#d4d4d4]"
              :aria-expanded="isGroupExpanded(group.key)"
              :aria-label="`${isGroupExpanded(group.key) ? '收起' : '展开'} ${group.label}`"
              @click="toggleGroup(group.key)"
              @keydown="handleGroupKeydown($event, group.key)"
            >
              <span
                class="shrink-0 text-[14px] transition-transform duration-150"
                :class="isGroupExpanded(group.key) ? 'rotate-90' : ''"
              >
                <span class="icon-[mdi--chevron-right]"></span>
              </span>
              <span class="min-w-0 flex-1 truncate">{{ group.label }}</span>
              <span class="shrink-0 text-[10px] text-[#666]">{{ group.models.length }}</span>
            </button>

            <div v-show="isGroupExpanded(group.key)" class="pl-4">
              <button
                v-for="model in group.models"
                :key="model.value"
                :ref="(el) => setOptionRef(el, model.selectableIndex)"
                type="button"
                role="option"
                class="flex w-full min-w-0 items-center rounded-[6px] px-3 py-2 text-left text-sm outline-none transition-colors"
                :class="[
                  model.value === modelValue
                    ? 'bg-[#10AD5D]/15 text-[#10d06f]'
                    : 'text-[#e5e5e5] hover:bg-[#303030]',
                  activeIndex === model.selectableIndex ? 'bg-[#303030]' : '',
                ]"
                :aria-selected="model.value === modelValue"
                tabindex="0"
                :title="model.label"
                @click="selectItem({ ...model, kind: 'model', groupKey: group.key })"
                @mouseenter="activeIndex = model.selectableIndex"
                @keydown="handleOptionKeydown($event, { ...model, kind: 'model', groupKey: group.key }, model.selectableIndex)"
              >
                <span class="min-w-0 truncate">{{ model.label }}</span>
              </button>
            </div>
          </div>

          <div
            v-if="!normalizedFallback && !modelGroups.length"
            class="px-3 py-3 text-xs text-[#858585]"
          >
            暂无可用模型
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