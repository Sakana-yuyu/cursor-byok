<script setup>
const props = defineProps({
  enabled: { type: Boolean, default: false },
  disabled: { type: Boolean, default: false },
  busy: { type: Boolean, default: false },
  compact: { type: Boolean, default: false },
  label: { type: String, default: "" },
  description: { type: String, default: "" },
  enabledText: { type: String, default: "已开启" },
  disabledText: { type: String, default: "已关闭" },
  busyText: { type: String, default: "切换中..." },
});

const emit = defineEmits(["change"]);

function handleToggle() {
  if (props.disabled || props.busy) {
    return;
  }
  emit("change", !props.enabled);
}
</script>

<template>
  <div
    class="flex items-center justify-between gap-4"
    :class="compact ? 'py-0' : 'py-1'"
  >
    <div class="flex min-w-0 flex-col" :class="compact ? 'gap-[2px]' : 'gap-1'">
      <div :class="compact ? 'text-[12px]' : 'text-sm'" class="font-medium text-white">
        {{ label }}
      </div>
      <div
        v-if="description"
        :class="compact ? 'text-[11px] leading-[16px]' : 'text-xs'"
        class="text-[#a3a3a3]"
      >
        {{ description }}
      </div>
      <div
        :class="[
          compact ? 'text-[11px] leading-[16px]' : 'text-xs',
          enabled ? 'text-[#10AD5D]' : 'text-[#a3a3a3]',
        ]"
      >
        {{ busy ? busyText : enabled ? enabledText : disabledText }}
      </div>
    </div>

    <button
      type="button"
      role="switch"
      :aria-checked="enabled"
      :disabled="disabled || busy"
      class="relative inline-flex h-[22px] w-[40px] shrink-0 cursor-pointer rounded-full outline-none transition-all duration-200 ease-out disabled:cursor-not-allowed disabled:opacity-55 focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35"
      :class="enabled ? 'bg-[#10AD5D]' : 'bg-[rgba(255,255,255,0.22)]'"
      @click="handleToggle"
    >
      <span
        class="absolute left-[2px] top-[2px] inline-flex h-[18px] w-[18px] rounded-full bg-white shadow-[0_2px_5px_rgba(0,0,0,0.22)] transition-all duration-200 ease-out"
        :class="enabled ? 'translate-x-[18px]' : 'translate-x-0'"
      />
    </button>
  </div>
</template>