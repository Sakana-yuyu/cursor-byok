<script setup>
defineProps({
  title: { type: String, required: true },
  description: { type: String, default: "" },
  icon: { type: String, default: "" },
});
</script>

<template>
  <section class="flex flex-col gap-3">
    <div class="flex items-start justify-between gap-3">
      <div class="min-w-0">
        <div class="center-row gap-2">
          <span v-if="icon" :class="icon" class="text-[18px] text-[#6ee7a5]" aria-hidden="true" />
          <h3 class="text-sm font-medium text-white">{{ title }}</h3>
        </div>
        <p v-if="description" class="mt-1 text-xs leading-relaxed text-[#8a8a8a]">{{ description }}</p>
      </div>
      <div v-if="$slots.actions" class="shrink-0">
        <slot name="actions" />
      </div>
    </div>
    <slot />
  </section>
</template>
