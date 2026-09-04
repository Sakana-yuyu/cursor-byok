<script setup>
import { arrow, autoUpdate, computePosition, flip, offset, shift } from "@floating-ui/dom";
import { computed, onBeforeUnmount, onMounted, ref, watchPostEffect } from "vue";
import { useRoute, useRouter } from "vue-router";
import Button from "@/components/ui/Button.vue";
import { createGuidedTourController, provideGuidedTourController } from "@/composables/useGuidedTour";
import { appState } from "@/state/appState";

const router = useRouter();
const route = useRoute();

// 交互式引导：advanceOn="click" 的步骤由用户真实点击目标元素驱动前进（点击会放行，
// 触发元素的原有行为，如导航/启动服务）；其余步骤用「下一步」按钮。
// target 一律用 data 属性选择器（改文案不破坏定位）。入口在首页，由用户自行点击触发。
function buildTourSteps(serviceRunning) {
  const steps = [
    {
      route: "/",
      center: true,
      title: "欢迎使用 Cursor助手",
      body: "它把你自己的 API 密钥接入 Cursor，无需官方订阅即可使用自定义模型。\n\n接下来跟着高亮提示点击操作一遍，1 分钟完成上手。",
    },
    {
      route: "/",
      target: "[data-tour-nav='/model-config']",
      placement: "right",
      advanceOn: "click",
      title: "第一步：配置模型",
      body: "点击左侧「模型」，进入模型配置页。",
    },
    {
      route: "/model-config",
      target: "[data-tour-target='model-config-add']",
      placement: "bottom",
      title: "在这里添加模型",
      body: "点右上角「+」新增供应商和模型，填入 API 地址与密钥即可。现在点「下一步」继续。",
    },
  ];
  if (!serviceRunning) {
    steps.push({
      route: "/",
      target: "[data-tour-target='home-service-toggle']",
      placement: "bottom",
      advanceOn: "click",
      title: "第二步：启动服务",
      body: "点击「启动服务」，开启本地代理。启动成功后会自动进入下一步。",
    });
  }
  steps.push(
    {
      route: "/",
      target: "[data-tour-target='home-proxy-addr']",
      placement: "bottom-start",
      // 服务由冷启动到代理就绪可能超过默认 2s，放宽等待。
      elementTimeoutMs: 8000,
      title: "本地代理地址",
      body: "这是本地代理监听地址，Cursor 已被自动配置指向它，模型请求将从这里转发到你的 API。",
    },
    {
      route: "/",
      target: "[data-tour-nav='/settings']",
      placement: "right",
      advanceOn: "click",
      title: "更多设置",
      body: "点击「设置」，可以调整界面语言、调试日志等偏好。",
    },
    {
      center: true,
      title: "引导完成",
      body: "现在去「模型」页添加你的第一个模型吧！之后随时可以回到首页，再次点击「使用引导」复习。",
    },
  );
  return steps;
}

const controller = createGuidedTourController({
  steps: buildTourSteps(Boolean(appState.serviceRunning)),
  router: {
    push: (path) => router.push(path),
    currentPath: () => route.path,
  },
  resolveTarget: (selector) => document.querySelector(selector),
});
provideGuidedTourController(controller);

const state = controller.state;
const step = computed(() => controller.currentStep());
const isClickStep = computed(() => step.value?.advanceOn === "click");
const bubbleRef = ref(null);
const arrowRef = ref(null);
const bubbleStyle = ref({});
const arrowStyle = ref({});
const highlightStyle = ref(null);
const nudging = ref(false);
let nudgeTimer = null;

// spotlight 定位：镂空框跟随目标元素 rect，气泡复用 Tooltip 的 floating-ui 模式
// （offset + arrow + flip + shift + autoUpdate），滚动/缩放时同步。
watchPostEffect((cleanup) => {
  if (!state.active || state.mode !== "spotlight" || !state.targetEl || !step.value) return;
  const el = state.targetEl;
  const bubble = bubbleRef.value;
  const arrowEl = arrowRef.value;
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
    const middleware = [offset(14)];
    if (arrowEl) middleware.push(arrow({ element: arrowEl, padding: 8 }));
    middleware.push(flip({ padding: 12 }), shift({ padding: 12 }));
    computePosition(el, bubble, {
      placement: step.value.placement || "bottom",
      middleware,
    }).then(({ x, y, placement: resolved, middlewareData }) => {
      bubbleStyle.value = { left: `${x}px`, top: `${y}px` };
      const arrowData = middlewareData?.arrow;
      if (!arrowData) return;
      const staticSide = { top: "bottom", right: "left", bottom: "top", left: "right" }[resolved.split("-")[0]];
      arrowStyle.value = {
        left: arrowData.x != null ? `${arrowData.x}px` : "",
        top: arrowData.y != null ? `${arrowData.y}px` : "",
        [staticSide]: "-5px",
      };
    });
  };
  update();
  const stopAuto = autoUpdate(el, bubble, update);
  cleanup(() => stopAuto());
});

