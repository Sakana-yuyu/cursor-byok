# Proposal: backend-capability-ui-discovery

## Why
镜像抓包能力已具备受限的高级设置入口，但 JSONL 中同一官方请求的请求、响应起始、流式片段与截断标记没有共同标识。并发或流式请求只能按时间猜测归属，无法作为后续 Cursor 对接的可靠对比依据。

同时，界面把 `upstream` 描述为“绕过本地代理服务”，但已启动服务时 Cursor 仍被写入本地 `http.proxy`，MITM 也没有读取 `routing.mode` 来选择请求去向。这个语义偏差会让用户无法判断当前是完全官方直连，还是仍经本地 MITM 进行官方上游镜像，进而误判抓包就绪状态。

## What
- 让前端配置归一化、持久化载荷和应用状态保留 `mirrorCapture`，新增只更新 `enabled` 的保存入口 | refs: R-1 | verify: 在浏览器预览中切换开关后，本地配置 payload 保留 hosts 且 `mirrorCapture.enabled` 与开关一致。
- 在“高级设置”增加默认关闭的镜像记录开关、记录位置和敏感内容警告，并复用现有保存、错误与重试反馈 | refs: R-1 | verify: 设置页可见开关；开启与关闭均显示结果，保存失败时恢复原值并显示可重试错误。
- 提供镜像抓包的只读状态：开关、本地服务、MITM、Cursor 代理设置、记录文件是否存在、大小与最后更新时间；状态区分未启用、未就绪、等待官方请求和已记录 | refs: R-2 | verify: 记录正文不在 DTO 或前端状态中；浏览器预览能显示模拟的未命中与已命中状态。
- 提供仅由用户点击触发的镜像记录目录打开入口，不读取、导出或在应用内展示 `official.raw.jsonl` 内容 | refs: R-2 | verify: Wails binding 只打开固定镜像目录；不返回文件内容。
- 同步新增中文源文案的 i18n 目录与三种非源语言翻译，并运行既有浏览器预览路径核对抓包状态交互 | refs: R-1, R-2 | verify: i18n 扫描、lint、构建与既有设置页预览冒烟检查通过。
- 为每个镜像 HTTP 交换生成仅本地使用的 `exchangeId`，使 `request`、`response_start`、`response_chunk` 与 `response_truncated` 记录可稳定关联 | refs: R-3 | verify: 同一代理上下文生成的每条记录携带同一 ID；不向上游 HTTP 请求添加该 ID。
- 为每条记录增加枚举 `phase`，并从官方请求已有正文或 Gemini URL 尽力提取 `model` | refs: R-3 | verify: 已知 OpenAI/Anthropic JSON `model` 和 Gemini `models/<name>` URL 可写入模型字段；缺失或畸形输入保留空字段且不阻断直通。
- 镜像请求 URL 对凭据型查询参数做本地脱敏，保留端点路径和非敏感查询项 | refs: R-4 | verify: `key`、`api_key`、`token`、`secret`、`signature` 与密码等值不出现在 JSONL，原始 HTTP 请求 URL 不被改写。
- 将代理请求分流显式接入 `routing.mode`：本地服务模式维持既有 Cursor relay 到 backend；官方上游模式仅在用户未启动服务时允许 Cursor 不经本地代理，在服务运行且镜像记录开启时保留 MITM 直通官方模型 API 的抓包路径 | refs: R-2 | verify: 启动服务后，状态与文案能明确说明 Cursor 是否仍经本地 MITM；停止服务或不注入代理时，不夸大为可抓包。
- 为 `cmd/isolated-cursor-e2e` 增加默认关闭、仅在 `CURSOR_E2E_MIRROR_CAPTURE=1` 时启用的官方上游镜像模式；启用时使用隔离 `history`、隔离配置管理器且不注入本地伪账号，并输出绝对镜像记录路径 | refs: R-2 | verify: 未设置环境变量时仍以空记录根和 `nil` 镜像配置启动；设置后隔离配置启用镜像和官方上游分流，输出的 `mirror_record` 位于 `isolated_root` 内，启动器不主动调用官方 API。
- 隔离镜像模式将 `api2.cursor.sh`、`api3.cursor.sh` 与 `api4.cursor.sh` 加入临时镜像 hosts，并仅向临时 Cursor 子进程传入本次隔离 CA 的 Chromium SPKI 信任参数 | refs: R-2 | verify: 隔离 Cursor 不再因临时 MITM CA 报 `ERR_CERT_AUTHORITY_INVALID`；Cursor relay 请求在临时 JSONL 中形成可关联记录；默认模式、真实 Cursor 设置、系统证书库和默认镜像 hosts 不变。

