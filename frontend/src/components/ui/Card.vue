<script setup></script>
<template>
  <div
    class="flex flex-col rounded-[8px] p-[1px]"
    style="background: linear-gradient(to bottom, #656565 0%, #3A3A3A 10px, #3A3A3A 100%);"
  >
    <div class="flex-1 rounded-[7px] bg-[#292929] p-4">
      <slot />
    </div>
  </div>
</template>
