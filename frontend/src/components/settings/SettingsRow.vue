<script setup>
defineProps({
  label: { type: String, default: "" },
  description: { type: String, default: "" },
  busy: { type: Boolean, default: false },
  error: { type: [String, Object], default: "" },
});

const emit = defineEmits(["retry"]);

function formatError(error) {
  if (!error) {
    return "";
  }

  return typeof error === "string" ? error : String(error.message || error);
}
</script>

<template>
  <div class="settings-row grid gap-4 border-b border-[#343434] py-4 last:border-b-0">
    <div class="min-w-0 space-y-1">
      <div class="text-sm font-medium text-white">{{ label }}</div>
      <div v-if="description" class="text-xs leading-5 text-[#8f8f8f]">
        {{ description }}
      </div>
    </div>

    <div class="min-w-0">
      <div
        class="flex min-h-[36px] min-w-0 items-center justify-start"
        :class="busy ? 'opacity-80' : ''"
      >
        <slot />
      </div>

      <div
        v-if="formatError(error)"
        class="mt-2 flex min-w-0 items-center gap-3 text-xs leading-5 text-[#f2a7a7]"
      >
        <span class="min-w-0 flex-1 break-words">{{ formatError(error) }}</span>
        <button
          type="button"
          class="shrink-0 text-[#10AD5D] transition-colors hover:text-[#33c476]"
          @click="emit('retry')"
        >
          重试
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.settings-row {
  grid-template-columns: minmax(0, 240px) minmax(0, 1fr);
}

@media (max-width: 639px) {
  .settings-row {
    grid-template-columns: minmax(0, 1fr);
    gap: 10px;
  }
}
</style>
