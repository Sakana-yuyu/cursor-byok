# v0.0.70 发布说明

## 🐛 修复贴边胶囊被裁剪问题

### 问题
贴左/右/上/下边时竖向/横向胶囊只显示一半，另一半被裁剪到屏幕边缘外。

### 根因
两个冲突的定位逻辑：
- **Go 定位**：左边吸附时窗口 `x = screenLeft`（屏幕边缘）
- **CSS transform**：左边吸附时 `translateX(-14px)`，将胶囊向屏幕外推 14px

结果：窗口在边缘，CSS 又把内容推出边缘 → 一半被裁剪

### 修复
在 Go 窗口定位时为 CSS transform 预留 14px 偏移空间：

| 边缘 | 修复前 | 修复后 |
|---|---|---|
| 左 | `x = screenLeft` | `x = screenLeft + 14` |
| 右 | `x = screenRight - width` | `x = screenRight - width - 14` |
| 上 | `y = screenTop` | `y = screenTop + 14` |
| 下 | `y = screenBottom - height` | `y = screenBottom - height - 14` |

### 效果
- 贴边胶囊完整显示，不再被裁剪
- "轻微内收"视觉效果保留（CSS transform 仍生效，但有了预留空间）
- 悬停展开时面板位置正确

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。
