<script setup>
defineProps({
  icon: { type: String, required: true },
  label: { type: String, required: true },
  disabled: { type: Boolean, default: false },
  danger: { type: Boolean, default: false },
  spinning: { type: Boolean, default: false },
});
</script>

<template>
  <button
    type="button"
    :aria-label="label"
    :title="label"
    :disabled="disabled"
    class="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-[6px] border border-white/10 bg-black/15 text-[#a3a3a3] transition-colors hover:border-white/15 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35 disabled:cursor-not-allowed disabled:opacity-45"
    :class="danger ? 'hover:border-[#5a2a2a] hover:text-[#fca5a5]' : ''"
  >
    <span
      :class="[icon, spinning ? 'animate-spin' : '', 'text-[16px]']"
      aria-hidden="true"
    ></span>
  </button>
</template>