**Not in this change**: 不提供 `official.raw.jsonl` 的应用内浏览、导出、对比或 hosts 编辑；不为已移除的授权/设备接口或不支持的用量查询建立 UI；不改变普通运行模式的脱敏、正文截断、镜像域名或代理直通语义；不自动修改真实用户的 Cursor 代理或系统证书库。

## How
- 选择现有 `Settings`、`appState`、`clientApi` 和 browser-preview mock 路径，而不创建新路由或直接调用 Wails binding。
- 状态合同遵循 `design.md`：代理桥接层从既有运行态和固定记录文件派生状态，正文绝不越过本地文件边界。
- 开关只变更 `mirrorCapture.enabled`，保留后端已有 hosts；默认关闭，记录继续仅存于本地调试目录。
- 先将配置数据流作为一个提交，再将高级设置和多语言文案作为第二个提交；后续将状态 binding、状态面板和规格台账分别提交；每个提交都可独立回退，不新增测试文件。
- 在镜像请求过滤器创建关联上下文写入 `goproxy.ProxyCtx.UserData`，并在响应过滤器读取它；记录器只消费该内部值，绝不修改官方请求头、URL 或正文。镜像 JSONL 写入前只脱敏记录副本的凭据型查询参数。
- `model` 提取是旁路元数据：OpenAI/Anthropic 从请求 JSON 的顶层 `model` 读取，Gemini 从 URL 路径的 `models/<name>` 段读取；JSON 解析失败或字段缺失时留空。
- 隔离镜像模式从临时 CA 证书计算 DER `RawSubjectPublicKeyInfo` 的 SHA-256，再以 Base64 形式传入 Chromium 的 `--ignore-certificate-errors-spki-list`；该白名单只覆盖本次生成的 CA。临时配置 hosts 在原有 hosts 基础上合并当前安装客户端可见的 `api2`、`api3`、`api4` Cursor relay 域名，普通配置与默认模式均不变。

## Risk
- 开启后会将提示词、响应和工作区上下文写入本地调试文件；开关旁明确路径和敏感性，默认关闭，不在应用内读取原文。
- 配置白名单漏传会在保存其他设置时清空开关或 hosts；归一化、payload、状态回填与既有浏览器预览路径共同验证保留语义。
- 本地测试只使用 preview mock，不能证明真实桌面代理已写记录；交付时单列桌面端人工验证，并可关闭开关或回退对应提交恢复。
- 文件存在只证明至少有一次写入，不证明当前每一条 Cursor 请求都会经过代理；状态文案明确为“已记录/等待请求”，不夸大为持续抓包成功。
- 打开目录受操作系统文件管理器可用性影响；失败不会影响代理或记录，回退对应目录入口提交即可恢复。
- JSONL 是调试文件而非稳定公共 API；新增字段保持向后兼容，既有消费者可忽略未知字段。若后续对比工具依赖这些字段，需将其版本化约束单独设计。
- 请求 URL 的 query string 可能携带 API key 或签名；记录器必须在落盘前脱敏常见凭据型参数。该脱敏只影响 JSONL 副本；回退本次提交会恢复此前记录格式，但不建议在共享或备份含旧记录的目录中保留明文 URL 凭据。
- `ProxyCtx.UserData` 可能被同一代理其他功能使用；镜像记录应使用具名上下文结构，并在只处理镜像域名的分支写入，避免覆盖 relay 流程的上下文数据。
- `upstream` 的旧文案可能使用户误以为已彻底清除本地代理；实现前需先以请求分流和 Cursor 设置写入的真实状态定义语义，再同步更正文案与抓包就绪判定。回退对应分流提交可恢复现有本地 relay 行为，但不应恢复“绕过代理”的错误承诺。
- Chromium SPKI 白名单仅应放行临时 CA 公钥；不得使用全局忽略证书错误的启动参数，也不得修改 Windows 证书库。若隔离 Cursor 未能使用该参数，记录真实错误并关闭临时实例；回退对应启动器提交即可恢复此前隔离启动行为。

<!-- APPROVED: 2026-08-12 00:00 -->
