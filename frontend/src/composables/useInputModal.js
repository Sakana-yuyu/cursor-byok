import { reactive } from "vue";

export const inputModalState = reactive({
  visible: false,
  title: "提示",
  content: "",
  placeholder: "",
  value: "",
  _resolve: null,
});

export function resolveInputModal(ok) {
  const value = String(inputModalState.value ?? "").trim();
  inputModalState.visible = false;
  inputModalState._resolve?.(ok ? value : null);
  inputModalState._resolve = null;
  if (!ok) {
    inputModalState.value = "";
  }
}
