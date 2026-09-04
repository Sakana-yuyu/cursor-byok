# 新手使用引导（Guided Tour）设计

日期：2026-09-04
分支：feat/guided-tour
状态：已实现待评审

## 背景

用户反馈新用户打开应用后不知道从哪里入手（配置模型 → 启动服务 → 在 Cursor 中使用）。
需要一个基础引导：**用户自行点击入口**触发（不自动弹出），随后分步引导了解核心使用流程。

上游评估结论（同日调研）：上游 leookun/cursor-byok 9120b90b..8942287a 的更新
（Task 投影、Commit 设置、bash 别名、ProxyMode 枚举等）在本仓库 Go/Vue 重构中
均已有等价实现或无对应机制，无需引入；本功能为本地自研，与上游无关。

## 交互设计

- 分步 spotlight tour：半透明遮罩镂空高亮当前步骤的目标元素，旁边浮动气泡卡片
  （标题 + 说明 + 步骤进度 + 跳过/上一步/下一步按钮）。
- 无目标元素的步骤（欢迎、完成）显示为居中卡片。
- 步骤可跨页面：进入步骤时按需 `router.push`，等待目标元素渲染（轮询，超时降级为居中卡片，不阻塞流程）。
- 键盘：Esc 跳过；Enter 下一步。
- 引导结束后写 localStorage 完成标记（`cursor-byok.guided-tour.completed`），
  入口常驻首页（用户可重复查看），标记仅供后续"新功能引导"扩展与文案微调。

## 步骤内容（7 步，覆盖首页与模型配置页）

1. 欢迎（首页，居中）：本工具把自定义 API 密钥接入 Cursor，流程三步走。
2. 侧边栏「模型」入口（高亮 AppSidebar 第 2 项）：第一步先配置供应商与模型。
3. 模型配置页主体（跳转 /model-config，高亮页面主区域）：添加/编辑供应商与模型。
4. 首页「启动服务」按钮（返回首页）：配置完成后启动本地服务。
5. 服务监听地址（高亮首页 proxyListenAddr；服务未运行时元素不存在 → 自动降级居中卡片）：Cursor 已被指向该地址；如未生效用「更多 → 修复代理」。
6. 侧边栏「设置」入口：语言、日志等偏好。
7. 完成（居中）：随时可从首页重新打开引导。

## 组件与文件

| 文件 | 职责 |
| --- | --- |
| `frontend/src/composables/useGuidedTour.js` | 纯逻辑：步骤序列、状态推进、路由跳转、元素等待（可注入依赖）、完成标记。单例 composable。 |
| `frontend/src/composables/useGuidedTour.test.js` | node --test 单测：推进/回退边界、跳过/完成、跨路由 push、元素等待与超时降级。 |
| `frontend/src/components/onboarding/GuidedTour.vue` | UI：遮罩镂空（box-shadow 100000px 方案）+ @floating-ui/dom 气泡定位（复用 Tooltip.vue 模式）+ autoUpdate。z-[90000]（低于 Modal 的 z-[100000]）。 |
| `frontend/src/views/Home.vue` | 顶部操作区新增「使用引导」按钮入口。 |
| `frontend/src/components/layout/AppSidebar.vue` | nav 按钮加 `data-tour-nav="<path>"` 供选择器定位。 |
| `frontend/src/views/ModelConfig.vue` | 主区域加 `data-tour-target="model-config-root"`。 |
| `frontend/e2e/guided-tour.spec.mjs` | Playwright e2e（browser-preview mock 模式）。 |

## 关键实现决策

- **不引入 driver.js 等依赖**：@floating-ui/dom 已在用，自研组件约 200 行，可控且无供应链负担。
- **目标元素定位用 data 属性选择器**而非文本匹配，避免文案变更导致引导失效。
- **遮罩用单个 div + 巨幅 box-shadow 镂空**：天然阻止外部点击（引导期间不可操作页面），圆角用 border-radius + padding 模拟。
- **元素等待超时（2s）降级居中卡片**：条件渲染的元素（如服务地址）不存在时引导不卡死。
- **i18n**：源文案直接写中文（zh-CN 唯一源语言），`npm run build` 扫描后补全
  en-US / ja-JP / ru-RU 非空翻译，placeholder 保持一致。

## 测试

- 单测：composable 纯逻辑（注入 fake router/storage/resolveElement）。
- e2e：入口点击 → 欢迎卡片 → 跨页推进到模型配置页 → 跳过/完成路径。
- 手动验证：桌面模式（yarn dev）确认镂空与气泡定位、Esc 退出。
