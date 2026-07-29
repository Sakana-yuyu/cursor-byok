# 直连模式悬浮说明 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将主页直连模式的长说明收纳到问号悬浮提示中，降低顶部控制区的视觉突兀感，同时保留可发现性和无障碍访问。

**Architecture:** 仅修改主页 `Home.vue` 的直连模式布局，不改变 `handleDirectModeChange`、路由模式状态或开关行为。使用原生 `title` 作为桌面悬浮提示，并在问号按钮上提供 `aria-label`、键盘焦点样式和可聚焦语义。

**Tech Stack:** Vue 3、Tailwind CSS、现有 `Switch` 组件。

## Global Constraints

- 本仓库代码禁止写任何测试。
- 不修改已安装的 Cursor 客户端代码、bundle 或 app 副本。
- 保留现有直连模式逻辑和开关行为。
- 使用 ASCII 编辑；不引入新的依赖。

---

### Task 1: 收纳直连模式详细说明

**Files:**
- Modify: `frontend/src/views/Home.vue:30`

**Interfaces:**
- Consumes: 现有 `directModeEnabled`、`appState.configSaving`、`handleDirectModeChange`。
- Produces: 顶部控制区内紧凑的直连模式标题、问号提示按钮和原有开关。

- [ ] **Step 1: 替换主页直连模式布局**

将当前带长 `description` 的紧凑 `Switch` 改为一个紧凑容器：标题旁增加问号按钮，按钮使用 `title="直连模式：绕过本地服务并直接连接官方，可能产生官方账号计费。"`、`aria-label="直连模式说明"`，并保留 `Switch` 的 `:enabled`、`:busy`、`:disabled` 和 `@change` 绑定。问号按钮不能触发开关，使用 `type="button"` 和 `@click.stop`。

问号按钮应使用 `?` 文本、`title` 原生悬浮提示、`focus-visible` 环形边框和不影响布局的固定尺寸；开关旁仅显示“直连模式”和“已开启/已关闭”状态。

- [ ] **Step 2: 检查源码差异**

运行：

```bash
rg -n "直连模式|直连模式说明|description=" frontend/src/views/Home.vue
```

预期：主页直连模式不再直接渲染长说明文本；问号按钮包含说明标题和无障碍标签；开关仍绑定原有处理函数。

- [ ] **Step 3: 构建前端**

运行：

```bash
npm --prefix frontend run build
```

预期：构建退出码为 0；允许既有动态导入和 chunk 大小警告。

- [ ] **Step 4: 浏览器验证**

在 `http://localhost:5174/` 检查：

- 顶部直连模式区域不再显示长说明文本。
- 问号按钮可见，鼠标悬浮显示完整说明。
- 键盘聚焦问号按钮时显示焦点样式，并可通过可访问名称识别。
- 直连开关仍可正常操作。

- [ ] **Step 5: 运行差异检查**

运行：

```bash
git diff --check
```

预期：无空白错误或冲突标记。
