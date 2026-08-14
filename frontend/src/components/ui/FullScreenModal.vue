<script setup>
import { useDialogFocus } from "@/composables/useDialogFocus";
import { computed, ref, toRef, useId } from "vue";

const props = defineProps({
  visible: { type: Boolean, default: false },
  title: { type: String, default: "" },
  maxWidth: { type: String, default: "1200px" },
});
const emit = defineEmits(["close"]);

const titleId = useId();
const dialogRef = ref(null);
const closeBtnRef = ref(null);

const { onKeydown } = useDialogFocus({
  visible: toRef(props, "visible"),
  dialogRef,
  initialFocusRef: closeBtnRef,
  onEscape: () => emit("close"),
});

const closeLabel = computed(() => "关闭");
</script>

<template>
  <Teleport to="body">
    <Transition name="fs-modal-mask">
      <div
        v-if="visible"
        class="fixed inset-0 z-999 flex items-center justify-center bg-black/60 p-3"
        @click.self="emit('close')"
      >
        <Transition name="fs-modal-content">
          <div
            v-if="visible"
            ref="dialogRef"
            class="relative flex max-h-[calc(100vh-24px)] w-full flex-col overflow-hidden rounded-[8px] border border-white/10 bg-[#1e1e1e] shadow-[0_25px_50px_-12px_rgba(0,0,0,0.6)] focus:outline-none"
            :style="{ maxWidth: maxWidth }"
            role="dialog"
            aria-modal="true"
            :aria-labelledby="titleId"
            tabindex="-1"
            @keydown="onKeydown"
          >
            <!-- 标题栏 -->
            <div class="flex shrink-0 items-center justify-between gap-4 border-b border-[#343434] px-5 py-3">
              <h2 :id="titleId" class="text-base font-medium text-white">{{ title }}</h2>
              <button
                ref="closeBtnRef"
                type="button"
                class="shrink-0 text-xl leading-none text-[#a3a3a3] transition-colors hover:text-white focus:outline-none"
                :aria-label="closeLabel"
                @click="emit('close')"
              >
                ✕
              </button>
            </div>
            <!-- 内容区（可滚动） -->
            <div class="min-h-0 flex-1 overflow-y-auto">
              <slot />
            </div>
          </div>
        </Transition>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.fs-modal-mask-enter-active,
.fs-modal-mask-leave-active {
  transition: opacity 0.2s ease;
}
.fs-modal-mask-enter-from,
.fs-modal-mask-leave-to {
  opacity: 0;
}
.fs-modal-content-enter-active,
.fs-modal-content-leave-active {
  transition: transform 0.2s ease, opacity 0.2s ease;
}
.fs-modal-content-enter-from,
.fs-modal-content-leave-to {
  transform: scale(0.97);
  opacity: 0;
}
</style>
