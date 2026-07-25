## v0.0.48

### 修复
- **Multitask Mode 请求排队**：`BidiAppend` 中 `streamCommandRun` 改用异步发送（`postStreamCommandAsync`），不再阻塞等待模型调用完成。主进程可以在模型生成期间继续接收新消息，实现 Multitask Mode 的并发对话。
- **Use Multiple Models 不生效**：`conversation_action` 处理器从现有 stream 复制字段时遗漏了 `SubagentModelOverrides`，导致 ExecutePlanAction 等场景下子代理全部使用父进程模型。现已修复，确保所有 run 路径都保留用户选择的模型覆盖。
- **commit message 生成闪退**：修复 MITM streaming 转发中 `http.Response.Request` 为 nil 导致 goproxy panic。

### 新功能
- **会话分析详情页**：首页统计卡片「详情」按钮，支持时间范围 / 模型筛选、ECharts 折线图。

### 优化
- **首页指标卡片**：改为前端自主聚合，不再依赖后端全量汇总。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。这是 Windows 对未购买代码签名证书的应用的标准行为，程序本身安全。