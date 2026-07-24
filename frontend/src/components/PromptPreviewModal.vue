<script setup>
import Button from "@/components/ui/Button.vue";

const props = defineProps({
  visible: { type: Boolean, default: false },
  templateName: { type: String, default: "" },
  repo: { type: String, default: "" },
  refName: { type: String, default: "" },
  content: { type: String, default: "" },
});

const emit = defineEmits(["close"]);
</script>

<template>
  <Teleport to="body">
    <Transition name="prompt-preview-mask">
      <div
        v-if="visible"
        class="fixed inset-0 z-999 flex items-center justify-center bg-black/60 p-4"
        @click.self="emit('close')"
      >
        <div
          class="relative flex max-h-[calc(100vh-32px)] w-full max-w-[860px] flex-col overflow-hidden rounded-[8px] border border-white/10 bg-[#292929] shadow-[0_25px_50px_-12px_rgba(0,0,0,0.6)]"
          role="dialog"
          aria-modal="true"
          aria-labelledby="prompt-preview-title"
        >
          <div class="flex items-start justify-between gap-4 border-b border-white/10 px-5 py-4">
            <div class="min-w-0">
              <h2 id="prompt-preview-title" class="text-base font-medium text-white">查看提示词</h2>
              <div class="mt-2 grid gap-x-6 gap-y-1 text-xs text-[#a3a3a3] sm:grid-cols-3">
                <div class="min-w-0 truncate" :title="templateName">模板：{{ templateName || "未选择" }}</div>
                <div class="min-w-0 truncate" :title="repo">仓库：{{ repo || "未设置" }}</div>
                <div class="min-w-0 truncate" :title="refName">Ref：{{ refName || "未设置" }}</div>
              </div>
            </div>
            <button
              type="button"
              class="shrink-0 text-xl leading-none text-[#a3a3a3] transition-colors hover:text-white focus:outline-none"
              aria-label="关闭提示词预览"
              @click="emit('close')"
            >
              ×
            </button>
          </div>
          <pre class="m-5 min-h-0 overflow-auto whitespace-pre-wrap break-words rounded-[6px] border border-white/10 bg-black/20 p-4 text-xs leading-relaxed text-[#e5e5e5]">{{ content || "暂无缓存提示词" }}</pre>
          <div class="flex justify-end border-t border-white/10 px-5 py-3">
            <Button variant="default" @click="emit('close')">关闭</Button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.prompt-preview-mask-enter-active,
.prompt-preview-mask-leave-active {
  transition: opacity 0.2s ease;
}
.prompt-preview-mask-enter-from,
.prompt-preview-mask-leave-to {
  opacity: 0;
}
</style>