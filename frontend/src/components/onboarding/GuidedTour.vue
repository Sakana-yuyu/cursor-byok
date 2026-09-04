<script setup>
import { autoUpdate, computePosition, flip, offset, shift } from "@floating-ui/dom";
import { computed, onBeforeUnmount, onMounted, ref, watchPostEffect } from "vue";
import { useRoute, useRouter } from "vue-router";
import Button from "@/components/ui/Button.vue";
import { createGuidedTourController, provideGuidedTourController } from "@/composables/useGuidedTour";

const router = useRouter();
const route = useRoute();

// 引导步骤：target 一律用 data 属性选择器（改文案不破坏定位）；route 与当前页不同时
// 先跳页再等元素。用户入口在首页自行点击触发，不自动弹出。
const TOUR_STEPS = [
  {
    route: "/",
    center: true,
    title: "欢迎使用 Cursor助手",
    body: "它把你自己的 API 密钥接入 Cursor，无需官方订阅即可使用自定义模型。\n\n接下来用 1 分钟了解使用流程：配置模型 → 启动服务 → 在 Cursor 中使用。",
  },
  {
    route: "/",
    target: "[data-tour-nav='/model-config']",
    placement: "right",
    title: "第一步：配置模型",
    body: "在「模型」页添加供应商与模型，支持 OpenAI 兼容、Anthropic、Gemini 等接口。",
  },
  {
    route: "/model-config",
    target: "[data-tour-target='model-config-root']",
    placement: "top",
    title: "管理供应商与模型",
    body: "在这里添加供应商、拉取模型列表、编辑参数并测试连通性。配置好的模型会自动同步给 Cursor。",
  },
  {
    route: "/",
    target: "[data-tour-target='home-service-toggle']",
    placement: "bottom",
    title: "第二步：启动服务",
    body: "配置完成后回到首页启动本地服务，Cursor 的模型请求将由它转发到你的 API。",
  },
  {
    route: "/",
    target: "[data-tour-target='home-proxy-addr']",
    placement: "bottom-start",
    title: "本地代理地址",
    body: "服务运行后，本地代理监听此地址，Cursor 已被自动配置指向它。如果没有生效，用「更多 → 修复代理」一键修复。",
  },
  {
    route: "/",
    target: "[data-tour-nav='/settings']",
    placement: "right",
    title: "更多设置",
    body: "界面语言、调试日志、出站代理等偏好都在「设置」页调整。",
  },
  {
    route: "/",
    center: true,
    title: "引导完成",
    body: "现在去「模型」页添加你的第一个模型吧！之后随时可以回到首页，再次点击「使用引导」复习。",
  },
];

const controller = createGuidedTourController({
  steps: TOUR_STEPS,
  router: {
    push: (path) => router.push(path),
    currentPath: () => route.path,
  },
  resolveTarget: (selector) => document.querySelector(selector),
});
provideGuidedTourController(controller);

const state = controller.state;
const step = computed(() => controller.currentStep());
const bubbleRef = ref(null);
const bubbleStyle = ref({});
const highlightStyle = ref(null);

// spotlight 定位：镂空框跟随目标元素 rect，气泡复用 Tooltip 的 floating-ui 模式
// （offset + flip + shift + autoUpdate），滚动/缩放时同步。
watchPostEffect((cleanup) => {
  if (!state.active || state.mode !== "spotlight" || !state.targetEl || !step.value) return;
  const el = state.targetEl;
  const bubble = bubbleRef.value;
  if (!bubble) return;
  if (typeof el.scrollIntoView === "function") {
    el.scrollIntoView({ block: "center", behavior: "smooth" });
  }
  const update = () => {
    const rect = el.getBoundingClientRect();
    highlightStyle.value = {
      left: `${rect.left - 6}px`,
      top: `${rect.top - 6}px`,
      width: `${rect.width + 12}px`,
      height: `${rect.height + 12}px`,
    };
    computePosition(el, bubble, {
      placement: step.value.placement || "bottom",
      middleware: [offset(14), flip({ padding: 12 }), shift({ padding: 12 })],
    }).then(({ x, y }) => {
      bubbleStyle.value = { left: `${x}px`, top: `${y}px` };
    });
  };
  update();
  const stopAuto = autoUpdate(el, bubble, update);
  cleanup(() => stopAuto());
});

// Enter 下一步 / Esc 跳过。输入控件聚焦时不劫持按键（避免与 Modal 输入冲突）。
function handleKeydown(event) {
  if (!state.active) return;
  const target = event.target;
  if (target?.tagName === "INPUT" || target?.tagName === "TEXTAREA" || target?.isContentEditable) return;
  if (event.key === "Escape") {
    event.preventDefault();
    controller.skip();
  } else if (event.key === "Enter") {
    event.preventDefault();
    controller.next();
  }
}
onMounted(() => window.addEventListener("keydown", handleKeydown));
onBeforeUnmount(() => window.removeEventListener("keydown", handleKeydown));
</script>

<template>
  <Teleport to="body">
    <template v-if="state.active && step">
      <!-- spotlight：单个高亮框 + 巨幅 box-shadow 形成镂空遮罩，引导期间挡住页面交互 -->
      <div
        v-if="state.mode === 'spotlight' && highlightStyle"
        class="fixed z-[90000] rounded-[10px] border border-white/15 shadow-[0_0_0_100000px_rgba(0,0,0,0.62)]"
        :style="highlightStyle"
        aria-hidden="true"
      ></div>
      <!-- center：欢迎/完成/元素缺失降级时的全屏遮罩 -->
      <div v-else class="fixed inset-0 z-[90000] bg-black/60" aria-hidden="true"></div>

      <div
        ref="bubbleRef"
        role="dialog"
        aria-label="使用引导"
        class="fixed z-[90001] w-[340px] max-w-[calc(100vw-24px)] rounded-[10px] border border-[#3f3f3f] bg-[#1f1f1f] p-4 shadow-[0_12px_32px_rgba(0,0,0,0.5)]"
        :class="state.mode === 'center' ? 'left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2' : ''"
        :style="state.mode === 'spotlight' ? bubbleStyle : null"
      >
        <div class="mb-1 text-[11px] tabular-nums text-[#8a8a8a]">{{ state.index + 1 }} / {{ controller.stepCount }}</div>
        <h3 class="text-sm font-semibold text-white">{{ step.title }}</h3>
        <p class="mt-2 whitespace-pre-wrap text-[13px] leading-relaxed text-[#c9c9c9]">{{ step.body }}</p>
        <div class="mt-4 flex items-center justify-between">
          <button
            type="button"
            class="cursor-pointer text-xs text-[#8a8a8a] transition-colors duration-150 hover:text-[#e5e5e5]"
            @click="controller.skip()"
          >跳过引导</button>
          <div class="flex items-center gap-2">
            <Button v-if="state.index > 0" variant="default" :disabled="state.resolving" @click="controller.prev()">上一步</Button>
            <Button variant="primary" :disabled="state.resolving" @click="controller.next()">
              {{ state.index >= controller.stepCount - 1 ? "完成" : "下一步" }}
            </Button>
          </div>
        </div>
      </div>
    </template>
  </Teleport>
</template>
