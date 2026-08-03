<script setup>
import { onBeforeUnmount, onMounted, ref } from "vue";

defineProps({
  disabled: {
    type: Boolean,
    default: false,
  },
});

const open = ref(false);
const menuRef = ref(null);

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
    <div @click="toggle">
      <slot name="trigger" />
    </div>
    <div
      v-if="open"
      class="absolute right-0 top-full z-50 mt-1 min-w-[160px] overflow-hidden rounded-[8px] border border-[#343434] bg-[#222222] py-1 shadow-lg shadow-black/40"
      role="menu"
    >
      <slot name="items" :close="close" />
    </div>
  </div>
</template>