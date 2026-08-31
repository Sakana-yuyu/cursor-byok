## v1.0.3

### 新功能

- **请求日志入口**：侧边栏和首页“更多”菜单恢复请求明细入口，可查看模型、供应商、Token、耗时、状态和费用。
- **绘图工具兼容**：OpenAI Responses 请求识别 `image2` 等绘图工具别名，并正确启用原生 `image_generation`。
- **失败模型自动停用**：模型测试失败自动停用配置可完整保存、读取和缓存，避免失效渠道继续参与路由。

### 修复

- **排队对话自动续跑**：当前对话完成后会正确进入下一条排队对话，补齐最终回复，避免界面停在等待状态。
- **官方模型选择器**：恢复官方原版模型列表和选择器结构，已选模型同时显示模型名称与思考强度。
- **Explorer Git 检查**：只读 Shell 策略可重复识别 `--no-pager --no-optional-locks`，避免合法 Git 检查刷出 `Skipped git` 并触发子代理熔断。
- **请求日志菜单**：清除首页重复的“请求明细”菜单项，并补充浏览器回归检查。

### 优化

- **原生 Goal 能力**：移除旧的自研 Goal 页面和设置，统一使用 Cursor 原生 Goal 能力。
- **会话标题**：针对绘图、模型和修复类请求生成更简洁的历史会话标题。
- **多语言资源**：同步清理失效文案并补齐当前界面的多语言目录。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.3-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.3-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.3-windows-arm64-installer.exe` 或 `cursor-byok-1.0.3-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.3-windows-x32-installer.exe` 或 `cursor-byok-1.0.3-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.3-macos-arm64.dmg` 或 `cursor-byok-1.0.3-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.3-macos-x64.dmg` 或 `cursor-byok-1.0.3-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.3-linux-x64.tar.gz`
