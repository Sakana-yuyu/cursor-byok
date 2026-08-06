# cursor2api / 网关类项目 vs cursor-byok：可移植机制调研

> 版本：2026-08 · 调研基线：cursor2api `v2.7.8`（hkxiaoyao，Python）、new-api（QuantumNous，Go）、Cline（cline/cline，TS）、cursorToApi（PieAIStudio）、上游 origin（Sakana-yuyu/cursor-byok）。
> 范围：与 cursor-byok 同类（Cursor 协议代理 / LLM 网关 / BYOK 编码 agent）项目中值得移植的机制；opencode 已单独对比（见 `docs/opencode-comparison-and-optimization.md`，编号 A1-A4/B1-B3/C1-C2），cc-switch 已对齐（见 `docs/superpowers/plans/2026-08-02-ccswitch-supplier-alignment.md`），本文不再重复。本文建议编号 D1-D7。

---

## 1. 调研对象与架构总览

| | cursor-byok | cursor2api | new-api | Cline |
|---|---|---|---|---|
| 语言/形态 | Go，本地代理进程（CA + MITM + 桥接） | Python，本地 HTTP 代理 | Go，自托管网关 | TS，VS Code 插件 + CLI + SDK |
| 方向 | **API → Cursor**（BYOK 接入） | **Cursor → API**（账号逆向，协议转换互逆） | **API 聚合分发**（渠道池 → 统一 API） | 独立编码 agent（自带编辑器集成） |
| 与本项目的可移植关系 | — | 协议转换/防截断/诊断机制 | 渠道管理/余额/熔断机制 | checkpoint/Plan-Act/linter 自修复 |

**上游核对**：origin（Sakana-yuyu/cursor-byok）相对本地仅领先 1 个 docs commit（`3a7269c`），无领先功能可拉取。

---

## 2. 可移植机制逐项对比

### 2.1 防截断（cursor2api v2.7.8，最直接同类）

| 机制 | cursor2api 做法 | cursor-byok 现状 | 可移植性 |
|---|---|---|---|
| 按工具类型差异化截断 | Read 头 50%+尾 30%、Bash 头 20%+尾 60%、Search 头 70%+尾 15% | `tool_result_snip.go` 统一头尾各 50%（≥8KB 裁至 4KB，保护尾部 4 轮） | **高**（D1）：同一函数按工具名换比例，改动局部 |
| 上下文压力膨胀 | 虚增报告给客户端的 input_tokens（如系数 1.35），让客户端提前自动压缩 | `context_overflow.go` 事后减半 + `compaction.go` 强制压缩（事前预防缺失） | 中（D3）：与 prefix-cache-stability、token 统计准确性冲突，须默认关闭按供应商开启 |
| 自适应历史预算 | 工具数量越多，自动预留越多输出空间 | max_tokens_recovery 只按失败减半/2048 兜底 | 中（D5）：可并入 max_tokens 恢复逻辑 |
| 动态工具结果预算 | 按上下文大小调整截断上限，替代固定值 | snip 固定 4KB 上限 | 低：现有 4 轮保护已足够，避免复杂化 |

### 2.2 诊断（cursor2api v2.7.7）

| 机制 | cursor2api 做法 | cursor-byok 现状 | 可移植性 |
|---|---|---|---|
| degraded（降级）状态分类 | 请求"能返回但体验差"时标记 degraded 并给出原因：工具不可用假成功、max_tokens 未续写、模型自述"写到一半/补写中" | 终态诊断无此语义分类；max_tokens_recovery/shell_recovery 各管一段，无统一标记 | **高**（D2）：请求明细加 degraded 字段 + 原因枚举，前后端各加少量代码 |
| 请求阶段耗时时间线 | 日志查看器按 receive→convert→send→response→complete 展示各阶段耗时 | 请求明细无阶段耗时 | 中（D4）：转发链路上游处打时间戳即可 |

### 2.3 渠道管理（new-api / one-api）

| 机制 | new-api 做法 | cursor-byok 现状 | 可移植性 |
|---|---|---|---|
| 渠道余额阈值 | 余额耗尽自动禁用渠道（cron 轮询 + 请求失败联动） | 有余额查询（supplier-registry）与健康冷却，无阈值自动禁用/告警 | 中（D6）：余额低于阈值标红/暂停，复用现有健康冷却状态机 |
| 缓存命中计费 | 按缓存命中扣减不同费用 | 本地个人工具无计费需求 | 不移植 |
| 多租户 / OIDC / 令牌审计 | 组织级隔离与审计 | 单机本地工具 | 不移植 |
| 渠道测活 | 定时 + 请求失败双路径测活 | 已有请求失败路径（router 健康冷却） | 基本对齐，不重复 |

### 2.4 Agent 体验（Cline / Roo Code）

| 机制 | Cline 做法 | cursor-byok 现状 | 可移植性 |
|---|---|---|---|
| step 级 checkpoint | 每个编辑快照可 undo | turn 级 rewind（rewind.go） | 与 opencode C2 合并排期（远期） |
| Plan/Act 模式 | Plan 只读探索、确认后切换 | 已有 PLAN mode 工具白名单 | 基本对齐 |
| linter/编译错误自修复 | 工具执行后监听 linter 错误并自动修复 | 网关不直接操作文件；可注入 linter 错误信息提示模型 | 中（D7）：工具结果附错误摘要，成本低于完整自修复 |
| 多 key 池化（cursorToApi） | 逗号分隔 token 轮询 | 已有渠道轮询负载均衡 + 健康冷却 | 已覆盖，不重复 |
| Custom Modes（Roo Code，项目已关停） | 自定义 mode 组合（prompt+模型+工具） | 已有 mode 枚举 + 工具白名单，无自定义扩展 | 低优先级，暂不移植 |

