<script setup>
defineProps({
  title: { type: String, default: "" },
  description: { type: String, default: "" },
});
</script>

<template>
  <section class="space-y-4">
    <header v-if="title || description" class="space-y-1">
      <h2 v-if="title" class="text-[15px] font-medium text-white">{{ title }}</h2>
      <p v-if="description" class="text-xs leading-5 text-[#8f8f8f]">{{ description }}</p>
    </header>

    <div class="min-w-0">
      <slot />
    </div>
  </section>
</template>
