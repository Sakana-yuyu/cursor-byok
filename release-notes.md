## v0.0.79

### 修复
- **添加/保存模型配置报错**：配置保存路径调用了一个在历史重构中丢失定义的校验函数（`validateConfigPayload is not defined`），导致拉取模型目录、添加模型适配器时直接报错；已恢复该函数并与当前路由模式归一化逻辑对齐
- **Cursor 账号功能不可用**：账号状态查询、登录、退出三个接口在桌面端引用未导入的绑定函数（`GetCursorAccountStatus is not defined` 等），首页「Cursor 控制面账号」卡片全部失效；已补全绑定导入并统一走桌面/预览双模式调用

### 优化
- **新增 ESLint 未定义标识符扫描**：配置 `eslint`（`npm run lint`）覆盖全部前端源码与 Vue 组件，`no-undef` 规则可从静态层阻止「调用存在、定义缺失」类回归；此前两处此类问题已全部修复并纳入检查
- **清理仓库遗留临时文件**：移除已弃用的 acorn 扫描脚本及其输出、GitHub Actions job 查询缓存等未跟踪临时文件

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-0.0.79-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-0.0.79-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Win 电脑）**：下载 `cursor-byok-0.0.79-windows-arm64-installer.exe` 或 `cursor-byok-0.0.79-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-0.0.79-windows-x32-installer.exe` 或 `cursor-byok-0.0.79-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-0.0.79-macos-arm64.dmg`
- **macOS Intel**：下载 `cursor-byok-0.0.79-macos-x64.dmg`
- **Linux 64 位**：下载 `cursor-byok-0.0.79-linux-x64.tar.gz`

**如何判断自己的 Windows 是多少位**：右键「此电脑」→「属性」，看「系统类型」显示"64 位操作系统"就下载 x64，显示"32 位"才下载 x32。macOS 可在「关于本机」查看芯片是 Apple 还是 Intel。

### macOS DMG 授权与首次打开
当前版本构建流程只给 `.app` 使用 ad-hoc 临时签名，未执行 Apple Developer ID Application 正式签名、未公证，也没有装订 Apple 公证票据。因此，首次打开时如果 macOS 阻止启动，请在 Finder 中右键应用选择"打开"并确认；仍被拦截时，到"系统设置 → 隐私与安全性"中点击"仍要打开"。仅对确认来源可信的文件执行此操作。

正式对外分发需要：
1. 使用 Apple Developer Program 的 **Developer ID Application** 证书签名 `.app`；若分发 `.pkg`，还需要 **Developer ID Installer** 证书
2. 使用 `xcrun notarytool submit` 提交公证，凭据应通过 GitHub Secrets 或 App Store Connect API Key 提供
3. 公证成功后使用 `xcrun stapler staple` 将票据装订到 `.app`，再把已签名、已公证的 `.app` 放入 DMG
4. 发布前用 `codesign --verify`、`spctl --assess` 和 `xcrun stapler validate` 验证签名、公证和票据

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」后选择「仍要运行」。