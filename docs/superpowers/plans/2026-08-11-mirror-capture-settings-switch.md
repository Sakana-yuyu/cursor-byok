# 镜像记录调试开关 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在设置 → 高级设置加一个"镜像记录官方请求（调试）"开关，测试时一键开启/关闭 mirrorCapture（写入 `history/_debug/mirror/official.raw.jsonl`）。测试专用、不进正式发布文案。

**Architecture:** 纯前端改动（后端 `Config.MirrorCapture` 字段、`Manager` 热加载、MITM 镜像记录均已就绪）。开关保存 `mirrorCapture.enabled`（hosts 保留 config.yaml 现有值/默认列表，不进 UI）。关键：前端配置数据流三处（`normalizeConfig` 白名单、`buildConfigPayload` 透传、appState 状态/`applyConfigToState`）必须同步加 `mirrorCapture`，否则保存任意其他设置会把 mirrorCapture 重置为关闭。

**Tech Stack:** Vue 3 + Vite 前端（frontend/），Wails bindings（`loadUserConfig`/`saveUserConfig`），静态 i18n scanner（`frontend/plugins/static-i18n-plugin.js`），zh-CN 为源语言。

## Global Constraints

- 开关只控制 `mirrorCapture.enabled`；**hosts 不暴露 UI**（默认列表 openai/anthropic/gemini 或 config.yaml 手改值，保存时原样保留）。
- 不新增任何 UI 文案的英文硬编码：.vue 里写中文源文案，跑 `yarn i18n:scan` 生成 key，4 个 locale 全部填非空翻译。
- 不更新 README、发布文案、release notes（测试专用，不正式发布）。
- 不做 hosts 编辑 UI、不做对比页。
- 验证命令：`yarn lint`、`yarn i18n:scan`、`yarn build`（--scan 模式）必须全绿；无后端改动，无需重跑 go test。

---

### Task 1: 前端配置数据流接入 mirrorCapture（防清零陷阱）

**Files:**
- Modify: `frontend/src/utils/configNormalize.js`（`normalizeConfig` 约 :163-189）
- Modify: `frontend/src/state/appState.js`（`buildConfigPayload` :214-235、`applyConfigToState` :257-278、状态初始化约 :546、新增导出函数）

**Interfaces:**
- Produces: `normalizeConfig` 返回对象新增 `mirrorCapture: { enabled: boolean, hosts: string[] }`；`buildConfigPayload` 返回对象新增 `mirrorCapture`；`appState.mirrorCaptureEnabled: boolean`；导出 `saveMirrorCaptureEnabled(enabled: boolean)`。

- [ ] **Step 1: configNormalize.js 加 mirrorCapture 归一化**

在 `frontend/src/utils/configNormalize.js` 的 `normalizeConfig` 返回对象中（`localResponseCache` 之后）加：

```js
    // 镜像记录官方请求（调试）：保留在归一化白名单中，避免保存其他设置时被清空回默认
    mirrorCapture: normalizeMirrorCapture(raw.mirrorCapture),
```

并在文件中（`normalizeConfig` 函数之前或附近）新增：

```js
// 默认镜像记录域名：与后端 config.DefaultMirrorHosts 一致。
const DEFAULT_MIRROR_HOSTS = [
  "api.openai.com",
  "api.anthropic.com",
  "generativelanguage.googleapis.com",
];

export function normalizeMirrorCapture(source) {
  const raw = source && typeof source === "object" ? source : {};
  const hosts = Array.isArray(raw.hosts) ? raw.hosts.filter((h) => typeof h === "string" && h.trim() !== "") : [];
  return {
    enabled: asBoolean(raw.enabled),
    hosts: hosts.length > 0 ? hosts : DEFAULT_MIRROR_HOSTS,
  };
}
```

- [ ] **Step 2: appState.js 三处接入**

(a) `buildConfigPayload`（:214-235）返回值加：

```js
    mirrorCapture: normalized.mirrorCapture,
```

(b) `applyConfigToState`（:257-278）在 `appState.debugLogEnabled = normalized.log;` 附近加：