// 引导期间的点击治理（capture 阶段，先于页面 handler）：
// - 气泡内点击：不干预；
// - 目标元素内点击且本步 advanceOn="click"：放行真实点击并前进；
// - 其余页面区域点击：拦截（防止引导期间误操作页面）并抖动提醒"点这里"。
function handleDocumentClick(event) {
  if (!state.active) return;
  const bubble = bubbleRef.value;
  if (bubble && (bubble === event.target || bubble.contains(event.target))) return;
  if (state.mode !== "spotlight" || !state.targetEl) return; // center 遮罩自身已挡住页面
  const el = state.targetEl;
  const path = typeof event.composedPath === "function" ? event.composedPath() : null;
  const inside = path ? path.includes(el) : el.contains(event.target);
  if (!inside) {
    event.preventDefault();
    event.stopPropagation();
    nudging.value = false;
    // 强制重放抖动动画
    requestAnimationFrame(() => {
      nudging.value = true;
      if (nudgeTimer) clearTimeout(nudgeTimer);
      nudgeTimer = window.setTimeout(() => {
        nudging.value = false;
        nudgeTimer = null;
      }, 500);
    });
    return;
  }
  if (isClickStep.value) {
    // 放行：让元素的原有行为（导航/启动服务）发生，同时推进引导。
    controller.next();
  }
}

// Enter 下一步 / Esc 跳过。输入控件聚焦时不劫持按键（避免与 Modal 输入冲突）。
function handleKeydown(event) {
  if (!state.active) return;
  const target = event.target;
  if (target?.tagName === "INPUT" || target?.tagName === "TEXTAREA" || target?.isContentEditable) return;
  if (event.key === "Escape") {
    event.preventDefault();
    controller.skip();
  } else if (event.key === "Enter" && !isClickStep.value) {
    event.preventDefault();
    controller.next();
  }
}
onMounted(() => {
  window.addEventListener("keydown", handleKeydown);
  window.addEventListener("click", handleDocumentClick, true);
});
onBeforeUnmount(() => {
  window.removeEventListener("keydown", handleKeydown);
  window.removeEventListener("click", handleDocumentClick, true);
  if (nudgeTimer) clearTimeout(nudgeTimer);
});
</script>

<template>
  <Teleport to="body">
    <template v-if="state.active && step">
      <!-- spotlight：高亮框 + 巨幅 box-shadow 形成镂空遮罩。框本身 pointer-events-none，
           页面点击由 document capture 监听统一放行/拦截。 -->
      <div
        v-if="state.mode === 'spotlight' && highlightStyle"
        class="pointer-events-none fixed z-[90000] rounded-[10px] border-2 border-[#10AD5D] shadow-[0_0_0_100000px_rgba(0,0,0,0.65)]"
        :style="highlightStyle"
        aria-hidden="true"
      >
        <div class="absolute inset-0 rounded-[8px] tour-pulse"></div>
      </div>
      <!-- center：欢迎/完成/元素缺失降级时的全屏遮罩（自身拦截点击） -->
      <div v-else class="fixed inset-0 z-[90000] bg-black/60" aria-hidden="true"></div>

      <div
        ref="bubbleRef"
        role="dialog"
        aria-label="使用引导"
        class="fixed z-[90001] w-[340px] max-w-[calc(100vw-24px)] rounded-[10px] border border-[#3f3f3f] bg-[#1f1f1f] p-4 shadow-[0_12px_32px_rgba(0,0,0,0.5)]"
        :class="[state.mode === 'center' ? 'left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2' : '', state.mode === 'spotlight' && nudging ? 'tour-nudge' : '']"
        :style="state.mode === 'spotlight' ? bubbleStyle : null"
      >
        <div v-if="state.mode === 'spotlight'" ref="arrowRef" class="absolute h-[10px] w-[10px] rotate-45 border border-[#3f3f3f] bg-[#1f1f1f]" :style="arrowStyle" aria-hidden="true"></div>
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
            <template v-if="isClickStep">
              <span class="center-row gap-1 text-xs text-[#10AD5D]"><span class="icon-[mdi--cursor-default-click-outline] text-[14px]" />点击高亮位置继续</span>
            </template>
            <Button v-else variant="primary" :disabled="state.resolving" @click="controller.next()">
              {{ state.index >= controller.stepCount - 1 ? "完成" : "下一步" }}
            </Button>
          </div>
        </div>
      </div>
    </template>
  </Teleport>
</template>

<style scoped>
.tour-pulse {
  animation: tour-pulse 1.6s ease-out infinite;
}
@keyframes tour-pulse {
  0% {
    box-shadow: 0 0 0 0 rgba(16, 173, 93, 0.55);
  }
  70% {
    box-shadow: 0 0 0 14px rgba(16, 173, 93, 0);
  }
  100% {
    box-shadow: 0 0 0 0 rgba(16, 173, 93, 0);
  }
}
.tour-nudge {
  animation: tour-nudge 0.45s ease-in-out;
}
@keyframes tour-nudge {
  0%,
  100% {
    transform: translateX(0);
  }
  25% {
    transform: translateX(-6px);
  }
  50% {
    transform: translateX(5px);
  }
  75% {
    transform: translateX(-3px);
  }
}
/* 抖动动画（nudging 类只在 spotlight 模式绑定，避免与 center 气泡的居中 transform 冲突） */
</style>
