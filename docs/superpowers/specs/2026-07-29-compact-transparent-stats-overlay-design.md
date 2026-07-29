# 紧凑透明统计浮窗设计

## 目标

将统计浮窗从 240x120 缩短到 240x104，使窗口高度只包裹顶部状态行和两排指标盒；窗口底层透明，并保持桌面置顶显示。

## 外观

- 原生窗口尺寸固定为 240x104。
- 保留“实时统计”文字和刷新状态圆点，作为状态提示及可拖动区域。
- 窗口页面和 WebView 背景完全透明，不显示整块深色矩形底板。
- 四个指标盒继续采用现有主题的深色样式，并改为略带透明度，使桌面背景能轻微透出。
- 保留 2x2 排列，不缩小现有指标文字，避免可读性下降。
- 收紧外层垂直内边距和盒子间距，内容完整落在 104px 内。

## 原生窗口

- 使用 Wails `BackgroundTypeTransparent`。
- `BackgroundColour` alpha 设为 0，避免 WebView 初始化阶段闪现不透明底色。
- 保持 `Frameless: true`、`AlwaysOnTop: true`、`DisableResize: true`。
- Windows 继续使用 `HiddenOnTaskbar: true` 和 `DisableFramelessWindowDecorations: true`。
- 重复点击首页“浮窗”按钮继续切换显示和隐藏。

## 验证

- Go production 构建通过。
- 前端 production 与 browser-preview 构建通过。
- 浏览器视觉检查确认 240x104 区域内四个盒子及状态行完整显示，无文字截断和页面溢出。
- 最终 Windows 压缩包重新生成，且包含透明窗口参数和最新前端 bundle。

## 约束

- 不新增测试文件。
- 不修改已安装 Cursor 客户端。
- 不改统计数据来源和刷新周期。
- 不提交或推送 Git 变更，除非用户明确要求。
