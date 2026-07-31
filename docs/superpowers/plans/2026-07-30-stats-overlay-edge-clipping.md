# 统计浮窗贴边形变 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 让统计浮窗在四个方向贴边后平滑收缩，并从贴边锚点一次展开，消除裁切、闪烁、抖动和跨窗口偏好不同步。

**Architecture:** Go 端负责原生窗口尺寸、显示器工作区夹取和贴边锚定；Vue 浮窗负责 pointer 状态和形态切换；持久化偏好通过共享状态事件同步。尺寸更新与置顶更新使用独立接口。

**Tech Stack:** Go、Wails v3、Vue 3、Vite

## Global Constraints

- 不新增测试文件。
- 不修改统计数据来源、刷新周期、贴边阈值或已确认的视觉样式。
- 不修改已安装的 Cursor 客户端。
- 不提交 GUI 截图或构建产物。

## Task 1：对齐四向贴边尺寸

- [x] 左右贴边使用 `44 x 当前样式完整高度`。
- [x] 上下贴边使用 `当前样式完整宽度 x 36`。
- [x] 未贴边锁定胶囊使用 `112x36`。
- [x] 贴边 inset 使用 `0`，保持对应边缘锚定。

## Task 2：稳定展开和收缩交互

- [x] 悬浮时只向远离贴边的一侧展开，并保持鼠标位于恢复后的窗口内。
- [x] 离开时直接形变为最小浮窗，不叠加插入或位移动画。
- [x] 使用当前 pointer 与窗口边界判断离开，避免 resize 产生的合成事件触发反馈循环。
- [x] 锁定和自动收缩偏好变化时立即执行双向形态转换。

## Task 3：恢复位置与偏好同步

- [x] 托盘打开时恢复保存坐标，并将坐标夹取到当前显示器工作区。
- [x] 主窗口、设置抽屉和浮窗实时同步锁定、自动收缩和置顶偏好。
- [x] 尺寸 morph 不再写入置顶状态，置顶由独立接口控制。

## Task 4：验收

- [x] `go test ./...`、`go build ./...` 通过。
- [x] 前端 production build 通过，i18n 扫描目录稳定。
- [x] 四向贴边尺寸、锚点、托盘恢复和偏好同步完成代码审查。
- [x] `gui-test-screenshots/` 保留为本地证据并加入忽略规则。
