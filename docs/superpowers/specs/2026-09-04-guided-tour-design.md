# 新手使用引导（Guided Tour）设计

日期：2026-09-04
分支：feat/guided-tour
状态：已实现（交互式改版）待评审

## 背景

用户反馈新用户打开应用后不知道从哪里入手（配置模型 → 启动服务 → 在 Cursor 中使用）。
需要一个基础引导：**用户自行点击入口**触发（不自动弹出），随后分步引导了解核心使用流程。

上游评估结论（同日调研）：上游 leookun/cursor-byok 9120b90b..8942287a 的更新
（Task 投影、Commit 设置、bash 别名、ProxyMode 枚举等）在本仓库 Go/Vue 重构中
均已有等价实现或无对应机制，无需引入；本功能为本地自研，与上游无关。

## 交互设计（v2：交互式引导）

- **点击驱动**：`advanceOn: "click"` 的步骤由用户真实点击目标元素前进（点击放行，
  元素原有行为照常发生——侧边栏导航、启动服务）。用户按引导实际操作一遍，而非看幻灯片。
- **点击治理**（document capture 阶段监听）：气泡内点击不干预；目标内点击按上条放行并前进；
  其余页面区域点击拦截（防止引导期间误操作），并让气泡抖动提醒"点这里"。
  高亮框 `pointer-events: none`，事件治理全在 capture 监听，遮罩不吞事件。
- **醒目高亮**：目标元素 2px 主题绿（#10AD5D）边框 + 巨幅 box-shadow 镂空遮罩
  + 脉冲光晕动画（tour-pulse）。
- **明确指向**：气泡用 floating-ui arrow middleware 带指向箭头，紧贴目标元素；
  文案一律指令式（"点击「模型」…"）；click 步骤不显示「下一步」，改为
  绿色"点击高亮位置继续"提示（Enter 仍可推进作为无障碍兜底）。
- 无目标元素的步骤（欢迎、完成）显示为居中卡片；条件渲染元素等待超时降级居中卡片
  （单步可用 `elementTimeoutMs` 覆盖，启动服务后的地址步骤放宽到 8s）。
- **动态步骤**：服务已运行时自动跳过"启动服务"步骤（buildTourSteps(serviceRunning)）。
- 键盘：Esc 跳过；Enter 下一步（click 步骤除外）。
- 引导结束后写 localStorage 完成标记（`cursor-byok.guided-tour.completed`），
  入口常驻首页（用户可重复查看）。

## 步骤内容（7 步；服务运行中为 6 步）

1. 欢迎（首页，居中）。
2. 点击侧边栏「模型」入口（click 驱动，真实导航）。
3. 「+ 新增模型」按钮（模型配置页，展示 + 下一步）：说明添加模型入口。
4. 点击「启动服务」（click 驱动；服务运行中跳过此步）。
5. 代理监听地址（服务启动后出现；等待超时 8s，降级居中卡片）。
6. 点击侧边栏「设置」入口（click 驱动）。
7. 完成（居中，就地显示不跳路由）。

## 组件与文件

| 文件 | 职责 |
| --- | --- |
| `frontend/src/composables/useGuidedTour.js` | 纯逻辑：步骤序列、状态推进、路由跳转、元素等待（可注入依赖、单步超时）、完成标记。单例 composable。 |
| `frontend/src/composables/useGuidedTour.test.js` | node --test 单测。 |
| `frontend/src/components/onboarding/GuidedTour.vue` | UI：镂空遮罩 + 脉冲高亮 + arrow 气泡 + 点击治理 + 键盘。 |
| `frontend/src/views/Home.vue` | 「使用引导」按钮入口 + 服务按钮/地址 data 标记。 |
| `frontend/src/components/layout/AppSidebar.vue` | nav 按钮加 `data-tour-nav="<path>"`。 |
| `frontend/src/views/ModelConfig.vue` | 「新增模型」按钮加 `data-tour-target="model-config-add"`。 |
| `frontend/e2e/guided-tour.spec.mjs` | Playwright e2e：交互全流程/拦截/跳过/Esc。 |

## 关键实现决策

- **不引入 driver.js 等依赖**：@floating-ui/dom 已在用，自研可控且无供应链负担。
- **目标元素定位用 data 属性选择器**，文案变更不破坏引导。
- **事件治理放 capture 监听**而非遮罩拦截：目标元素真实可点（放行原生行为），
  非目标区域 preventDefault+stopPropagation 拦截。
- **i18n**：zh-CN 唯一源语言，`npm run build` 扫描后补全 en/ja/ru 非空翻译。
- browser-preview 扫描构建：`node ./scripts/run-vite-build.mjs --scan --mode browser-preview`
  （生产构建需 wails bindings）。

## 测试

- 单测：composable 纯逻辑（推进/回退/跳过/完成、跨路由、元素等待、单步超时降级）。
- e2e：交互式全流程（点击驱动跨页 + 服务 mock 启动后地址步骤自动衔接 + 完成标记）、
  非目标点击拦截、跳过无标记、Esc。
- 视觉截图审查：高亮醒目度、箭头指向、指令文案（已通过）。

