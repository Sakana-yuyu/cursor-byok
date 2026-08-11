<script setup>
import Button from "@/components/ui/Button.vue";
import { safeErrorLogAttributes } from "@/utils/errorContract";
import { nextTick, onBeforeUnmount, ref, useId, watch } from "vue";

const props = defineProps({
  visible: { type: Boolean, default: false },
  title: { type: String, default: "提示" },
  content: { type: String, default: "" },
  confirmText: { type: String, default: "确定" },
  cancelText: { type: String, default: "取消" },
  showCancel: { type: Boolean, default: true },
  confirmDisabled: { type: Boolean, default: false },
  markdown: { type: Boolean, default: false },
});

// 稳定的唯一 id，供 aria-labelledby 指向标题元素。
const titleId = useId();
const dialogRef = ref(null);
const confirmBtnRef = ref(null);
// 打开对话框前的焦点元素，关闭后还原。用普通变量即可，不需响应式。
let lastFocused = null;

const renderedHtml = ref("");
const markdownReady = ref(false);
let markdownRenderVersion = 0;

watch(
  () => [props.visible, props.markdown, props.content],
  async ([visible, markdown, content]) => {
    const renderVersion = ++markdownRenderVersion;
    renderedHtml.value = "";
    markdownReady.value = false;
    if (!visible || !markdown || !content) return;

    try {
      const { marked } = await import("marked");
      marked.setOptions({ breaks: true, gfm: true });
      const html = marked(content);
      if (renderVersion !== markdownRenderVersion) return;
      renderedHtml.value = html;
      markdownReady.value = true;
    } catch (err) {
      if (renderVersion !== markdownRenderVersion) return;
      console.error("Markdown parse error:", safeErrorLogAttributes(err, { operation: "modal.renderMarkdown" }));
    }
  },
  { immediate: true },
);

const emit = defineEmits(["update:visible", "confirm", "cancel"]);

function handleConfirm() {
  emit("confirm");
  emit("update:visible", false);
}

function handleCancel() {
  emit("cancel");
  emit("update:visible", false);
}

function onMaskClick() {
  handleCancel();
}

// 取得对话框内当前可聚焦元素列表，用于焦点陷阱的边界判定。
// 排除 disabled 与 display:none 的元素，边界随 showCancel 动态变化。
function getFocusable() {
  const root = dialogRef.value;
  if (!root) return [];
  const nodes = root.querySelectorAll(
    'a[href], button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
  );
  return Array.from(nodes).filter((el) => {
    if (el.hasAttribute("disabled")) return false;
    if (el.getAttribute("tabindex") === "-1") return false;
    return el.offsetParent !== null || el === document.activeElement;
  });
}

function trapTab(event) {
  const focusable = getFocusable();
  if (focusable.length === 0) {
    event.preventDefault();
    dialogRef.value?.focus();
    return;
  }
  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  const active = document.activeElement;
  if (event.shiftKey) {
    if (active === first || !dialogRef.value?.contains(active)) {
      event.preventDefault();
      last.focus();
    }
  } else {
    if (active === last) {
      event.preventDefault();
      first.focus();
    }
  }
}

function onKeydown(event) {
  if (event.key === "Escape") {
    event.preventDefault();
    handleCancel();
  } else if (event.key === "Tab") {
    trapTab(event);
  }
}

watch(
  () => props.visible,
  (val) => {
    if (val) {
      lastFocused = document.activeElement;
      nextTick(() => {
        // 优先聚焦确认按钮；被禁用或拿不到时退到对话框容器本身。
        // Button 是组件，其根 button 元素挂在实例的 $el 上。
        const confirmEl = confirmBtnRef.value?.$el ?? confirmBtnRef.value;
        if (confirmEl && typeof confirmEl.focus === "function" && !confirmEl.disabled) {
          confirmEl.focus();
          if (document.activeElement === confirmEl) return;
        }
        dialogRef.value?.focus();
      });
    } else {
      if (lastFocused && typeof lastFocused.focus === "function") {
        lastFocused.focus();
      }
      lastFocused = null;
    }
  },
);

