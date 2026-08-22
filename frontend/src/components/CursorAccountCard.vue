<script setup>
import ActionMenu from "@/components/ui/ActionMenu.vue";
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import Tooltip from "@/components/ui/Tooltip.vue";
import { useMessage } from "@/composables/useMessage";
import { showModal } from "@/composables/useModal";
import {
  beginCursorAccountLogin,
  cancelCursorAccountLogin,
  deleteCursorAccounts,
  executeCursorAccountRecoveryExport,
  executeCursorClientAccountSwitch,
  getCursorAccountLoginStatus,
  importCursorAccount,
  listCursorAccounts,
  prepareCursorAccountRecoveryExport,
  prepareCursorClientAccountSwitch,
  setCurrentCursorAccount,
  updateCursorAccountTags,
} from "@/services/clientApi";
import { notifyAccountSync } from "@/utils/accountSync";
import { toUserError } from "@/state/appState";
import { Browser } from "@wailsio/runtime";
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { useRouter } from "vue-router";

const props = defineProps({
  showControlCenterLink: { type: Boolean, default: true },
  variant: { type: String, default: "home" }, // home | panel
});

const CURSOR_ACCOUNT_CONTRIBUTOR_URL = "https://github.com/aike0210";
const message = useMessage();
const router = useRouter();
const accounts = ref([]);
const busy = ref(false);
const loginSessionId = ref("");
const loginTimer = ref(0);
const importDialog = ref(null);
const importValue = ref("");
const tagDialog = ref(null);
const tagValue = ref("");

const isPanel = computed(() => props.variant === "panel");

function accountInitial(email) {
  const text = String(email || "?").trim();
  return text.charAt(0).toUpperCase();
}

function accountHint(account) {
  const hint = String(account?.authIdHint || "").trim();
  return hint ? `ID ${hint}` : "";
}

function openControlCenter() {
  void router.push({ path: "/control-center", query: { tab: "accounts" } });
}

async function showActionError(title, error) {
  await showModal({
    title,
    content: toUserError(error),
    showCancel: false,
    confirmText: "知道了",
  });
}

async function reloadAccounts() {
  const listed = await listCursorAccounts();
  accounts.value = Array.isArray(listed) ? listed : [];
}

// 账号变更后仅广播刷新事件；余额同步由后端在账号变更钩子中后台执行，
// 前端不再重复触发全量上游查询（此前会触发 2-3 轮重复同步）。
async function afterAccountChanged() {
  await reloadAccounts();
  notifyAccountSync();
}

function clearImportDialog() {
  importValue.value = "";
  importDialog.value = null;
}

function clearTagDialog() {
  tagValue.value = "";
  tagDialog.value = null;
}

async function handleOAuthLogin() {
  if (busy.value) return;
  busy.value = true;
  try {
    const session = await beginCursorAccountLogin();
    loginSessionId.value = String(session?.sessionId || "");
    if (!loginSessionId.value) return;
    await pollLogin(loginSessionId.value);
  } catch (error) {
    await showActionError("登录 Cursor 失败", error);
  } finally {
    busy.value = false;
  }
}

async function pollLogin(sessionId) {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const status = await getCursorAccountLoginStatus(sessionId);
    if (status?.state === "signed_in") {
      loginSessionId.value = "";
      await afterAccountChanged();
      message.success("已添加 Cursor 账号");
      return;
    }
    if (status?.state === "error") {
      loginSessionId.value = "";
      throw new Error(status.error || "登录失败");
    }
    await new Promise((resolve) => {
      loginTimer.value = window.setTimeout(resolve, 400);
    });
  }
  loginSessionId.value = "";
  throw new Error("登录超时");
}

async function handleImport(mode) {
  if (mode === "local_cursor") {
    busy.value = true;
    try {
      await importCursorAccount({ mode });
      await afterAccountChanged();
      message.success("已从本地 Cursor 导入");
    } catch (error) {
      await showActionError("导入失败", error);
    } finally {
      busy.value = false;
    }
    return;
  }
  importDialog.value = mode;
  importValue.value = "";
}

async function confirmImport() {
  const mode = importDialog.value;
  const value = importValue.value;
  importValue.value = "";
  if (!mode) return;
  busy.value = true;
  try {
    if (mode === "token") {
      await importCursorAccount({ mode, token: value });
    } else {
      await importCursorAccount({ mode: "recovery_json", jsonContent: value });
    }
    clearImportDialog();
    await afterAccountChanged();
    message.success("已导入账号");
  } catch (error) {
    clearImportDialog();
    await showActionError("导入失败", error);
  } finally {
    busy.value = false;
  }
}