```js
  appState.mirrorCaptureEnabled = normalized.mirrorCapture?.enabled ?? false;
```

(c) 状态初始化（约 :546 `debugLogEnabled: asBoolean(cachedConfig.log)` 之后）加：

```js
  mirrorCaptureEnabled: asBoolean(cachedConfig.mirrorCapture?.enabled),
```

(d) 文件末尾（`saveDebugLogEnabled` :1169-1175 之后）新增：

```js
// 镜像记录官方请求（调试）：只切换 enabled，hosts 保留配置现有值/默认列表。
export async function saveMirrorCaptureEnabled(enabled) {
  const currentConfig = await loadPersistedUserConfig();
  const mirrorCapture = currentConfig.mirrorCapture ?? {};
  return persistConfigPayload({
    ...currentConfig,
    mirrorCapture: {
      ...mirrorCapture,
      enabled: !!enabled,
    },
  });
}
```

> 需要确认 `asBoolean` 已从 configNormalize.js 导入（debugLogEnabled 已用，应已导入）；`cachedConfig` 结构已含 `mirrorCapture`（后端 Config 整体序列化，前端 `loadUserConfig` 原样返回，normalizeConfig 会兜底缺失）。

- [ ] **Step 3: 验证**

Run: `cd frontend && yarn lint`（若 lint 不过则修）
Run: `node -e "import('./src/utils/configNormalize.js').then(m => { const n = m.normalizeConfig({mirrorCapture:{enabled:true}}); if (!n.mirrorCapture.enabled || n.mirrorCapture.hosts.length !== 3) { console.error('FAIL'); process.exit(1); } console.log('OK', JSON.stringify(n.mirrorCapture)); })"`（Node ESM 直接验证归一化逻辑）
Expected: OK + `{"enabled":true,"hosts":["api.openai.com","api.anthropic.com","generativelanguage.googleapis.com"]}`

- [ ] **Step 4: 提交**

```bash
git add frontend/src/utils/configNormalize.js frontend/src/state/appState.js
git commit -m "feat(settings): 前端数据流接入 mirrorCapture（防保存清空）"
```

---

### Task 2: 高级设置页加调试开关

**Files:**
- Modify: `frontend/src/components/settings/categories/AdvancedSettings.vue`

**Interfaces:**
- Consumes: Task 1 的 `appState.mirrorCaptureEnabled`、`saveMirrorCaptureEnabled`。

- [ ] **Step 1: script 部分加状态与 handler**

在 `AdvancedSettings.vue` 的 `<script setup>` 中，参照 `debugLogDraft`/`debugLogState`/`handleDebugLogChange`（:183-204）实现：

```js
const mirrorCaptureDraft = ref(appState.mirrorCaptureEnabled ?? false);
const mirrorCaptureState = reactive({ busy: false, error: "", retry: null });

async function handleMirrorCaptureChange(enabled) {
  const nextValue = Boolean(enabled);
  const previousValue = mirrorCaptureDraft.value;
  mirrorCaptureDraft.value = nextValue;
  mirrorCaptureState.retry = () => handleMirrorCaptureChange(nextValue);
  mirrorCaptureState.error = "";
  mirrorCaptureState.busy = true;
  try {
    await props.autosave.run("advanced.mirror-capture", async () => {
      const result = await saveMirrorCaptureEnabled(nextValue);
      if (!result?.ok) {
        throw new Error(result?.error || "保存失败");
      }
      message.success(nextValue ? "已开启镜像记录（即时生效）" : "已关闭镜像记录（即时生效）");
    });
  } catch (error) {
    mirrorCaptureDraft.value = previousValue;
    mirrorCaptureState.error = toUserError(error);
  } finally {
    mirrorCaptureState.busy = false;
  }
}
```

> 若 `debugLogDraft` 通过 `watch(() => appState.debugLogEnabled, ...)` 同步外部变更，mirrorCaptureDraft 也加同样的 watch（参照现有写法，保持一致）。

- [ ] **Step 2: template 加开关行**

