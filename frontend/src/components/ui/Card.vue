<script setup></script>
<template>
  <div
    class="flex flex-col rounded-[8px] p-[1px]"
    style="background: #343434;"
  >
    <div class="flex-1 rounded-[7px] bg-[var(--bg-card,#232323)] p-4">
      <slot />
    </div>
  </div>
</template>
