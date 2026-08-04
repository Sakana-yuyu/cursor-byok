<script setup>
import { nextTick, onBeforeUnmount, onMounted, ref, useId } from "vue";

defineProps({
  disabled: {
    type: Boolean,
    default: false,
  },
  // 菜单最小宽度，默认 160px。
  minWidth: {
    type: String,
    default: "160px",
  },
  // matchTrigger 为 true 时，菜单左右边缘与触发器对齐，宽度等于触发器宽度
  // （通过同时设 left-0 right-0 实现，优先级高于 minWidth）。
  matchTrigger: {
    type: Boolean,
    default: false,
  },
});

const open = ref(false);
const menuRef = ref(null);
const triggerRef = ref(null);
// 稳定 id，供 aria-controls 关联触发器与菜单。
const menuId = useId();

function toggle() {
  if (!open.value) {
    open.value = true;
  } else {
    open.value = false;
  }
}

function close() {
  open.value = false;
}

function openMenu() {
  if (open.value) return;
  open.value = true;
}

function focusTrigger() {
  if (triggerRef.value && typeof triggerRef.value.focus === "function") {
    triggerRef.value.focus();
  }
}

// 取得菜单内菜单项列表，用于方向键导航。
// 跳过 disabled 菜单项（原生 disabled button 无法 focus，会让方向键导航卡住）。
function getMenuItems() {
  const root = menuRef.value;
  if (!root) return [];
  const nodes = root.querySelectorAll('[role="menuitem"]');
  return Array.from(nodes).filter((el) => {
    if (el.hasAttribute("disabled") || el.disabled === true) return false;
    return el.offsetParent !== null || el === document.activeElement;
  });
}

function focusMenuItem(index) {
  const items = getMenuItems();
  if (items.length === 0) {
    // 没有菜单项时焦点留在触发器。
    focusTrigger();
    return;
  }
  const clamped = ((index % items.length) + items.length) % items.length;
  const target = items[clamped];
  if (target && typeof target.focus === "function") {
    target.focus();
    currentRowIndex.value = clamped;
  }
}

// 记录方向键导航的当前菜单项下标，便于 Up/Down 连续移动。
const currentRowIndex = ref(0);

// 触发器的键盘处理：Enter/Space 打开（或关闭）菜单。
// 注意：复合触发器（如 Home.vue 的 split-button）内部可能含独立可聚焦按钮，
// 此时用户聚焦的是外层触发器容器；Enter/Space 只切换菜单，不会触发内部按钮
// 的原生 click（因为焦点不在按钮上）。
function onTriggerKeydown(event) {
  if (event.key === "Enter" || event.key === " " || event.key === "Spacebar") {
    event.preventDefault();
    if (open.value) {
      close();
      // 焦点已在触发器上，保持不变。
    } else {
      openMenu();
      nextTick(() => focusMenuItem(0));
    }
  } else if (event.key === "ArrowDown") {
    event.preventDefault();
    openMenu();
    nextTick(() => focusMenuItem(0));
  } else if (event.key === "ArrowUp") {
    event.preventDefault();
    openMenu();
    nextTick(() => focusMenuItem(-1));
  } else if (event.key === "Escape" && open.value) {
    event.preventDefault();
    close();
    focusTrigger();
  }
}

// 菜单容器的键盘委托：方向键移动、Home/End 跳首尾、Escape 关闭并还焦给触发器。
function onMenuKeydown(event) {
  const items = getMenuItems();
  if (event.key === "ArrowDown") {
    event.preventDefault();
    if (items.length === 0) return;
    const idx = currentRowIndex.value;
    focusMenuItem(idx + 1);
  } else if (event.key === "ArrowUp") {
    event.preventDefault();
    if (items.length === 0) return;
    const idx = currentRowIndex.value;
    focusMenuItem(idx - 1);
  } else if (event.key === "Home") {
    event.preventDefault();
    focusMenuItem(0);
  } else if (event.key === "End") {
    event.preventDefault();
    focusMenuItem(items.length - 1);
  } else if (event.key === "Escape") {
    event.preventDefault();
    close();
    focusTrigger();
  }
}

// 菜单项获得焦点时同步 currentRowIndex，保证方向键从当前项出发。
function onMenuFocusin(event) {
  const items = getMenuItems();
  const idx = items.indexOf(event.target);
  if (idx >= 0) currentRowIndex.value = idx;
}

function handleDocumentClick(event) {
  if (open.value && menuRef.value && !menuRef.value.contains(event.target)) {
    open.value = false;
  }
}

onMounted(() => document.addEventListener("click", handleDocumentClick));
onBeforeUnmount(() => document.removeEventListener("click", handleDocumentClick));
</script>

<template>
  <div ref="menuRef" class="relative inline-block">
    <div
      ref="triggerRef"
      tabindex="0"
      role="button"
      aria-haspopup="menu"
      :aria-expanded="open"
      :aria-controls="menuId"
      class="inline-block outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/45 focus-visible:rounded-[6px]"
      @click="toggle"
      @keydown="onTriggerKeydown"
    >
      <slot name="trigger" />
    </div>
    <div
      v-if="open"
      :id="menuId"
      :class="[
        'absolute top-full z-50 mt-1 overflow-hidden rounded-[8px] border border-[#343434] bg-[#222222] py-1 shadow-lg shadow-black/40',
        matchTrigger ? 'left-0 right-0' : 'right-0',
      ]"
      :style="matchTrigger ? {} : { minWidth: minWidth }"
      role="menu"
      tabindex="-1"
      @keydown="onMenuKeydown"
      @focusin="onMenuFocusin"
    >
      <slot name="items" :close="close" />
    </div>
  </div>
</template>