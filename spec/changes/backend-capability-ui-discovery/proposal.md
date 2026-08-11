# Proposal: backend-capability-ui-discovery

## Why
镜像抓包能力已具备受限的高级设置入口，但 JSONL 中同一官方请求的请求、响应起始、流式片段与截断标记没有共同标识。并发或流式请求只能按时间猜测归属，无法作为后续 Cursor 对接的可靠对比依据。

## What
- 让前端配置归一化、持久化载荷和应用状态保留 `mirrorCapture`，新增只更新 `enabled` 的保存入口 | refs: R-1 | verify: 在浏览器预览中切换开关后，本地配置 payload 保留 hosts 且 `mirrorCapture.enabled` 与开关一致。
- 在“高级设置”增加默认关闭的镜像记录开关、记录位置和敏感内容警告，并复用现有保存、错误与重试反馈 | refs: R-1 | verify: 设置页可见开关；开启与关闭均显示结果，保存失败时恢复原值并显示可重试错误。
- 提供镜像抓包的只读状态：开关、本地服务、MITM、Cursor 代理设置、记录文件是否存在、大小与最后更新时间；状态区分未启用、未就绪、等待官方请求和已记录 | refs: R-2 | verify: 记录正文不在 DTO 或前端状态中；浏览器预览能显示模拟的未命中与已命中状态。
- 提供仅由用户点击触发的镜像记录目录打开入口，不读取、导出或在应用内展示 `official.raw.jsonl` 内容 | refs: R-2 | verify: Wails binding 只打开固定镜像目录；不返回文件内容。
- 同步新增中文源文案的 i18n 目录与三种非源语言翻译，并运行既有浏览器预览路径核对抓包状态交互 | refs: R-1, R-2 | verify: i18n 扫描、lint、构建与既有设置页预览冒烟检查通过。
- 为每个镜像 HTTP 交换生成仅本地使用的 `exchangeId`，使 `request`、`response_start`、`response_chunk` 与 `response_truncated` 记录可稳定关联 | refs: R-3 | verify: 同一代理上下文生成的每条记录携带同一 ID；不向上游 HTTP 请求添加该 ID。
- 为每条记录增加枚举 `phase`，并从官方请求已有正文或 Gemini URL 尽力提取 `model` | refs: R-3 | verify: 已知 OpenAI/Anthropic JSON `model` 和 Gemini `models/<name>` URL 可写入模型字段；缺失或畸形输入保留空字段且不阻断直通。

**Not in this change**: 不提供 `official.raw.jsonl` 的应用内浏览、导出、对比或 hosts 编辑；不为已移除的授权/设备接口或不支持的用量查询建立 UI；不改变脱敏、正文截断、镜像域名或代理直通语义；不自动修改 Cursor 代理或主动向官方 API 发起测试请求。

## How
- 选择现有 `Settings`、`appState`、`clientApi` 和 browser-preview mock 路径，而不创建新路由或直接调用 Wails binding。
- 状态合同遵循 `design.md`：代理桥接层从既有运行态和固定记录文件派生状态，正文绝不越过本地文件边界。
- 开关只变更 `mirrorCapture.enabled`，保留后端已有 hosts；默认关闭，记录继续仅存于本地调试目录。
- 先将配置数据流作为一个提交，再将高级设置和多语言文案作为第二个提交；后续将状态 binding、状态面板和规格台账分别提交；每个提交都可独立回退，不新增测试文件。
- 在镜像请求过滤器创建关联上下文写入 `goproxy.ProxyCtx.UserData`，并在响应过滤器读取它；记录器只消费该内部值，绝不修改请求头、URL 或正文。
- `model` 提取是旁路元数据：OpenAI/Anthropic 从请求 JSON 的顶层 `model` 读取，Gemini 从 URL 路径的 `models/<name>` 段读取；JSON 解析失败或字段缺失时留空。

## Risk
- 开启后会将提示词、响应和工作区上下文写入本地调试文件；开关旁明确路径和敏感性，默认关闭，不在应用内读取原文。
- 配置白名单漏传会在保存其他设置时清空开关或 hosts；归一化、payload、状态回填与既有浏览器预览路径共同验证保留语义。
- 本地测试只使用 preview mock，不能证明真实桌面代理已写记录；交付时单列桌面端人工验证，并可关闭开关或回退对应提交恢复。
- 文件存在只证明至少有一次写入，不证明当前每一条 Cursor 请求都会经过代理；状态文案明确为“已记录/等待请求”，不夸大为持续抓包成功。
- 打开目录受操作系统文件管理器可用性影响；失败不会影响代理或记录，回退对应目录入口提交即可恢复。
- JSONL 是调试文件而非稳定公共 API；新增字段保持向后兼容，既有消费者可忽略未知字段。若后续对比工具依赖这些字段，需将其版本化约束单独设计。
- `ProxyCtx.UserData` 可能被同一代理其他功能使用；镜像记录应使用具名上下文结构，并在只处理镜像域名的分支写入，避免覆盖 relay 流程的上下文数据。

<!-- APPROVED: 2026-08-12 02:02 (用户确认：全部使用推荐) -->
