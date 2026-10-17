# Cursor 3.22.12 与上游同步记录

日期：2026-10-16（Asia/Shanghai）。本地起点：`9bf446a0`，Go/Wails/Vue 架构。

## 前置说明与上游证据

用户已确认按本轮设计直接适配，保留架构与已有未提交改动。本轮执行 `git fetch upstream --prune`，上游由 `9a8fde279c701b47dae97b7a18ebe92fa0119013` 更新到 `31e9f5cbc98a7d28cffcb5efbb9323cd3e143354`。

- 检索命令/关键词：`git log 9a8fde27..upstream/main`、`git diff --stat 9a8fde27..upstream/main`。
- 访问日期：2026-10-16。
- 采用来源：配置的官方上游 `https://github.com/leookun/cursor-byok.git`，以及本轮拉取的 Git 对象。
- 采用原因：Git 对象能直接验证提交范围与实际文件差异，不依赖网页摘要或旧的远端缓存。
- 结果：区间新增 12 次提交，全部修改 README.md / README-CN.md，净增 4 行，主要为 BaoCode 项目介绍与链接；没有新增功能代码。

因此本轮没有直接合并 Rust/Tauri/React 上游，也没有把 README 宣传内容当作当前分支的新功能。本轮代码变化来自对实际安装版 Cursor 的兼容适配。

## 当前安装包与静态提取

只读检查的安装包：`D:\cursor`，版本 `3.22.12`，commit `3a92974361033b2051526321308c2740fe5912c0`。能力扫描成功，原有固定协议标记和浏览器工具标记仍存在。

原始输入 SHA-256：

| bundle | SHA-256 |
| --- | --- |
| cursor-always-local/dist/main.js | C392530DEA61FA3EB0F9E560001A5741A7D1F5317E676F254B472FBF7793FF11 |
| cursor-agent-exec/dist/main.js | 2A0E07B7E9479B856072F5FCC8A1E5B88E2218C8F5071D445F47938F44D62D64 |

`.cursor-app-formatted/` 是 9 月旧快照，未作为新版结论依据。安装包仅静态读取，没有加载执行其中的 JavaScript，没有修改客户端、签名、设置、凭据或进程。

## 变更说明

### 可重复的协议提取与生成

- webpack 导入、导出、CommonJS 转导出按每个模块的实际参数识别，支持新版变量名、逗号表达式导入及明确的入口 bootstrap 作用域。
- 导入赋值与 ESM getter 验证完整表达式，不把属性访问、再次调用或二元表达式截断为模块/类型本体。
- 静态解析 `static $p()` / `static $()` 紧凑描述，支持嵌套、标量、消息、枚举、optional、repeated、map 和 oneof。
- 枚举 helper 通过静态实现特征和真实模块导出识别，不绑定压缩模块 ID 或导出名字；直接调用必须使用所属模块的真实 require 参数。
- 相同全限定名的自定义类型必须结构一致，冲突消息或枚举会失败，避免生成树静默覆盖字段。Google 标准类型继续使用官方 proto，不生成客户端附带的重复运行时描述。
- 更新 `proto/from_extensions` 中四份已有协议源，再通过 `apply_tool` 生成编译输入，并用 protoc 重建 Go 类型。
- `apply_tool` 显式保留本地仍需使用的 10 个旧会话顶层类型、4 个仓库状态顶层类型和 `ModelDetails` 的四个上下文字段；精确校验嵌套路径、字段号、类型和 optional 语义，复用字段号时拒绝生成。

协议原始源与兼容编译输入有所区别：旧类型只由经过审计的 `proto/apply_tool/compatibility/*.proto.inc` 补入，没有伪装成新版安装包仍包含这些类型。兼容规则重复运行保持幂等。

### MCP 结果语义

- `needs_auth=true` 转为模型可见的认证要求，同时保留原始错误；没有自动认证或自动重试。
- MCP 服务状态筛选保留 `error_message` 和未知字段，目录及定向查询均能显示诊断，并沿用内容长度预算。
- 明确选择服务后，即使指定工具/正则没有匹配到工具，也保留该目标服务的已知错误；其他服务和不匹配工具仍被过滤。

### Canvas 路径与保存状态

- 保留旧 `.cursor/projects/.../canvases` 路径，并适配实际客户端的 agent store 和 UUID workfile 路径。
- 保持原有 TypeScript 检查输出，额外反馈保存状态。`pending` 表示校验通过且异步发布中；无效路径、不可用、过大、类型检查失败、编译失败明确表示未保存；未知状态按原值展示。
- 状态、标题、详情净化控制字符与空白，按原生 UTF-16 长度预算 100/200/500 截断，并保留完整 Unicode 字符。
- 不根据 `canvas_id` 构造未经验证的地址，也不把文件写入成功当成发布成功。

