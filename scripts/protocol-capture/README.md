# 镜像抓包解码脚本

隔离保真抓包（`cmd/isolated-cursor-e2e` + `internal/mitm` 保真记录器）会写出两份 JSONL：

| 文件 | 内容 |
|------|------|
| `_debug/mirror/official.raw.jsonl` | 每条请求/响应的 Base64 原始字节 + 结构摘要 |
| `_debug/mirror/protocol.timeline.jsonl` | 只含结构字段的协议时间线索引 |

记录器**故意不落解码后的正文**，所以看协议细节要靠这里的两个脚本。抓包产物本身含完整对话正文，
不要提交进 git；这两个脚本只有工具逻辑，路径全部走参数。

## 第一遍：结构直方图（不需要 protoc）

```powershell
python scripts/protocol-capture/summarize.py <official.raw.jsonl> <out_dir>
```

产出 `summary.json`（各 kind / decodeError / content-encoding 的直方图与 URL 计数）和
`frames-index.jsonl`（每个 RunSSE 帧的行号定位）。先看这一遍再决定要解哪些帧。

## 第二遍：解成带字段名的文本（需要 protoc）

```powershell
protoc --include_imports --descriptor_set_out=$env:TEMP\agent_v1.desc -I proto proto/agent_v1.proto
python scripts/protocol-capture/decode.py <official.raw.jsonl> <out_dir> $env:TEMP\agent_v1.desc agent_v1.proto
```

产出 `server-frames.txt`（`agent.v1.AgentServerMessage`）与 `client-messages.txt`
（`aiserver.v1.BidiAppendRequest` 内层的 `agent.v1.AgentClientMessage`）。
第 5 个可选参数是逗号分隔的跳过列表，默认过掉纯增量帧（`text_delta` 等）。

## 驱动隔离实例产生流量

`cmd/isolated-cursor-e2e` 只负责拉起隔离的 Cursor，**不驱动对话**，也接不上 Playwright
（Electron 需要 `--remote-debugging-port`，启动器不传这个参数）。流量要靠 `cursor-e2e-drive.ps1`
做 Win32 键鼠注入。

```powershell
# 先确认目标窗口位置，pid 取自 isolated-cursor-e2e 启动日志的 cursor_pid
powershell -File scripts/protocol-capture/cursor-e2e-drive.ps1 -TargetPid <pid> -Action rect

# 一次性完成：聚焦 -> 点输入框 -> 粘贴 -> 回车。X/Y 是窗口矩形的比例
powershell -File scripts/protocol-capture/cursor-e2e-drive.ps1 -TargetPid <pid> -Action send-prompt -X 0.5 -Y 0.93 -Text "修复 app.py 里的 off-by-one"
```

**这个脚本会向前台窗口注入键鼠事件，用错 pid 就会打进你自己的 Cursor。** 抓包时屏幕上通常
同时有两个 Cursor 窗口、标题都是 "Cursor Agents"，肉眼无法区分，唯一的防线是 `TargetPid` 校验：
除 `raw-click` 外的每个动作在注入前都会确认目标窗口或前台窗口属于 `TargetPid`，不满足就抛
`SAFETY_ABORT` 中止。2026-08-13 那次抓包里这个保护实际触发了 3 次（用户的 Cursor 抢到焦点），
没有发生误输入。

`raw-click` 是唯一的例外——它只校验坐标落在目标窗口矩形内，不校验前台归属，若有窗口覆盖在
目标之上，点击会落到那个窗口。仅在确认目标未被遮挡时使用。

中文文本不要用 `-Action type`：SendKeys 会被输入法截走，字符进不了输入框。用 `paste` /
`send-prompt`，它们走剪贴板 `Ctrl+V`。

## 两条容易踩的坑

**1. 先核对字段号，再相信解码结果。** 曾经 `proto/agent_v1.proto` 的 `ToolCall` oneof 缺
`send_to_user`(58) / `pi_*`(61–67) / `search_conversations`(69) / `create_goal`(70) / `update_goal`(71)，
用它解码会把这些 arm 静默丢进 unknown fields，看起来像「没采到」。

该缺口已修复。2026-08-13 复核当前仓库状态：`proto/agent_v1.proto`、
`proto/from_extensions/agent_v1.proto` 和 `gen/agentv1` 三者都带上述字段号；两份 proto 现在只差
`go_package` 与 3 处被解析成具体消息类型的字段（`cursor_position` / `file_not_found` / `files`），
`proto/agent_v1.proto` 更完整且是 `gen/` 的来源，优先用它做 descriptor set。
换 proto 或换分支后请重新核对字段号，别沿用旧结论。

**2. gzip 请求体。** Cursor 会用 `Content-Encoding: gzip` 发 BidiAppend 请求。记录器现在自己解压，
摘要字段完整；`decode.py` 里的 gunzip 兜底是给修复之前的旧抓包用的——那些记录的
`protocol.decodeError` 是 `unsupported_content_encoding`，但 `bodyBase64` 仍是完整原始字节，
在外部解压后可以照常解码。
