# Cursor 更新巡检与模型目录刷新记录

日期：2026-10-23（Asia/Shanghai）。基线：`8be05d92`（release: v1.0.13）之后的未提交工作区。

## Cursor 更新巡检结论：当前无需适配

- 本机安装包 `D:\cursor` 仍为 `3.22.12`（`resources/app/package.json`），与 2026-10-16 适配轮（`c2adfbb3`）同一版本。
- 安装包 bundle SHA-256 与上轮记录逐字节一致，适配时提交的协议源与实际安装版严格对齐：
  - `cursor-always-local/dist/main.js` = `C392530DEA61FA3EB0F9E560001A5741A7D1F5317E676F254B472FBF7793FF11`
  - `cursor-agent-exec/dist/main.js` = `2A0E07B7E9479B856072F5FCC8A1E5B88E2218C8F5071D445F47938F44D62D64`
- 官方 changelog 自 3.22.12 发布后仅有云侧产品功能（10-16 Cloud Agent Canvases / Environments API beta、10-13 Origin App Marketplace、10-06 iOS Remote Control、09-23 token 效率优化），未发现影响本地模式协议、BYOK 模型配置或 exec/forwarder 桥接的变化。Canvases 相关的 CanvasDiagnostics 路径上轮已适配。
- 仓库侧复核：`go run ./cmd/sync-tool-catalog` 渲染结果与 `docs/cursor-capability-map.md` 零漂移；`go test ./proto/ext_tool ./proto/apply_tool ./internal/backend/forwarder -count=1` 通过；`go build ./...` 通过。

后续：待官方发布高于 3.22.12 的版本后，按 `docs/superpowers/plans/2026-10-16-cursor-3.22.12-compatibility.md` 的提取→比对→适配流程重跑一轮。

## 模型能力目录刷新（internal/modelcontext/models.json）

按官方文档/发布说明补充 2026-09~10 国内外新模型：

| 模型 | 规则 | 关键规格 |
| --- | --- | --- |
| grok-4.6 / grok-4.7 | 新增 | 500K / 32K，$2/$6 |
| grok-4.7-fast | 新增 | 500K / 32K，$4/$12 |
| claude-opus-5.5（含 -5-5/-thinking） | 新增 | 1M / 128K，$4/$20，efforts low~max |
| claude-sonnet-5.5（含变体） | 新增 | 1M / 128K，$2/$10，efforts low~max |
| claude-haiku-5.5 | 新增 | 1M / 128K，$0.1/$0.5 |
| gpt-6-luna | 新增 | 1M / 128K，$0.1/$0.5，efforts none~xhigh |
| gpt-6.1-sol | 新增 | 标准处理上限 272K，$2/$10 |
| composer-2.5 | 新增 | 200K / 32K，$0.5/$2.5 |
| MiniMax-M3.1-Flash | 新增 | 1M / 16K，多模态，思考为开关式，$0.15/$0.6 |
| DeepSeek V4.1-Flash | 新增 | 1M / 32K，原生视觉，$0.3/$1.2（峰值） |
| Qwen3.8-Flash | 新增 | 1M / 131072，多模态，$0.15/$0.47 |
| Doubao Seed 2.1 Pro | 新增 | 1M（0915 版）/ 32K，多模态 + 深度思考，¥6/¥30 |
| grok-4.5 | 扩展 | 兼容 Cursor 渠道 `cursor-grok-4.5-*` 命名 |

调研后确认无需新增：GLM（无 5.4/6）、Kimi（K3 已覆盖）、Qwen4（尚未开放 API）、Gemini（3.8 Flash 已覆盖）、豆包 Seed Evolving（上下文规格未证实，暂不收录）。

注意：`internal/client/model_catalog.go` 侧 `volcengineBuiltinPricing` 中 doubao-seed-2.1-pro 仍为 ¥3/¥15（上轮按火山方舟价格表核对），与方舟产品页现价 ¥6/¥30 存在口径差异，待下次发版前按火山方舟官方页复核统一。

## 验证

- `go test ./internal/modelcontext/` 通过（含 `TestCapabilitiesOctober2026Models` 两批新增断言）。
- `go build ./...` 通过；proto/forwarder 回归通过（见上）。
