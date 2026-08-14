<script setup>
import { MdEditor } from "md-editor-v3";
import Button from "@/components/ui/Button.vue";
import { useDialogFocus } from "@/composables/useDialogFocus";
import { computed, ref, toRef, useId } from "vue";

const props = defineProps({
  visible: { type: Boolean, default: false },
  title: { type: String, default: "编辑" },
  modelValue: { type: String, default: "" },
  saveBusy: { type: Boolean, default: false },
  saveText: { type: String, default: "保存" },
  placeholder: { type: String, default: "" },
});

const emit = defineEmits(["update:visible", "update:modelValue", "save", "cancel"]);

const titleId = useId();
const dialogRef = ref(null);
const saveBtnRef = ref(null);

const { onKeydown } = useDialogFocus({
  visible: toRef(props, "visible"),
  dialogRef,
  initialFocusRef: saveBtnRef,
  onEscape: handleCancel,
});

const editorContent = computed({
  get: () => props.modelValue,
  set: (value) => emit("update:modelValue", value),
});

function handleSave() {
  emit("save");
}

function handleCancel() {
  emit("cancel");
  emit("update:visible", false);
}
</script>

<template>
  <Teleport to="body">
    <Transition name="modal-mask">
      <div
        v-show="visible"
        class="modal-mask-layer fixed inset-0 z-[100000] flex items-center justify-center bg-black/50 p-4"
        @click.self="handleCancel"
      >
        <Transition name="modal-content">
          <div
            ref="dialogRef"
            v-show="visible"
            class="relative z-10 w-full max-w-[860px] overflow-hidden rounded-[8px] p-px shadow-[0_25px_50px_-12px_rgba(0,0,0,0.6)] focus:outline-none"
            style="background: linear-gradient(to bottom, #656565 0%, #3A3A3A 10px, #3A3A3A 100%);"
            role="dialog"
            aria-modal="true"
            :aria-labelledby="titleId"
            tabindex="-1"
            @click.stop
            @keydown="onKeydown"
          >
            <div class="flex max-h-[88vh] flex-col rounded-[7px] bg-[#292929] p-5">
              <h3 :id="titleId" class="mb-3 min-w-0 truncate text-base font-medium text-white" :title="title">
                {{ title }}
              </h3>

              <div class="min-h-0 flex-1 overflow-hidden rounded-[6px] border border-[#3b3b3b]">
                <MdEditor
                  v-model="editorContent"
                  theme="dark"
                  language="zh-CN"
                  :placeholder="String(placeholder ?? '')"
                  style="height: 440px;"
                />
              </div>

              <div class="mt-4 flex justify-end gap-2">
                <Button variant="default" @click="handleCancel">取消</Button>
                <Button ref="saveBtnRef" variant="primary" :disabled="saveBusy" @click="handleSave">
                  {{ saveBusy ? "保存中..." : saveText }}
                </Button>
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

/* 把 md-editor-v3 默认主题绿对齐到项目色 #10AD5D */
:deep(.md-editor-catalog-indicator),
:deep(.md-editor-catalog-active > span),
:deep(.md-editor-catalog-link span:hover) {
  color: #10AD5D;
}
:deep(.md-editor-catalog-indicator) {
  background-color: #10AD5D;
}
</style>
