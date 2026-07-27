## v0.0.54

### 修复
- **批量添加模型成功后自动关窗**：与「快速添加」一致，批量添加成功后关闭模型编辑窗口，避免停留在已完成的编辑页。

### 新功能
- **供应商卡片显示备注**：卡片标题区展示同组模型中出现最多的非空「备注」（tooltipData），悬停可看全文。
- **模型测速/可用性结果持久化**：终态测速结果写入 `model-adapter-test-results.json`，重启后仍可展示可用性摘要（不落盘大段 raw 响应）。
- **请求明细错误态更完整**：失败记录写入并展示供应商（groupName/baseUrl）与错误码（HTTP 状态或 terminal code；旧数据可回退 status）。

### 优化
- **错误码提取更稳健**：从 HTTPStatusError、错误文本中的 `status=` 与 TerminalCode 解析可展示码，压缩与主路径 provider_error 一并覆盖。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。