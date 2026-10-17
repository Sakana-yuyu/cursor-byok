# Cursor 上游更新与本地适配记录

日期：2026-10-03（Asia/Shanghai）

## 上游核对

本次先执行了 `git fetch upstream --prune`。当前仓库的 `main` 与 `upstream/main` 没有共同祖先：本地仍是 Go/Wails3/Vue 架构，上游已经迁移到 Rust/Tauri/React，因此不能直接合并上游分支。上游最新提交为 `9a8fde279c701b47dae97b7a18ebe92fa0119013`。

与当前仓库功能边界最相关的上游更新如下：

- `9a8fde27 feat(byok): add external API for local apps`：增加本机 OpenAI Chat/Responses、Anthropic Messages 兼容接口，带本机 Key 鉴权和原生协议转发。
- `4420c33f fix(local-app): don't fail takeover when Cursor termination fails`：接管本地应用时，Cursor 进程终止失败不再阻断代理设置写入。这个行为只适用于上游本机接管流程，当前仓库的账户切换/重启路径仍需要显式终止进程，未直接套用。
- `d9267eea refactor(storage): show row counts instead of byte size`：上游 SQLite 存储统计改为行数，当前仓库没有同构 SQLite 存储层，未移植。
- `2068ab20 chore(release): bump desktop to 1.0.1`：上游桌面版本号调整，未改变当前仓库版本。

更早的上游工具别名、内容过滤、MCP 截断与 usage anchor 变更在本地已有对应实现；Rust/Tauri 的云端 Projects、Self-hosted machines、Teams/Enterprise Rollouts 需要上游云服务和新桌面运行时，本次没有伪装成已适配。

## 已适配内容

本地新增 `/byok/v1` External API：

- `GET /byok/v1/models`
- `POST /byok/v1/chat/completions`
- `POST /byok/v1/responses`
- `POST /byok/v1/messages`

接口默认关闭，只接受环回连接，并要求 `Authorization: Bearer <key>` 或 `x-api-key: <key>`。模型清单只暴露启用的第三方 OpenAI/Anthropic 通道；Cursor 账户模型、停用模型和 Gemini 通道不会被错误宣传为可用的这三类接口。

请求转发按通道已配置的协议组严格匹配：Chat 不会路由到 Responses，Messages 不会路由到 OpenAI。转发会原样保留工具、图片、推理和供应商扩展字段，只替换供应商实际模型 ID；上游 Key、Host、Cookie 和本机 API Key 不会从请求头透传。重定向被禁止，流式响应支持空闲超时和客户端取消。

设置页的“本机 External API”区提供开关、随机 Key、显式保存和接口地址复制。Key 不写入前端 `localStorage` 快照；后端配置保存时启用状态不能为空 Key。

## 当前客户端证据

本机安装目录 `D:\cursor` 仅作只读核对：`product.json` 显示 Cursor `3.22.7`，commit `37076c6c3f9e253c0fa2305197e45befd13a2260`。`go run ./cmd/cursor-capability-scan -root D:\cursor` 返回 scannerVersion `1`、扩展 `cursor-agent-exec`/`cursor-agent-host`/`cursor-browser-automation`，以及 `computer_use`、`subagent_await`、`mcp_allowlist_precheck` 等协议标记。现有 `.cursor-app-formatted/` 快照为9月1日旧版本，哈希与安装包不一致；没有修改安装包或快照，结论以当前安装包只读扫描为准。

## 回滚

关闭设置页 External API 开关即可立即停止新接口；删除或回滚 `externalApi` 配置字段不会影响既有模型适配器。代码回滚范围为本记录列出的 External API 文件、配置白名单和设置页改动；没有执行远端 push、生产部署或 Cursor 安装包修改。

## v1.0.13 发布前复核（2026-10-17）

用户已要求发布新版本，本功能与 Cursor 3.22.12 兼容改动一并进入发布范围。独立审查发现前端显式保存同时携带旧 `externalAPI` 与新 `externalApi`，归一化时旧值优先，造成保存成功但配置未更新。现已统一内部字段为 `externalAPI`，在生成后端载荷时转换为 `externalApi`。

新增浏览器回归通过实际设置页覆盖保存 Key、启用、Key 轮换、关闭及运行状态缓存不含 Key；修复前能复现失败，修复后完整 93 项浏览器回归通过。前端 lint、61 项单元测试与生产构建通过；External API 包测试通过。浏览器使用预览绑定，后端使用本地受控测试，仍未调用真实供应商。发布与六平台产物以 GitHub Actions 和对应 Release 结果为准。
