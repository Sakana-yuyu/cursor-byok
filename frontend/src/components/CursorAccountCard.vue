<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import { showModal } from "@/composables/useModal";
import {
  disconnectCursorAccount,
  getCursorAccountStatus,
  startCursorAccountLogin,
} from "@/services/clientApi";
import { toUserError } from "@/state/appState";
import { computed, onMounted, onUnmounted, ref } from "vue";

const cursorAccountStatus = ref({
  state: "signed_out",
  authId: "",
  email: "",
  error: "",
});
const cursorAccountBusy = ref(false);
let cursorAccountTimer = null;

const cursorAccountSignedIn = computed(
  () => cursorAccountStatus.value.state === "signed_in",
);
const cursorAccountWaiting = computed(
  () => cursorAccountStatus.value.state === "waiting",
);
const cursorAccountStateText = computed(() => {
  if (cursorAccountSignedIn.value) return "已经登录";
  if (cursorAccountWaiting.value) return "等待浏览器登录";
  return "未连接";
});

async function showActionError(title, error) {
  await showModal({
    title,
    content: String(error || "服务错误").trim() || "服务错误",
  });
}

async function refreshCursorAccountStatus() {
  cursorAccountStatus.value = await getCursorAccountStatus();
}

async function handleCursorAccountLogin() {
  cursorAccountBusy.value = true;
  try {
    cursorAccountStatus.value = await startCursorAccountLogin();
  } catch (error) {
    await showActionError("登录失败", toUserError(error));
    await refreshCursorAccountStatus().catch(() => {});
  } finally {
    cursorAccountBusy.value = false;
  }
}

async function handleCursorAccountDisconnect() {
  const confirmed = await showModal({
    title: "退出登录",
    content: "只会退出 cursor-byok 中的 Cursor 账号，不会退出 Cursor 客户端。是否继续？",
    confirmText: "退出登录",
    cancelText: "取消",
    showCancel: true,
  });
  if (!confirmed) return;

  cursorAccountBusy.value = true;
  try {
    cursorAccountStatus.value = await disconnectCursorAccount();
  } catch (error) {
    await showActionError("退出登录失败", toUserError(error));
  } finally {
    cursorAccountBusy.value = false;
  }
}

onMounted(async () => {
  await refreshCursorAccountStatus().catch(() => {});
  cursorAccountTimer = window.setInterval(() => {
    if (cursorAccountWaiting.value) {
      void refreshCursorAccountStatus().catch(() => {});
    }
  }, 1500);
});

onUnmounted(() => {
  if (cursorAccountTimer) {
    window.clearInterval(cursorAccountTimer);
    cursorAccountTimer = null;
  }
});
</script>

<template>
  <Card>
    <div class="flex items-center justify-between gap-4">
      <div class="min-w-0">
        <div class="flex items-center gap-2">
          <h2 class="text-base font-medium text-white">Cursor 控制面账号</h2>
          <span
            class="rounded-full border border-[#3a3a3a] bg-[#202020] px-2 py-0.5 text-xs text-[#b8b8b8]"
          >
            {{ cursorAccountStateText }}
          </span>
        </div>
        <div
          v-if="cursorAccountSignedIn && (cursorAccountStatus.email || cursorAccountStatus.authId)"
          class="mt-1 truncate text-sm text-[#d0d0d0]"
        >
          {{ cursorAccountStatus.email || cursorAccountStatus.authId }}
        </div>
        <div class="mt-1 text-sm text-[#a3a3a3]">
          独立用于插件、Skills 和 MCP；不会改变 Cursor 客户端当前账号
        </div>
        <div v-if="cursorAccountWaiting" class="mt-1 text-sm text-[#d6a84b]">
          请在浏览器完成登录，完成后返回 Cursor 重新打开插件市场
        </div>
        <div
          v-if="cursorAccountStatus.error"
          class="mt-1 break-all text-sm text-[#e06c75]"
        >
          {{ cursorAccountStatus.error }}
        </div>
      </div>
      <Button
        v-if="cursorAccountSignedIn"
        :disabled="cursorAccountBusy"
        @click="handleCursorAccountDisconnect"
      >
        退出登录
      </Button>
      <Button
        v-else
        variant="primary"
        :disabled="cursorAccountBusy || cursorAccountWaiting"
        @click="handleCursorAccountLogin"
      >
        {{ cursorAccountWaiting ? "等待登录..." : "登录 Cursor" }}
      </Button>
    </div>
  </Card>
</template>
