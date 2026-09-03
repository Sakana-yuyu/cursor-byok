# Daoxe 模型自适应兼容性实施计划

> **执行约束：** 按任务逐项执行；每项完成后运行该项最小验证，再进入下一项。

## 目标

让 OpenAI 兼容供应商的默认输出上限不再发送不安全的 `65536`，使 Daoxe 目录、协议分组和已有超限恢复链路可以自动适配；保留显式配置优先级，不把实际不支持的模型伪装成可用。

## 当前证据与边界

- Daoxe `/v1/models` 曾返回 49 个模型，最终复测返回 57 个模型；目录是动态的，聊天补全接口可用模型与失败原因存在差异。
- Daoxe `/v1/responses` 对已验证模型返回 `not implemented`，因此 Daoxe 默认走 Chat Completions。
- 部分文本模型在 `max_tokens=65536` 时返回 `status=400`，正文为 `max_tokens (65536) exceeds limit (4096)`；已有恢复代码能解析该上限并持久化。
- 当前任务不新增模型权重、不记录账户 key、不修改数据库、不推送远端，也不把超时模型直接判定为不可用。

## 实施任务

### 任务 1：先固定安全默认值，再实现最小变更

- [x] 在 `internal/backend/server/config/resolver_test.go` 增加测试，断言未显式配置时 OpenAI/Gemini 为 4096、Anthropic 为 65536，且显式 `MaxCompletionTokens` 仍优先。
- [x] 在 `internal/client/model_adapter_benchmark_test.go` 增加测试，断言三类协议的测试预算遵守同一策略，并验证真实 OpenAI 请求体预算。
- [x] 先运行以上两个聚焦测试，确认旧实现按预期失败。
- [x] 修改 `resolver.go` 的默认常量及 Anthropic 独立默认值；修改 `model_adapter_benchmark.go` 的测试预算常量。
- [x] 重跑聚焦测试与对应 Go 包测试。

### 任务 2：接入 Daoxe 供应商模板，复用现有目录和探测流程

- [x] 在 `frontend/src/utils/supplierCatalog.js` 增加 Daoxe 图标复用和 OpenAI Chat Completions 模板，目录地址固定为 `https://api.daoxe.com/v1/models`，不写入静态模型清单。
- [x] 增加 `frontend/src/utils/supplierCatalog.test.js`，断言模板、目录地址、协议组和默认端点；断言未知供应商不会误用 Daoxe 配置。
- [x] 运行该单测，随后运行前端单测。

### 任务 3：锁定精确报错的回归测试

- [x] 在 `internal/backend/forwarder/max_tokens_recovery_test.go` 增加精确正文的解析/识别测试，并覆盖非 400、无上限和不相关错误不触发恢复。
- [x] 现有实现已满足测试，不修改恢复逻辑；仅保留测试作为回归保护，避免重复造第二套恢复器。
- [x] 运行 `go test ./internal/backend/forwarder -run 'MaxTokens|Token' -count=1`。

### 任务 4：完整验证与外部证据

- [x] 运行 `go test ./...`、`go vet ./...`、`go build ./...`、前端 `npm run test:unit`、`npm run lint`、`npm run build` 和 `git diff --check`。
- [x] 重新请求 Daoxe 目录并以脱敏摘要记录总数、成功、配额不足、明确拒绝和超时；没有把超时当作模型不支持。
- [x] 用同一模型分别验证安全预算和超大预算的行为，确认 400 超限正文仍能被恢复器识别；没有输出 key 或完整响应。
- [x] 检查构建生成物和工作区，只保留本任务文件；变更已按任务范围暂存，后续仅本地提交、不推送。

## 验收标准

1. 新建且未显式配置上限的 OpenAI/Gemini 通道最多发送 4096；Anthropic 路径仍保留 65536 的独立默认值。
2. 显式上限、已有更小持久化上限和自动恢复优先级不被破坏。
3. Daoxe 能从模型目录进入现有探测、批量添加和请求路径，默认协议为 Chat Completions；Responses 不可用不阻断 Chat 路径。
4. 精确 `max_tokens ... exceeds limit ...` 错误有回归测试，且不相关错误不会误降级。
5. 所有可运行的本地检查通过；外部模型结果按“成功/配额/明确不支持/超时未确认”分类，未验证项在交付中明确列出。

## 回滚

回滚仅需恢复本任务涉及的提交或文件；不涉及数据库迁移和远端发布。若供应商更改限制，可通过显式通道上限和现有持久化恢复机制暂时收敛预算，再修订默认常量。
