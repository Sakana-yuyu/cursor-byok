# 镜像抓包交换关联实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**目标：** 让本地 `official.raw.jsonl` 能可靠表示一次官方模型 HTTP 交换，供后续 Cursor 与 BYOK 请求对比使用。

**架构：** 在镜像请求过滤器中创建仅本地使用的关联信息并挂在 `goproxy.ProxyCtx.UserData`，响应过滤器复用同一信息。记录器为请求、响应起始、响应片段和截断事件写入统一的 `exchangeId` 与 `phase`；模型名从既有 JSON 正文或 Gemini URL 尽力提取。

**技术栈：** Go、`github.com/elazarl/goproxy`、NDJSON、本仓库既有 `internal/mitm`。

## 全局约束

- 不新增测试文件；复用并运行现有模块测试。
- 不改变官方 HTTP 请求，不修改真实 Cursor 配置，不向官方 API 主动发起请求。
- 不在前端传输或显示记录正文、URL、请求头。
- 每项独立提交，并在提交前运行 `git diff --cached --check`。

---

### 任务 1：固化关联记录规格

**文件：**
- 修改：`spec/changes/backend-capability-ui-discovery/{research,proposal,design}.md`
- 修改：`docs/superpowers/specs/2026-08-11-native-request-mirror-capture-design.md`

- [x] 将用户选择的推荐方案记录为 `exchangeId`、`phase` 和 `model`。
- [x] 明确 `exchangeId` 不进入官方网络请求，模型解析失败不影响直通，前端边界保持不变。
- [x] 运行 Markdown 差异检查并提交本任务。

### 任务 2：实现代理上下文与 JSONL 关联

**文件：**
- 修改：`internal/mitm/mirror.go`
- 修改：`internal/mitm/service.go`

- [x] 定义镜像交换上下文和四种记录阶段。
- [x] 在镜像请求分支创建上下文、记录请求并从 JSON/URL 尽力提取模型。
- [x] 在响应和响应流包装分支传递相同上下文，记录响应起始、片段和截断。
- [x] 运行 `go test ./internal/mitm`、`go build ./...` 与 `go vet ./internal/mitm`，检查代码输出字段并提交本任务。

### 任务 3：更新验证台账

**文件：**
- 修改：`spec/changes/backend-capability-ui-discovery/verify.md`

- [x] 记录静态、模块测试和构建证据。
- [x] 明确真实 Cursor 官方请求仍需用户显式完成的桌面端人工验证。
- [x] 提交本任务。
