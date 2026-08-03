## v0.0.80

### 新功能
- **Task 子代理授权模型升级**：新增 `access_mode`（inspect 只读 / act 可写）显式授权，替代旧的 readonly 布尔参数；子代理类型与权限组合不合法时直接拒绝，避免隐式默认权限
- **inspect 只读子代理的 Shell 强制白名单**：服务端在派发前强制校验 Shell 命令，仅允许只读 git 证据命令（status/diff/log/show/blame 等）、进程/端口查询（tasklist/netstat/ps/ss/lsof）与文件哈希；拒绝管道、重定向、变量、命令替换，工作目录必须限定在会话工作区内，并自动注入 `--no-pager --no-optional-locks` 保护参数
- **Shell 拒绝指纹熔断**：同一确定性拒绝（权限/策略/能力类）在同一轮内达到阈值后自动熔断，本轮剩余时间 Shell 不可用，改为引导模型使用 Read/Grep/Glob 或上报阻塞；Shell 成功执行则自动重置，防止模型对同一错误命令无限重试造成 UI 刷屏
- **构建产物注入 Git 提交号**：构建时写入源码 commit 标识，诊断信息可精确追溯版本对应的源码提交

### 修复
- **工具参数 JSON 解析容错**：部分模型流式输出工具参数时重复拼接多份对象草稿、Windows 路径反斜杠漏转义导致调用直接失败；现已自动取最后一个完整对象并修复非法转义后恢复
- **手动连接 MCP 服务器失效**：显式连接 MCP 服务器不再受扫描总开关影响（总开关只控制是否注入 agent 会话），仍尊重 mcp.json 配置级 enabled 与显式禁用列表
- **Skill 编辑器请求 404**：baseURL 以 `/v1` 结尾时与 endpoint 的 `/v1/` 前缀重复拼接成 `/v1/v1/...`；已复用主流程端点拼接规则
- **历史会话标题为空**：兼容 Cursor context.json 新旧格式（新格式用户消息在 payload.text，旧格式在 content），request_context 等注入条目自动跳过
- **更新清单校验加固**：update.json 资产字段完整性校验（URL 非空、大小为正、sha256 校验和合法），防止损坏清单进入下载流程；Linux 平台资产未发布时静默跳过而非误报更新失败
- **Windows 提权路径安全加固**：certutil / powershell 改走 SystemRoot 绝对路径定位，防止 PATH 环境变量注入劫持证书安装提权流程；MCP 服务器启动与工具发现进程隐藏控制台窗口，避免弹黑框

### 优化
- **委派任务条改为平铺卡片**：最多 4 条任务两列平铺展示（此前为单条定时轮换），任务状态一目了然；首页改为独立滚动区域
- **模型编辑器供应商选择升级**：供应商模板从下拉菜单改为带图标的网格卡片，快速辨识与切换；「协议与高级参数」区域样式重构
- **供应商详情页编辑入口修复**：从模型配置跳转 `?edit=1` 改用 watch 监听路由，解决同一组件实例复用时不展开批量编辑面板的问题
- **设置侧边栏层级强化**：分组标题增加图标、选中项增加左侧绿色强调条
- **历史会话树优化**：年/月/日改为文件夹图标展示，并显示各层级的会话数量
- **新增单元测试**：覆盖只读 Shell 策略、熔断账本、历史解析、更新器清单校验、工具参数恢复等核心逻辑

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-0.0.80-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-0.0.80-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Win 电脑）**：下载 `cursor-byok-0.0.80-windows-arm64-installer.exe` 或 `cursor-byok-0.0.80-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-0.0.80-windows-x32-installer.exe` 或 `cursor-byok-0.0.80-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-0.0.80-macos-arm64.dmg`
- **macOS Intel**：下载 `cursor-byok-0.0.80-macos-x64.dmg`
- **Linux 64 位**：下载 `cursor-byok-0.0.80-linux-x64.tar.gz`

**如何判断自己的 Windows 是多少位**：右键「此电脑」→「属性」，看「系统类型」显示"64 位操作系统"就下载 x64，显示"32 位"才下载 x32。macOS 可在「关于本机」查看芯片是 Apple 还是 Intel。

### macOS DMG 授权与首次打开
当前版本构建流程只给 `.app` 使用 ad-hoc 临时签名，未执行 Apple Developer ID Application 正式签名、未公证，也没有装订 Apple 公证票据。因此，首次打开时如果 macOS 阻止启动，请在 Finder 中右键应用选择"打开"并确认；仍被拦截时，到"系统设置 → 隐私与安全性"中点击"仍要打开"。仅对确认来源可信的文件执行此操作。

正式对外分发需要：
1. 使用 Apple Developer Program 的 **Developer ID Application** 证书签名 `.app`；若分发 `.pkg`，还需要 **Developer ID Installer** 证书
2. 使用 `xcrun notarytool submit` 提交公证，凭据应通过 GitHub Secrets 或 App Store Connect API Key 提供
3. 公证成功后使用 `xcrun stapler staple` 将票据装订到 `.app`，再把已签名、已公证的 `.app` 放入 DMG
4. 发布前用 `codesign --verify`、`spctl --assess` 和 `xcrun stapler validate` 验证签名、公证和票据

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」后选择「仍要运行」。