---

## 3. 综合结论

- **协议转换方向互逆**：cursor2api 是"Cursor 账号 → 标准 API"，cursor-byok 是"标准 API → Cursor"，但两侧共享同一批协议坑（截断、假成功、未续写），其防截断与诊断机制是本次最高价值的移植对象。
- **网关管理**：new-api 面向多用户分发（租户/计费/审计），与单机本地工具场景重叠面小，只取"余额阈值自动禁用"一项。
- **Agent 体验**：Cline 的 checkpoint 与 opencode C2 建议重复，合并排期；其余多为已对齐或低优先级。

**可移植清单（按优先级）**：
1. **D1** ✅ 已实施：按工具类型差异化截断（`tool_result_replay_truncation.go`）
2. **D2** ✅ 已实施：degraded 请求分类诊断（`synthetic_shell_result`，后端标记 + 请求明细徽标）
3. **D4** ✅ 已实施：请求阶段耗时（TTFB/总耗时，`artifacts.go` 桥接 + 请求明细列）
4. **D6** ✅ 已实施：低余额标红提示（`SupplierDetail.vue`；自动暂停渠道暂缓，涉及请求路径联动）
5. **D7** ⏸️ 关闭：linter 注入。网关不操作文件系统/无语言工具链，且 Cursor 客户端 Edit/Write 工具结果自带 lint 摘要（透传即达模型），无增量价值
6. **D3** 📋 已评估、暂缓：上下文压力膨胀。设计文档 `docs/superpowers/specs/2026-08-06-context-pressure-inflation-design.md`——机制可行（统计可隔离、缓存零影响），但收益依赖 Cursor 客户端黑盒压缩行为且与现有强制压缩重叠，建议真实反馈后再实验实施
7. **D5** 自适应历史预算（并入 max_tokens 恢复，远期）

---

## 4. 优化建议清单

| # | 参考项目/机制 | cursor-byok 现状 | 建议 | 目标文件 | 优先级 |
|---|---|---|---|---|---|
| D1 | cursor2api 工具结果差异化截断（Read 50/30、Bash 20/60、Search 70/15） | `tool_result_snip.go` 统一头尾各 50%（≥8KB 裁 4KB） | 按工具名选头/尾保留比例；默认保持现有统一比例，仅对 Read/Bash/Search 生效 | forwarder/tool_result_snip.go | 高 |
| D2 | cursor2api degraded 状态分类（工具假成功 / max_tokens 未续写 / 模型自述写到一半） | 终态诊断无统一 degraded 标记 | 请求明细/会话分析增加 degraded 枚举 + 原因，覆盖三类已知场景 | forwarder/types.go、server 请求明细、frontend 会话分析 | 高 |
| D3 | cursor2api 上下文压力膨胀（系数 1.35） | context_overflow 事后减半 + compaction | 可选：按供应商配置 input_tokens 报告系数，默认关闭；须评估 prefix-cache 与统计准确性 | agent/model/*.go、config | 中（默认关） |
| D4 | cursor2api 阶段耗时时间线 | 请求明细无阶段耗时 | ✅ 已实施：`artifacts.go` 在 `RecordLLMSummary` 把 summary 的 ttft_ms/duration_ms 暂存到 stream，`recordTurnUsageSnapshot` 落库（仅 completed），请求明细新增「耗时」列（首字 → 总） | forwarder/artifacts.go、token_usage.go、RequestMetrics.vue | 已完成 |
| D5 | cursor2api 自适应历史预算 | max_tokens_recovery 失败减半/2048 兜底 | 按当前 tool 数量预留下限，并入 max_tokens 恢复逻辑 | forwarder/max_tokens_recovery.go | 低（远期） |
| D6 | new-api 余额阈值自动禁用 | 有余额查询与健康冷却，无阈值联动 | ✅ 已实施（提示部分）：`SupplierDetail.vue` 余额徽标低阈值标红（USD<2/CNY<10/百分比<5%）；「自动暂停渠道」涉及请求路径联动，单机场景收益低，暂缓 | config、SupplierDetail.vue | 已完成（提示）/ 暂缓（自动禁用） |
| D7 | Cline linter 自修复（注入式） | 无 | 工具结果附 linter/编译错误摘要，提示模型优先修复 | forwarder/tool_result_*.go | 中 |

---

## 5. 实施约束与后续建议

- **沿用仓库既有约束**（IMPROVEMENT_TASKS.md）：不写测试；改 prompt/history/replay 须遵守 prefix-cache-stability（D1 仅改 snip 的保留比例，不改变触发时机与文案结构，缓存影响与现状一致；D3 修改上报 token 数，须单独评估统计与缓存影响）；不改已安装 Cursor 客户端。
- **验证方式**：`go build ./...` + `go vet ./...` 全量编译；前端 `npm run build`；手动联调 BYOK 网关场景回归。
- **建议实施顺序**：D1 → D2 → D4/D6 → D7 → D3 → D5。
- 本文档仅作分析与建议；实施按上表分阶段进行，每阶段独立验证。