在 `AdvancedSettings.vue` 的 `<template>` 中，"调试日志" `SettingsSection`（:346-364）之后加：

```html
    <SettingsSection title="官方请求镜像记录（调试）">
      <SettingsRow
        label="镜像记录官方请求"
        description="抓取 Cursor 官方直连模型 API 的请求/响应明文（history/_debug/mirror/official.raw.jsonl），用于对比 BYOK 出站请求、优化功能一致性。测试专用，即时生效。"
        :busy="mirrorCaptureState.busy"
        :error="mirrorCaptureState.error"
        @retry="mirrorCaptureState.retry?.()"
      >
        <Switch
          compact
          label=""
          :enabled="mirrorCaptureDraft"
          :busy="mirrorCaptureState.busy"
          :disabled="mirrorCaptureState.busy"
          aria-label="镜像记录官方请求"
          @change="handleMirrorCaptureChange"
        />
      </SettingsRow>
    </SettingsSection>
```

> 确认 `Switch`、`SettingsSection`、`SettingsRow`、`ref`、`reactive`、`toUserError`、`message` 均已在该文件导入/可用（调试日志开关同款用法，应已具备）。

- [ ] **Step 3: 验证**

Run: `cd frontend && yarn lint`
Expected: 无新增错误（可能需先跑 Task 1 的改动一起 lint）。

- [ ] **Step 4: 提交**

```bash
git add frontend/src/components/settings/categories/AdvancedSettings.vue
git commit -m "feat(settings): 高级设置加镜像记录调试开关"
```

---

### Task 3: i18n scan 与构建验证

**Files:**
- Modify: `frontend/src/i18n/generated/catalog.json`、`frontend/src/i18n/locales/{zh-CN,en-US,ja-JP,ru-RU}.json`（scanner 生成/需填翻译）

- [ ] **Step 1: 扫描并生成 catalog**

Run: `cd frontend && yarn i18n:scan`
Expected: 新中文文案（"官方请求镜像记录（调试）"、"镜像记录官方请求"、description、两条消息）key 进入 `generated/catalog.json` 与 4 个 locale 文件。

- [ ] **Step 2: 填非源语言翻译**

在 `frontend/src/i18n/locales/en-US.json`、`ja-JP.json`、`ru-RU.json` 中为新 key 填非空翻译（参照"记录调试日志"等既有翻译风格，例如 en: "Mirror capture official requests" / description / "Mirror capture enabled (effective immediately)" / "Mirror capture disabled (effective immediately)"）。zh-CN.json 保留源文案。

- [ ] **Step 3: 构建验证**

Run: `cd frontend && yarn build --scan --mode production`（或项目实际构建命令 `node ./scripts/run-vite-build.mjs --scan --mode production`，README 开发节）
Expected: 构建成功；每个 locale JSON 与 catalog.json key 一致、非源 locale 无空值。

Run: `cd frontend && yarn lint`
Expected: 无错误。

- [ ] **Step 4: 提交**

```bash
git add frontend/src/i18n
git commit -m "chore(i18n): 镜像记录开关文案扫描与翻译"
```

---

## Self-Review 记录

- **Spec 覆盖**：开关（Task 2）+ 数据流防清零（Task 1）+ i18n/构建（Task 3）全部覆盖设计 6 点；hosts 不进 UI、不进发布文案、不做对比页符合 Global Constraints。
- **占位符扫描**：无 TBD；每步含实际代码/命令。
- **类型一致性**：`normalizeMirrorCapture` 在 Task 1 定义并被 `normalizeConfig` 使用；`saveMirrorCaptureEnabled`/`appState.mirrorCaptureEnabled` 在 Task 1 定义、Task 2 消费；`DEFAULT_MIRROR_HOSTS` 与后端 `config.DefaultMirrorHosts` 三域名一致。
- **已知风险**：后端 `NormalizeConfig`（types.go:243）已无条件透传 `MirrorCapture`，故前端保存后不会被吞；本计划防止的是**前端侧** payload 缺字段导致的覆盖。Task 1 的 Step 3 用 Node 直接验证归一化，不依赖 Wails 运行时。
