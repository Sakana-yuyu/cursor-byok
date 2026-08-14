import { nextTick, onBeforeUnmount, watch } from "vue";

function getFocusable(root) {
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

function trapTab(event, dialogRef) {
  const focusable = getFocusable(dialogRef.value);
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
  } else if (active === last) {
    event.preventDefault();
    first.focus();
  }
}

/**
 * Focus trap + restore for modal dialogs. Mirrors Modal.vue behavior.
 */
export function useDialogFocus({ visible, dialogRef, initialFocusRef, onEscape }) {
  let lastFocused = null;

  function onKeydown(event) {
    if (event.key === "Escape") {
      event.preventDefault();
      onEscape?.();
    } else if (event.key === "Tab") {
      trapTab(event, dialogRef);
    }
  }

  watch(
    () => visible.value ?? visible,
    (val) => {
      const isOpen = Boolean(val);
      if (isOpen) {
        lastFocused = document.activeElement;
        nextTick(() => {
          const preferred = initialFocusRef?.value?.$el ?? initialFocusRef?.value ?? initialFocusRef;
          if (preferred && typeof preferred.focus === "function" && !preferred.disabled) {
            preferred.focus();
            if (document.activeElement === preferred) return;
          }
          dialogRef.value?.focus();
        });
      } else if (lastFocused && typeof lastFocused.focus === "function") {
        lastFocused.focus();
        lastFocused = null;
      }
    },
    { immediate: true },
  );

  onBeforeUnmount(() => {
    if (lastFocused && typeof lastFocused.focus === "function") {
      lastFocused.focus();
    }
    lastFocused = null;
  });

  return { onKeydown };
}
