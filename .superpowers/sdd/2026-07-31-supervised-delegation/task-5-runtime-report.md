# Task 5 Runtime UI Report

日期：2026-07-31

## 范围与边界

- 目标：为委派运行状态 UI 补齐 supervised delegation runtime 展示。
- 明确未改：
  - `frontend/src/components/settings/categories/DelegationSettings.vue`
  - `frontend/src/components/settings/delegation/DelegationGroupEditor.vue`
  - `frontend/src/components/settings/categories/SkillsMcpSettings.vue`
  - locale 文件
- 本次实际修改：
  - `frontend/src/components/DelegationTaskStrip.vue`
  - `frontend/src/components/DelegationRuntimePanel.vue`
  - `frontend/src/services/runtimeControlApi.js`
  - `frontend/src/services/browserBindings.js`
  - `internal/backend/delegation/scheduler.go`
  - `internal/backend/forwarder/delegation_runtime.go`
  - `internal/backend/forwarder/supervisor_coordinator.go`

## 实现摘要

### 1. 运行时安全字段桥接

- 扩展调度器/forwarder 的安全 runtime snapshot，仅携带：
  - `workerRole`
  - `supervisionPhase`
  - `supervisionRound`
  - `correctionCount`
  - `retryCount`
  - `reassignCount`
  - `escalateCount`
  - `issueCategory`
  - `progressSummary`
- `progressSummary` 只来自 checkpoint/监督安全摘要，不直接展示原始 prompt、tool 参数、凭据或绝对路径。
- follow-up worker（correct/retry/reassign/escalate）在重新提交前会把当前监督计数和安全摘要带入新的 runtime snapshot，保证轮询时能看到连续状态。

### 2. 前端 runtime wrapper

- 在 `runtimeControlApi.js` 增加 snapshot 归一化：
  - 兼容后端字段名差异
  - 统一导出 `isSupervised`
  - 统一 round/counter/category/progress 字段

### 3. Task Strip / Runtime Panel UI

- `DelegationTaskStrip.vue`
  - 保留现有 polling / stale-generation / cancel 行为
  - 新增 phase、role、round、counter、issue category、safe progress summary 的紧凑展示
- `DelegationRuntimePanel.vue`
  - 保留现有 task + MCP 双轮询、busy/error/cancel 行为
  - 在每个 task 卡片中增加 supervised runtime 元数据展示

### 4. Browser preview

- 更新 `browserBindings.js` 的 preview task mock，便于浏览器预览模式看到监督字段。

## 验证

已执行：

1. `npm --prefix frontend run build`
   - 结果：通过
   - 备注：存在既有 chunk size / dynamic import 警告，但不阻塞构建
2. `git diff --check`
   - 结果：通过
   - 备注：仅有既有 LF/CRLF 警告，无 whitespace error

## concerns

- 未执行桌面页面手动验收（enable/save/reload/disable/cancel、窄宽度、console）；
  当前仅完成代码级实现与构建校验。
- 为满足 runtime UI 展示需求，除前端文件外还补充了最小后端安全 snapshot 映射文件；未触及受保护的 settings/locale 改动。

## Focused Review Fix

- `DelegationTaskStrip.vue` 同时渲染 `issueCategory` 和 `progressSummary`，保留紧凑布局、换行和溢出约束。
- `runtimeControlApi.js` 统一归一化 `reviewPending`、`supervisionPhase` 和 `cancelable`；显式后端 `cancelable` 优先，缺失时只按 task status 回退，不把 `reviewing` 视为终态。
- `DelegationTaskStrip.vue` 与 `DelegationRuntimePanel.vue` 使用 phase 作为有效状态展示，取消按钮仍只由 normalized `cancelable` 控制。
- runtimePanel 仍只展示安全 runtime 字段，未增加 prompt、tool arguments、credentials、workspace paths 或 raw output 的渲染。
