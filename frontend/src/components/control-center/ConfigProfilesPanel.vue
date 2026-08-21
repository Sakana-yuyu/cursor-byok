<script setup>
import Button from "@/components/ui/Button.vue";
import { deleteConfigProfile, exportConfigProfile, importConfigProfile, listConfigProfiles, prepareConfigProfileApply, executeConfigProfileApply, previewConfigProfile, saveCurrentConfigProfile } from "@/services/clientApi";
import { useMessage } from "@/composables/useMessage";
import { onMounted, ref } from "vue";

const message = useMessage();
const profiles = ref([]);
const name = ref("当前配置");
const domains = ref(["models", "routing"]);
const preview = ref(null);
const importInput = ref(null);

const DOMAIN_OPTIONS = [
  { id: "models", label: "模型" },
  { id: "model_groups", label: "模型组" },
  { id: "routing", label: "路由" },
  { id: "delegation", label: "委派" },
  { id: "skills_mcp", label: "Skills/MCP" },
  { id: "computer_use", label: "ComputerUse" },
  { id: "appearance", label: "界面" },
];

async function load() {
  try {
    profiles.value = (await listConfigProfiles()) || [];
  } catch (error) {
    message.error(error?.message || "加载档案失败");
  }
}

function toggleDomain(id) {
  if (domains.value.includes(id)) {
    domains.value = domains.value.filter((item) => item !== id);
    return;
  }
  domains.value = [...domains.value, id];
}

async function save() {
  try {
    await saveCurrentConfigProfile({ name: name.value, domains: domains.value });
    message.success("已保存无凭据档案");
    await load();
  } catch (error) {
    message.error(error?.message || "保存失败");
  }
}

async function showPreview(id) {
  try {
    preview.value = await previewConfigProfile(id);
  } catch (error) {
    message.error(error?.message || "预览失败");
  }
}

async function apply(id) {
  try {
    const prepared = await prepareConfigProfileApply(id);
    if (!prepared.preview?.canApply) {
      message.error("凭据绑定缺失或歧义，无法应用");
      return;
    }
    await executeConfigProfileApply(prepared.confirmationToken);
    message.success("档案已应用");
  } catch (error) {
    message.error(error?.message || "应用失败");
  }
}

async function exportProfile(id) {
  try {
    const result = await exportConfigProfile(id);
    message.success(`已导出 ${result.path}`);
  } catch (error) {
    message.error(error?.message || "导出失败");
  }
}

async function remove(id) {
  try {
    await deleteConfigProfile(id);
    await load();
  } catch (error) {
    message.error(error?.message || "删除失败");
  }
}

function openImportPicker() {
  importInput.value?.click();
}

async function onImportSelected(event) {
  const file = event.target.files?.[0];
  event.target.value = "";
  if (!file) return;
  if (file.size > 1024 * 1024) {
    message.error("JSON 文件不能超过 1 MiB");
    return;
  }
  try {
    const content = await file.text();
    preview.value = await importConfigProfile(content);
    message.success(`已导入档案 ${preview.value?.profile?.name || ""}`.trim());
    await load();
  } catch (error) {
    message.error(error?.message || "导入失败");
  }
}

onMounted(() => {
  void load();
});
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto">
    <input v-model="name" class="rounded-[6px] border border-[#343434] bg-[#1f1f1f] px-2 py-1 text-sm text-white" />
    <div class="flex flex-wrap gap-2 text-xs">
      <button
        v-for="item in DOMAIN_OPTIONS"
        :key="item.id"
        type="button"
        class="rounded-[6px] border px-2 py-1"
        :class="domains.includes(item.id) ? 'border-[#2f6b49] bg-[#1f3a2c] text-white' : 'border-[#343434] text-[#a3a3a3]'"
        @click="toggleDomain(item.id)"
      >
        {{ item.label }}
      </button>
    </div>
    <Button variant="primary" @click="save">保存当前配置为档案</Button>
    <input ref="importInput" type="file" accept="application/json,.json" class="hidden" @change="onImportSelected" />
    <Button @click="openImportPicker">导入 JSON 档案</Button>
    <div v-for="profile in profiles" :key="profile.id" class="rounded-[8px] border border-[#343434] bg-[#292929] p-3 text-xs">
      <div class="text-sm text-white">{{ profile.name }}</div>
      <div class="mt-2 flex flex-wrap gap-2">
        <Button @click="showPreview(profile.id)">预览</Button>
        <Button variant="primary" @click="apply(profile.id)">应用</Button>
        <Button @click="exportProfile(profile.id)">导出</Button>
        <Button variant="danger" @click="remove(profile.id)">删除</Button>
      </div>
    </div>
    <div v-if="preview" class="text-xs text-[#a3a3a3]">
      可应用 {{ preview.canApply ? "是" : "否" }} · 变更 {{ preview.changes?.length || 0 }}
    </div>
  </div>
</template>