onBeforeUnmount(() => {
  if (lastFocused && typeof lastFocused.focus === "function") {
    lastFocused.focus();
  }
  lastFocused = null;
});
</script>

<template>
  <Teleport to="body">
    <Transition name="modal-mask">
      <div
        v-show="visible"
        class="modal-mask-layer fixed inset-0 z-[100000] flex items-center justify-center bg-black/50 p-4 "
        @click.self="onMaskClick"
      >
        <Transition name="modal-content">
          <div
            ref="dialogRef"
            v-show="visible"
            class="relative z-10 w-full max-w-[360px] overflow-hidden rounded-[8px] p-px shadow-[0_25px_50px_-12px_rgba(0,0,0,0.6)] focus:outline-none"
            style="background: linear-gradient(to bottom, #656565 0%, #3A3A3A 10px, #3A3A3A 100%);"
            role="dialog"
            aria-modal="true"
            :aria-labelledby="titleId"
            tabindex="-1"
            @click.stop
            @keydown="onKeydown"
          >
            <div class="rounded-[7px] bg-[#292929] p-5">
              <h3 :id="titleId" class="mb-3 text-base font-medium text-white">
                {{ title }}
              </h3>
              <!-- Markdown 解析器按需加载；加载或失败时保留原始文本，避免用户看到空白内容。 -->
              <div
                v-if="markdown && markdownReady"
                class="modal-md mb-5 max-h-[55vh] overflow-y-auto text-sm leading-relaxed text-[#a3a3a3]"
                v-html="renderedHtml"
              />
              <p v-else class="mb-5 max-h-[55vh] overflow-y-auto whitespace-pre-wrap text-sm leading-relaxed text-[#a3a3a3]">
                {{ content }}
              </p>
              <div class="flex justify-end gap-2">
                <Button v-if="showCancel" variant="default" @click="handleCancel">{{ cancelText }}</Button>
                <Button ref="confirmBtnRef" variant="primary" :disabled="confirmDisabled" @click="handleConfirm">{{ confirmText }}</Button>
              </div>
            </div>
          </div>
        </Transition>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.modal-mask-enter-active,
.modal-mask-leave-active {
  transition: opacity 0.25s ease, backdrop-filter 0.25s ease;
}
.modal-mask-enter-from,
.modal-mask-leave-to {
  opacity: 0;
  backdrop-filter: blur(0);
}

.modal-content-enter-active,
.modal-content-leave-active {
  transition: all 0.25s cubic-bezier(0.34, 1.56, 0.64, 1);
}
.modal-content-enter-from,
.modal-content-leave-to {
  opacity: 0;
  transform: scale(0.9) translateY(-10px);
}

/* Markdown 渲染样式 */
.modal-md { color: #a3a3a3; }
.modal-md h1, .modal-md h2 { color: #e5e5e5; font-size: 13px; font-weight: 600; margin: 12px 0 6px; padding-bottom: 4px; border-bottom: 1px solid #3a3a3a; }
.modal-md h3 { color: #d4d4d4; font-size: 12px; font-weight: 600; margin: 10px 0 4px; }
.modal-md h1:first-child, .modal-md h2:first-child { margin-top: 0; }
.modal-md ul { margin: 4px 0 8px; padding-left: 16px; list-style: disc; }
.modal-md li { margin: 3px 0; line-height: 1.5; }
.modal-md strong { color: #e5e5e5; font-weight: 600; }
.modal-md code { background: rgba(110,231,165,0.1); color: #6ee7a5; border-radius: 3px; padding: 1px 4px; font-size: 11px; font-family: ui-monospace, monospace; }
.modal-md blockquote { border-left: 2px solid #444; margin: 8px 0 4px; padding: 4px 10px; color: #777; font-size: 11px; }
.modal-md hr { border: none; border-top: 1px solid #3a3a3a; margin: 10px 0; }
.modal-md p { margin: 4px 0; }
</style>
