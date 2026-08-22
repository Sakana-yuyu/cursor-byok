export const ACCOUNT_SYNC_EVENT = "cursor-byok:account-sync";

export function notifyAccountSync() {
  if (typeof window === "undefined") return;
  window.dispatchEvent(new CustomEvent(ACCOUNT_SYNC_EVENT));
}

export function onAccountSync(listener) {
  if (typeof window === "undefined") return () => {};
  window.addEventListener(ACCOUNT_SYNC_EVENT, listener);
  return () => window.removeEventListener(ACCOUNT_SYNC_EVENT, listener);
}
