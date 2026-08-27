## v1.0.1

### 新功能

- **备用密钥池（多密钥轮换）**：模型编辑器新增「备用密钥」输入（每行一把）。同一渠道的多把密钥在请求时自动轮换，且每把密钥是独立的冷却单元——单把密钥被限流/失效只冷却该密钥，不影响同渠道其他密钥与供应商继续服务。

### 修复

- **模型分组页分组偏好**：修复分组方式（名称 / 连接）在非响应式存储中读取导致 computed 缓存失效时机错误的问题。
- **交互细节**：模型配置页左栏统计文字不再与「新增模型」按钮重叠（按钮改为紧凑 ＋ 号）；移除供应商面板顶部冗余的「← 返回」；顶部标题栏不再重复显示与侧边栏相同的页面名称。

### 优化

- **启动更快**：首屏初始化由 5 段串行 await 改为并行执行，并移除首页指标固定 600ms 的人为加载延迟；非中文语言包加载不再阻塞入口。
- **运行更省**：本地缓存持久化拆分为「配置 + 运行状态」两层快照并对相同内容跳过写盘，消除轮询字段反复触发全量序列化的主线程抖动；首页磁盘占用统计轮询由 10s 降为 60s 且后端增加 15 秒 TTL 缓存（清理后立即失效）；悬浮窗位置检查在窗口隐藏时跳过，指标轮换定时器按需启停；页面指标轮询补齐窗口隐藏守卫。
- **首屏更轻**：缓存命中率环形图改用纯 SVG 实现，移除 chart.js 双图表库依赖（约 -200KB 依赖体积）。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.1-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.1-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.1-windows-arm64-installer.exe` 或 `cursor-byok-1.0.1-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.1-windows-x32-installer.exe` 或 `cursor-byok-1.0.1-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.1-macos-arm64.dmg` 或 `cursor-byok-1.0.1-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.1-macos-x64.dmg` 或 `cursor-byok-1.0.1-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.1-linux-x64.tar.gz`
