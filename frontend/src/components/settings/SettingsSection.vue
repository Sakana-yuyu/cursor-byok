<script setup>
import { ref } from "vue";

const props = defineProps({
  title: { type: String, default: "" },
  description: { type: String, default: "" },
  collapsible: { type: Boolean, default: false },
  defaultExpanded: { type: Boolean, default: true },
});

const expanded = ref(props.defaultExpanded);

function toggleExpanded() {
  if (props.collapsible) {
    expanded.value = !expanded.value;
  }
}
</script>

<template>
  <section class="space-y-4">
    <header v-if="title || description" class="space-y-1">
      <button
        v-if="collapsible"
        type="button"
        class="group flex w-full items-start gap-3 rounded-[6px] text-left outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35"
        :aria-expanded="expanded"
        @click="toggleExpanded"
      >
        <span class="min-w-0 flex-1">
          <span v-if="title" class="block text-[15px] font-medium text-white">{{ title }}</span>
          <span v-if="description" class="mt-1 block text-xs leading-5 text-[#8f8f8f]">{{ description }}</span>
        </span>
        <span
          class="mt-0.5 shrink-0 text-[#8f8f8f] transition-transform duration-200 group-hover:text-white"
          :class="expanded ? 'rotate-180' : ''"
          aria-hidden="true"
        >
          <span class="icon-[mdi--chevron-down] text-[18px]"></span>
        </span>
      </button>

      <template v-else>
        <h2 v-if="title" class="text-[15px] font-medium text-white">{{ title }}</h2>
        <p v-if="description" class="text-xs leading-5 text-[#8f8f8f]">{{ description }}</p>
      </template>
    </header>

    <div v-show="!collapsible || expanded" class="min-w-0">
      <slot />
    </div>
  </section>
</template>
