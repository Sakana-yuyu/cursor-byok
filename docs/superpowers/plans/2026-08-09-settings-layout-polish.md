# 设置页收缩导航与全尺寸布局优化实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 优化设置页收缩导航的视觉层级，并让设置工作区适应中等、宽屏和窄窗口尺寸。

**Architecture:** 保持现有设置状态与分类组件不变，只调整页面 shell 和侧栏的职责边界。`Settings.vue` 负责弹性尺寸与响应式容器，`SettingsSidebar.vue` 负责展开/收缩导航的视觉和交互，现有分类组件继续消费同一 `autosave` 接口。

**Tech Stack:** Vue 3 Composition API、现有 Tailwind utility classes、scoped CSS、现有 i18n 扫描机制。

## Global Constraints

- 不回退工作区中与本任务无关的未提交改动。
- 不改变设置分类 ID、路由、持久化键、autosave 和后端 payload。
- 不添加新的依赖或测试文件；使用现有前端构建、静态检查和浏览器检查验证。
- 不修改安装中的 Cursor 客户端、bundle 或 `.cursor-app-formatted`。
- 用户可见源文本保持中文；本次优先复用现有文本，若新增文本须运行 i18n 构建。

---

### Task 1: 重构设置页面弹性工作区

**Files:**
- Modify: `frontend/src/views/Settings.vue`

**Interfaces:**
- Consumes: `SettingsSidebar` 的 `collapsed`、`selectedCategory` 和 `moreExpanded` 双向绑定。
- Produces: 页头与分类内容共享可用宽度；在窄窗口下不产生横向溢出。

- [ ] 调整外层容器，移除对整个工作区的固定最大宽度约束，改用 `min-w-0` 和弹性列。
- [ ] 让页头、滚动内容与分类面板使用同一个弹性内容列，保留内容内部的可读宽度约束。
- [ ] 增加窄窗口断点下的纵向布局和分类选择器容纳空间，确保 `sm` 以下侧栏不会挤压内容。
- [ ] 保留现有 Transition、返回逻辑、autosave status 和 `prefers-reduced-motion` 规则。
- [ ] 运行 `npm --prefix frontend run build`，确认编译和 i18n 扫描成功。

### Task 2: 优化收缩侧栏视觉与菜单行为

**Files:**
- Modify: `frontend/src/components/settings/SettingsSidebar.vue`

**Interfaces:**
- Consumes: `categories`、`modelValue`、`moreExpanded`、`collapsed` props 及现有 emits。
- Produces: 展开态分组导航、收缩态图标窄栏、可访问工具提示和稳定的“更多”菜单。

- [ ] 先保留现有导航数据分组逻辑，只替换收缩态的尺寸、背景、选中态和间距样式。
- [ ] 收缩态隐藏分组标题和冗余装饰，仅保留分类图标按钮；按钮统一命中区域并添加分类 `title`。
- [ ] 将“更多”从悬停延时浮层调整为点击打开/关闭的右侧菜单，菜单项继续复用分类图标、标题和描述。
- [ ] 为菜单外点击、Escape、键盘 focus 和 reduced-motion 保留明确行为；不改变选中分类的父子状态同步。
- [ ] 运行 `npm --prefix frontend run build` 并用浏览器检查展开、收缩和窄窗口三种状态。

### Task 3: 静态检查与视觉回归

**Files:**
- Review: `frontend/src/views/Settings.vue`
- Review: `frontend/src/components/settings/SettingsSidebar.vue`

**Interfaces:**
- Consumes: Task 1 与 Task 2 的布局和交互实现。
- Produces: 可复现的构建、lint 和界面检查结果。

- [ ] 对最近编辑的 Vue 文件运行 linter 检查并修复本次引入的错误。
- [ ] 运行 `npm --prefix frontend run build`。
- [ ] 运行 `git diff --check`，确认没有空白错误。
- [ ] 检查工作区 diff，只确认目标文件中的修改，不覆盖其他已有改动。
- [ ] 在宽屏、中等窗口和窄窗口验证：分类切换、返回、收缩持久化、更多菜单、键盘 focus、无横向滚动和保存状态。