async function handleSetCurrent(account) {
  busy.value = true;
  try {
    await setCurrentCursorAccount(account.id);
    await afterAccountChanged();
  } catch (error) {
    await showActionError("设置当前账号失败", error);
  } finally {
    busy.value = false;
  }
}

async function handleSwitch(account) {
  if (busy.value) return;
  busy.value = true;
  try {
    const preparation = await prepareCursorClientAccountSwitch(account.id);
    const confirmed = await showModal({
      title: "切换 Cursor 账号",
      content: `将关闭 Cursor 并切换到 ${account.email || "所选账号"}。将备份 ${preparation?.backupFileCount ?? 0} 个状态文件。`,
      confirmText: "关闭并切换",
    });
    if (!confirmed) return;
    const result = await executeCursorClientAccountSwitch(preparation.confirmationToken);
    if (result?.state === "succeeded") {
      await afterAccountChanged();
      message.success("已切换并重新启动 Cursor");
      return;
    }
    await showActionError("切换 Cursor 账号失败", result?.errorCode || "切换失败");
  } catch (error) {
    await showActionError("切换 Cursor 账号失败", error);
  } finally {
    busy.value = false;
  }
}

async function handleExport(account) {
  const first = await showModal({
    title: "导出恢复包",
    content: "将把所选账号的凭据写入本地恢复文件，请确认目标设备可信。",
    confirmText: "继续",
  });
  if (!first) return;
  const second = await showModal({
    title: "再次确认导出",
    content: "恢复包包含登录凭据。确定导出？",
    confirmText: "导出",
  });
  if (!second) return;
  busy.value = true;
  try {
    const prepared = await prepareCursorAccountRecoveryExport({ accountIds: [account.id] });
    await executeCursorAccountRecoveryExport(prepared.confirmationToken);
    message.success("已导出恢复包");
  } catch (error) {
    await showActionError("导出失败", error);
  } finally {
    busy.value = false;
  }
}

function openTagDialog(account) {
  tagDialog.value = account;
  tagValue.value = Array.isArray(account.tags) ? account.tags.join(", ") : "";
}

async function confirmTags() {
  const account = tagDialog.value;
  const raw = tagValue.value;
  clearTagDialog();
  if (!account) return;
  const tags = raw.split(/[,，]/).map((item) => item.trim()).filter(Boolean);
  busy.value = true;
  try {
    await updateCursorAccountTags(account.id, tags);
    await reloadAccounts();
  } catch (error) {
    await showActionError("更新标签失败", error);
  } finally {
    busy.value = false;
  }
}

async function handleDelete(account) {
  if (account.isCurrent) {
    const others = accounts.value.filter((item) => item.id !== account.id);
    if (others.length > 0) {
      const confirmed = await showModal({
        title: "删除当前账号",
        content: `删除后将改用 ${others[0].email || "另一账号"} 作为当前账号。`,
        confirmText: "删除",
      });
      if (!confirmed) return;
      busy.value = true;
      try {
        await deleteCursorAccounts({ accountIds: [account.id], replacementId: others[0].id });
        await afterAccountChanged();
      } catch (error) {
        await showActionError("删除失败", error);
      } finally {
        busy.value = false;
      }
      return;
    }
    const confirmed = await showModal({
      title: "删除当前账号",
      content: "没有可替换的账号，将清除当前账号指针。",
      confirmText: "清除并删除",
    });
    if (!confirmed) return;
    busy.value = true;
    try {
      await deleteCursorAccounts({ accountIds: [account.id], clearCurrent: true });
      await afterAccountChanged();
    } catch (error) {
      await showActionError("删除失败", error);
    } finally {
      busy.value = false;
    }
    return;
  }
  const confirmed = await showModal({
    title: "删除账号",
    content: `确定删除 ${account.email || "该账号"}？`,
    confirmText: "删除",
  });
  if (!confirmed) return;
  busy.value = true;
  try {
    await deleteCursorAccounts({ accountIds: [account.id] });
    await afterAccountChanged();
  } catch (error) {
    await showActionError("删除失败", error);
  } finally {
    busy.value = false;
  }
}

onMounted(() => {
  void reloadAccounts().catch((error) => {
    void showActionError("读取账号失败", error);
  });
});

onBeforeUnmount(() => {
  if (loginTimer.value) window.clearTimeout(loginTimer.value);
  if (loginSessionId.value) {
    void cancelCursorAccountLogin(loginSessionId.value).catch(() => {});
  }
  clearImportDialog();
  clearTagDialog();
});
</script>

