<script setup>
import { computed } from "vue";
import { probeRuntimeHealth, runtimeHealthState } from "@/services/runtimeHealth";

const messages = {
  offline: "当前网络离线，恢复联网后会自动重连",
  connecting: "正在连接本地服务…",
  reconnecting: "本地服务暂时不可用，正在自动恢复…",
  blocked: "本地服务连接失败，请重试或查看诊断信息",
  retry: "立即重试",
};

const visible = computed(() => (
  runtimeHealthState.noticeVisible
  && ["offline", "connecting", "reconnecting", "blocked"].includes(runtimeHealthState.phase)
));
const message = computed(() => String(messages[runtimeHealthState.phase] || messages.reconnecting));
const canRetry = computed(() => runtimeHealthState.phase !== "offline");
const traceId = computed(() => String(runtimeHealthState.lastFailure?.traceId || "").slice(0, 16));
const title = computed(() => traceId.value ? `${message.value} (${traceId.value})` : message.value);

function retryNow() {
  void probeRuntimeHealth({ immediate: true });
}
</script>

<template>
  <Transition name="runtime-health">
    <aside
      v-if="visible"
      class="fixed bottom-4 left-1/2 z-[10020] flex max-w-[min(560px,calc(100vw-32px))] -translate-x-1/2 items-center gap-3 rounded-xl border border-white/10 bg-[#191919]/95 px-4 py-3 text-sm text-[#d4d4d4] shadow-2xl backdrop-blur"
      role="status"
      aria-live="polite"
      :title="title"
    >
      <span class="h-2 w-2 shrink-0 rounded-full bg-amber-400" />
      <span class="min-w-0 flex-1">{{ message }}</span>
      <button
        v-if="canRetry"
        type="button"
        class="shrink-0 rounded-md border border-white/10 px-2.5 py-1 text-xs text-white transition hover:bg-white/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-400/70"
        @click="retryNow"
      >
        {{ messages.retry }}
      </button>
    </aside>
  </Transition>
</template>

<style scoped>
.runtime-health-enter-active,
.runtime-health-leave-active {
  transition: opacity 160ms ease, transform 160ms ease;
}
.runtime-health-enter-from,
.runtime-health-leave-to {
  opacity: 0;
  transform: translate(-50%, 8px);
}
</style>