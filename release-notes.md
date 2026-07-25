## v0.0.47

### 修复
- **commit message 生成闪退**：修复 MITM streaming 转发中 `http.Response.Request` 为 nil 导致 goproxy MITM TLS 路径 panic 的问题。Cursor 客户端在请求生成 commit message 时不再报 `[aborted] socket hang up`。
- 移除有缺陷的 `forwardToServerStreaming` chunked encoding 路径，commit message 请求改用同步转发，确保 Connect RPC 响应格式正确。

### 新功能
- **会话分析详情页**：首页统计卡片「详情」按钮，支持时间范围 / 模型筛选、ECharts 折线图。

### 优化
- **首页指标卡片**：改为前端自主聚合，不再依赖后端全量汇总。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。这是 Windows 对未购买代码签名证书的应用的标准行为，程序本身安全。