### Shell 与历史兼容

- 执行请求和工具卡片使用已恢复的结构化 `ShellOutputNotificationConfig`，沿用现有配置构造函数。
- 历史读取同时兼容结构化对象与旧 protobuf bytes 的 Base64 表示，覆盖字段别名及标准/URL 字母表、有无 padding。
- 已有对象保留原字节；旧 bytes 仅替换目标值，不改其他大整数、空白、键序或输入切片，不写回历史。非法内容交给原有错误路径处理。
- 重复回放与追加下一轮的旧消息/转录前缀稳定性已有独立审查用例验证。

### 能力清单

补充新版 Adopt、跨代理、PR 代码导览和独立 Canvas ToolCall 协议项，并删除已消失的 AbortArgs 清单项。新增类型标记为协议支持或本地尚无发送路径，未宣传为已实现的本地工具。现有文件编辑加 Canvas 诊断路径继续有效。

## 验证说明

真实安装包严格提取结果：

| 输入 | 消息 | 枚举 | 服务 | 字段解析 | skipped / unresolved / placeholders |
| --- | ---: | ---: | ---: | ---: | --- |
| cursor-always-local | 6031 | 576 | 25 | 20554 / 20554 | 0 / 0 / 0 |
| cursor-agent-exec | 4024 | 374 | 2 | 13630 / 13630 | 0 / 0 / 0 |

always-local 的六份输出经过多次提取，文件集合及逐文件 SHA-256 完全一致；全部生成 proto 经语法和依赖解析。当前四份受版本控制的 raw 与真实提取输出一致，编译输入与 `apply_tool` 输出一致。

新增行为均经过失败回归后修复。独立审查发现的冲突类型覆盖、伪 require、ESM 返回值截断，以及 MCP 定向查询诊断遗漏均已修复并复测。

本机还存在旧路径的两份 Connect 生成产物，最初全仓检查因此引用了已移除的类型。已先备份，再用同一协议源和 `paths=source_relative` 重新生成；标准 module 路径的生成产物也已刷新。`gen/` 属于忽略的构建产物，不代替受版本控制的协议源与生成规则。

最终验证结果：

| 检查 | 结果 |
| --- | --- |
| `go test -p 1 ./...` | 通过，退出码 0 |
| `go build ./...` | 通过，退出码 0 |
| `go vet ./...` | 通过，退出码 0 |
| `go test ./proto/ext_tool ./proto/apply_tool -count=1` | 通过；包含严格拒绝、兼容片段、字段冲突及幂等回归 |
| exec / forwarder 定向与整包回归 | 通过；包括 MCP、Canvas、Shell 及能力清单 |
| 独立审查原缺陷用例与历史前缀稳定性用例 | 全部通过，无剩余审查问题 |
| 四份 raw 与真实提取输出、两份编译输入与生成器输出 | SHA-256 一致 |
| 安装包输入 SHA-256 | 与开始调查时一致 |
| `git diff --check` | 通过 |

全仓测试曾因陈旧 Connect 产物及旧清单测试仍要求已经消失的 AbortArgs 而失败。前者已重新生成，后者已按真实协议更新为独立 Canvas 展示类型分类及已删除项不得残留的断言，随后完整重跑通过。没有忽略失败测试或把未验证项标记成功。

## 风险、边界与回滚

- 证据覆盖源码、真实安装包的静态协议提取、受控本地测试、编译和静态检查。未建立真实 Cursor 会话，未连接真实供应商或 MCP 服务，未实际认证或发布 Canvas；没有真实客户端端到端成功的结论。
- 新增远程代理、云端服务、独立 Canvas 工具等协议类型不等于本地功能已经实现；以能力清单为准。
- 任务开始前的 10 个已修改文件均通过 SHA-256 核对，内容保持不变；没有暂存、提交、推送或发布，也没有清理 `.claude/`、`.codework/` 或既有 External API 目录。
- 默认 Go 缓存不可写时改用仓库内 `.analysis-tmp/go-cache`；Git 拉取与 protoc 使用了本机工具所需的授权执行，未改变应用行为。
- 回滚只撤销本轮协议提取器、生成规则、协议源、运行时适配及文档的补丁，再从对应协议源重新生成 Go 产物。不要整仓 reset，也不要覆盖任务前的未提交文件。旧生成路径的两份文件备份位于 `.analysis-tmp/cursor-20261016-generated-backup/`；本轮源文件基线与初始改动哈希位于 `.analysis-tmp/cursor-20261016-baseline/`。
