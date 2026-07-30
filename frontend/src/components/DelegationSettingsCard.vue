<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import Select from "@/components/ui/Select.vue";
import Switch from "@/components/ui/Switch.vue";
import { useMessage } from "@/composables/useMessage";
import { appState, persistUserConfig, toUserError } from "@/state/appState";
import { ref } from "vue";

const message = useMessage();
const saving = ref(false);
const error = ref("");
const modeOptions = [
  { value: "auto", label: "自动选择" },
  { value: "cursor", label: "Cursor 子会话" },
  { value: "local", label: "本地子代理" },
];
const permissionGroups = [
  { key: "read", label: "读取与搜索", tools: ["Read", "Grep", "Glob", "Ls", "ReadLints"] },
  { key: "write", label: "写入与编辑", tools: ["Write", "PatchEdit", "Delete"] },
  { key: "shell", label: "终端命令", tools: ["Shell", "AwaitShell", "WriteShellStdin", "ForceBackgroundShell"] },
  { key: "mcp", label: "MCP 工具", tools: ["CallMcpTool", "ListMcpResources", "FetchMcpResource"] },
  { key: "task", label: "继续委派", tools: ["Task"] },
];

function addGroup() {
  const index = appState.delegation.groups.length + 1;
  appState.delegation.groups.push({
    id: `delegation-group-${Date.now()}`,
    name: `委派模型组 ${index}`,
    enabled: true,
    modelIDs: [],
    defaultModelID: "",
    executionMode: "auto",
    toolPermissions: {},
  });
}

function removeGroup(index) {
  appState.delegation.groups.splice(index, 1);
}

function toggleModel(group, modelID, enabled) {
  const next = new Set(group.modelIDs || []);
  if (enabled) next.add(modelID);
  else next.delete(modelID);
  group.modelIDs = [...next];
  if (!group.modelIDs.includes(group.defaultModelID)) group.defaultModelID = group.modelIDs[0] || "";
}

function permissionEnabled(group, permission) {
  return permission.tools.every((tool) => group.toolPermissions?.[tool] !== false);
}

function togglePermission(group, permission, enabled) {
  const next = { ...(group.toolPermissions || {}) };
  for (const tool of permission.tools) {
    if (enabled) delete next[tool];
    else next[tool] = false;
  }
  group.toolPermissions = next;
}

async function save() {
  if (saving.value) return;
  saving.value = true;
  error.value = "";
  try {
    const result = await persistUserConfig();
    if (!result.ok) throw new Error(result.error || "保存失败");
    message.success("保存成功");
  } catch (saveError) {
    error.value = toUserError(saveError);
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <Card>
    <div class="min-w-0 space-y-3">
      <div class="flex min-w-0 items-start justify-between gap-3">
        <div class="min-w-0">
          <h3 class="text-sm font-medium text-white">Multitask 委派</h3>
          <div class="mt-1 text-xs leading-5 text-[#858585]">使用已配置模型并行处理子任务，失败的子任务不会阻塞其他任务。</div>
        </div>
        <Switch compact label="" enabled-text="" disabled-text="" :enabled="appState.delegation.enabled" :disabled="saving" aria-label="启用 Multitask 委派" @change="(value) => (appState.delegation.enabled = value)" />
      </div>

      <div class="flex items-end justify-between gap-3">
        <label class="min-w-0 flex-1 text-xs text-[#a3a3a3]">
          最大并发数
          <input v-model.number="appState.delegation.maxConcurrency" type="number" min="1" :disabled="saving" class="mt-1 h-8 w-full rounded-[6px] border border-white/10 bg-black/20 px-2 text-xs text-white outline-none focus:border-[#10AD5D]" />
        </label>
        <Button variant="default" :disabled="saving" @click="addGroup">新增模型组</Button>
      </div>

      <div v-if="!appState.delegation.groups.length" class="rounded-[6px] border border-dashed border-[#444] px-3 py-4 text-xs text-[#858585]">尚未配置委派模型组，请先在模型配置中添加模型。</div>
      <div v-for="(group, groupIndex) in appState.delegation.groups" :key="group.id" class="min-w-0 space-y-3 rounded-[6px] border border-white/10 bg-black/15 p-3">
        <div class="flex min-w-0 items-center gap-2">
          <input v-model="group.name" :disabled="saving" class="h-8 min-w-0 flex-1 rounded-[6px] border border-white/10 bg-black/20 px-2 text-xs text-white" aria-label="委派模型组名称" />
          <Switch compact label="启用" :enabled="group.enabled" :disabled="saving" @change="(value) => (group.enabled = value)" />
          <button type="button" class="shrink-0 text-xs text-[#fca5a5] hover:text-white disabled:opacity-50" title="删除模型组" :disabled="saving" @click="removeGroup(groupIndex)">删除</button>
        </div>
        <div class="grid gap-2 sm:grid-cols-2">
          <Select v-model="group.executionMode" :options="modeOptions" aria-label="委派执行模式" :disabled="saving" />
          <Select v-model="group.defaultModelID" :options="appState.modelAdapters.filter((item) => group.modelIDs.includes(item.id)).map((item) => ({ value: item.id, label: item.displayName || item.modelID }))" aria-label="默认委派模型" :disabled="saving || !group.modelIDs.length" />
        </div>
        <div class="grid gap-2 sm:grid-cols-2">
          <label v-for="adapter in appState.modelAdapters" :key="adapter.id" class="flex min-w-0 items-center gap-2 text-xs text-[#cfcfcf]">
            <input type="checkbox" :checked="group.modelIDs.includes(adapter.id)" :disabled="saving" @change="(event) => toggleModel(group, adapter.id, event.target.checked)" />
            <span class="min-w-0 truncate" :title="adapter.displayName || adapter.modelID">{{ adapter.displayName || adapter.modelID }}</span>
          </label>
        </div>
        <div class="border-t border-white/10 pt-2">
          <div class="mb-2 text-[11px] text-[#858585]">工具权限</div>
          <div class="grid gap-2 sm:grid-cols-2">
            <Switch v-for="permission in permissionGroups" :key="permission.key" compact :label="permission.label" :enabled="permissionEnabled(group, permission)" :disabled="saving" @change="(value) => togglePermission(group, permission, value)" />
          </div>
        </div>
      </div>

      <div v-if="error" class="break-words rounded-[6px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-xs text-[#fca5a5]">{{ error }}</div>
      <div class="flex justify-end">
        <Button variant="primary" :disabled="saving" @click="save">{{ saving ? "保存中..." : "保存配置" }}</Button>
      </div>
    </div>
  </Card>
</template>
