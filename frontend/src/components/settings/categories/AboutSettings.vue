<script setup>
import SettingsSection from "@/components/settings/SettingsSection.vue";

// 「关于」页为纯展示页，声明 autosave 以与其余分类页 props 形态保持一致。
defineProps({
  autosave: {
    type: Object,
    required: true,
  },
});

// 致谢条目：本项目依赖或集成的外部服务/资源提供方。
// leokun：原项目（cursor-byok）作者，提供 commit message 生成服务（tab.leokun.cn）
//         与代码补全能力，本仓库在其基础上新增了「来源可选」。
// Codex-X：默认提示词模板来源（MIT，copyright 2026 yynxxxxx）。
const credits = [
  {
    name: "leokun（leookun）",
    detail: "原项目 cursor-byok 作者。提供 Git 提交信息生成服务（tab.leokun.cn）与代码补全能力，本项目在其基础上新增了本地模型 / Cursor 直连等可选来源。",
    link: "https://github.com/leookun/cursor-byok",
  },
  {
    name: "Codex-X（yynxxxxx）",
    detail: "默认提示词模板来源（MIT License，copyright 2026 yynxxxxx）。本仓库仅消费其公开示例目录，不执行或分发其代码。",
    link: "https://github.com/yynxxxxx/Codex-X",
  },
  {
    name: "Oh My Pi",
    detail: "供应商多时间窗口额度、运行诊断、冷却状态与多厂商协议兼容行为参考（MIT License）。本项目使用原生 Go/Vue 实现，并保留完整第三方许可声明。",
    link: "https://github.com/can1357/oh-my-pi",
    noticeLink: "/THIRD_PARTY_NOTICES.md",
  },
];
</script>

<template>
  <SettingsSection
    title="关于"
    description="致谢与开源信息。"
  >
    <ul class="space-y-3">
      <li
        v-for="item in credits"
        :key="item.name"
        class="rounded-[6px] border border-[#3f3f3f] bg-[#1b1b1b] px-4 py-3"
      >
        <div class="flex items-center justify-between gap-3">
          <span class="text-sm font-medium text-white">{{ item.name }}</span>
          <span class="flex min-w-0 flex-wrap items-center justify-end gap-x-3 gap-y-1">
            <a
              v-if="item.link"
              :href="item.link"
              target="_blank"
              rel="noopener noreferrer"
              class="text-xs text-[#10d06f] hover:underline"
            >
              {{ item.link }}
            </a>
            <a
              v-if="item.noticeLink"
              :href="item.noticeLink"
              target="_blank"
              rel="noopener noreferrer"
              class="text-xs text-[#a3a3a3] hover:text-white hover:underline"
            >
              第三方许可
            </a>
          </span>
        </div>
        <p class="mt-1.5 text-xs leading-5 text-[#8f8f8f]">
          {{ item.detail }}
        </p>
      </li>
    </ul>
  </SettingsSection>
</template>