<template>
  <Card>
    <div class="flex flex-col gap-3">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="min-w-0">
          <div class="center-row gap-2">
            <h2 class="text-base font-semibold text-white">Cursor 账号</h2>
            <Tooltip content="用于 Skills / MCP 的独立 Cursor 身份，可保存多个账号。">
              <span class="icon-[mdi--information-outline] text-[15px] text-[#858585]" />
            </Tooltip>
          </div>
          <p v-if="!isPanel" class="mt-1 text-xs text-[#8a8a8a]">
            感谢
            <button type="button" class="text-[#7dd3a0] underline-offset-2 hover:underline" @click="Browser.OpenURL(CURSOR_ACCOUNT_CONTRIBUTOR_URL)">@aike0210</button>
            赞助开发
          </p>
          <p v-else class="mt-1 text-xs text-[#8a8a8a]">
            管理控制面身份与 Cursor 客户端登录；切换后会自动刷新各厂商余额。
          </p>
          <p class="mt-1 text-xs leading-5 text-[#8f8f8f]">
            Agent 内置模型执行通道仍需与真实 Cursor 请求协议完成比对验证，当前不会将账户授权用于第三方 API。
          </p>
        </div>
        <div class="center-row flex-wrap gap-2">
          <Button v-if="props.showControlCenterLink" variant="text" @click="openControlCenter">打开控制中心</Button>
          <ActionMenu>
            <template #trigger>
              <Button variant="primary" :disabled="busy || loginWaiting">
                <span v-if="loginWaiting" class="icon-[mdi--loading] animate-spin text-[14px]" />
                {{ loginWaiting ? "等待登录…" : "添加账号" }}
              </Button>
            </template>
            <template #items="{ close }">
              <button type="button" class="menu-item" role="menuitem" @click="() => { close(); void handleOAuthLogin(); }">
                <span class="icon-[mdi--login]" /> 官方 OAuth 登录
              </button>
              <button type="button" class="menu-item" role="menuitem" @click="() => { close(); void handleImport('local_cursor'); }">
                <span class="icon-[mdi--download-outline]" /> 从本地 Cursor 导入
              </button>
              <button type="button" class="menu-item" role="menuitem" @click="() => { close(); void handleImport('token'); }">
                <span class="icon-[mdi--key-outline]" /> 导入 Token
              </button>
              <button type="button" class="menu-item" role="menuitem" @click="() => { close(); void handleImport('recovery_json'); }">
                <span class="icon-[mdi--file-restore-outline]" /> 导入恢复包
              </button>
            </template>
          </ActionMenu>
        </div>
      </div>

      <div v-if="busy && !accounts.length" class="rounded-[8px] border border-[#343434] bg-[#252525]/70 px-3 py-6 text-center text-sm text-[#8a8a8a]">
        <span class="icon-[mdi--loading] animate-spin text-[18px]" /> 读取账号…
      </div>

      <div
        v-else-if="accounts.length === 0"
        class="flex flex-col items-center rounded-[8px] border border-dashed border-[#3f3f3f] bg-[#252525]/40 px-4 py-8 text-center"
      >
        <span class="icon-[mdi--account-plus-outline] text-[36px] text-[#4a4a4a]" aria-hidden="true" />
        <p class="mt-3 text-sm text-[#a3a3a3]">还没有保存的 Cursor 账号</p>
        <p class="mt-1 max-w-sm text-xs text-[#737373]">添加后可分别设置控制面当前账号，或写入 Cursor 客户端登录态。</p>
      </div>

      <div v-else class="flex flex-col gap-2">
        <div
          v-for="account in accounts"
          :key="account.id"
          class="rounded-[8px] border px-3 py-3 transition-colors"
          :class="account.isCurrent ? 'border-[#2f6b49]/60 bg-[#1f3a2c]/40' : 'border-[#343434] bg-[#252525]/70'"
        >
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div class="center-row min-w-0 gap-3">
              <div
                class="flex h-10 w-10 shrink-0 items-center justify-center rounded-full border text-sm font-semibold"
                :class="account.isCurrent ? 'border-[#2f6b49] bg-[#1f3a2c] text-[#7dd3a0]' : 'border-[#3f3f3f] bg-[#232323] text-[#d4d4d4]'"
              >
                {{ accountInitial(account.email) }}
              </div>
              <div class="min-w-0">
                <div class="center-row min-w-0 flex-wrap gap-2">
                  <span class="truncate text-sm font-medium text-white">{{ account.email || "未命名账号" }}</span>
                  <span v-if="account.isCurrent" class="rounded-full border border-[#2f6b49] px-2 py-0.5 text-[10px] text-[#7dd3a0]">控制面当前</span>
                </div>
                <div v-if="accountHint(account)" class="mt-0.5 text-[11px] text-[#737373]">{{ accountHint(account) }}</div>
                <div v-if="account.tags?.length" class="mt-2 flex flex-wrap gap-1">
                  <span v-for="tag in account.tags" :key="tag" class="rounded-full border border-[#3f3f3f] px-2 py-0.5 text-[10px] text-[#a3a3a3]">{{ tag }}</span>
                </div>
              </div>
            </div>

            <div class="center-row flex-wrap justify-end gap-1.5">
              <Button
                variant="primary"
                :disabled="busy"
                @click="handleSwitch(account)"
              >
                切换到 Cursor
              </Button>
              <Button variant="default" :disabled="busy || account.isCurrent" @click="handleSetCurrent(account)">
                设为当前
              </Button>
              <ActionMenu>
                <template #trigger>
                  <Button variant="text" :disabled="busy">更多</Button>
                </template>
                <template #items="{ close }">
                  <button type="button" class="menu-item" role="menuitem" @click="() => { close(); openTagDialog(account); }">
                    <span class="icon-[mdi--tag-outline]" /> 编辑标签
                  </button>
                  <button type="button" class="menu-item" role="menuitem" @click="() => { close(); void handleExport(account); }">
                    <span class="icon-[mdi--export]" /> 导出恢复包
                  </button>
                  <button type="button" class="menu-item text-[#fca5a5]" role="menuitem" @click="() => { close(); void handleDelete(account); }">
                    <span class="icon-[mdi--delete-outline]" /> 删除账号
                  </button>
                </template>
              </ActionMenu>
            </div>
          </div>
        </div>
      </div>
    </div>
  </Card>

  <Teleport to="body">
    <div
      v-if="importDialog"
      class="fixed inset-0 z-[100000] flex items-center justify-center bg-black/50 p-4"
      @click.self="clearImportDialog"
    >
      <div class="w-full max-w-[420px] rounded-[8px] border border-[#343434] bg-[#292929] p-5" role="dialog" aria-modal="true" :aria-label="importDialog === 'token' ? '导入 Token' : '导入恢复包'">
        <h3 class="mb-3 text-base font-medium text-white">{{ importDialog === "token" ? "导入 Token" : "导入恢复包" }}</h3>
        <p class="mb-3 text-sm text-[#a3a3a3]">{{ importDialog === "token" ? "粘贴 Cursor Token，提交后立即从内存清除。" : "粘贴恢复包 JSON，提交后立即从内存清除。" }}</p>
        <textarea
          v-model="importValue"
          class="mb-4 h-28 w-full rounded-[6px] border border-[#3f3f3f] bg-[#232323] p-2 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
          :placeholder="importDialog === 'token' ? 'Token' : '{ }'"
          autocomplete="off"
        />
        <div class="flex justify-end gap-2">
          <Button variant="default" @click="clearImportDialog">取消</Button>
          <Button variant="primary" :disabled="busy || !importValue.trim()" @click="confirmImport">导入</Button>
        </div>
      </div>
    </div>
    <div
      v-if="tagDialog"
      class="fixed inset-0 z-[100000] flex items-center justify-center bg-black/50 p-4"
      @click.self="clearTagDialog"
    >
      <div class="w-full max-w-[380px] rounded-[8px] border border-[#343434] bg-[#292929] p-5" role="dialog" aria-modal="true" aria-label="编辑标签">
        <h3 class="mb-3 text-base font-medium text-white">编辑标签</h3>
        <input
          v-model="tagValue"
          class="mb-4 h-9 w-full rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
          placeholder="用逗号分隔"
        />
        <div class="flex justify-end gap-2">
          <Button variant="default" @click="clearTagDialog">取消</Button>
          <Button variant="primary" :disabled="busy" @click="confirmTags">保存</Button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.menu-item {
  display: flex;
  width: 100%;
  cursor: pointer;
  align-items: center;
  gap: 0.5rem;
  padding: 0.375rem 0.75rem;
  text-align: left;
  font-size: 0.875rem;
  line-height: 1.25rem;
  color: #d4d4d4;
}
.menu-item:hover {
  background: rgb(255 255 255 / 0.05);
}
</style>
