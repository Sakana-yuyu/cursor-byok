## v0.0.44

### 新功能
- **模型配置重构为供应商两级导航**：按 baseURL + groupName 自动分组，点击供应商卡片进入模型列表页，支持在详情页拉取远程模型并勾选添加。
- **提示词注入界面可折叠**：三个注入卡片（Codex-X、自定义、中文化）收纳为折叠容器，默认收起，减少首页视觉干扰。
- **提示词注入新增生效日志**：注入触发时在 app.log 输出 `prompt injection applied` 日志，便于确认开关是否生效。

### 修复
- **修复 Cursor "Generate Commit Message" 报 "Network disconnected"**：
  - commit message 生成禁用 thinking effort，避免推理模型耗时超过 Cursor 客户端超时。
  - MITM 层对 WriteGitCommitMessage 使用 streaming forward（立即返回响应头，body 异步填充），彻底解决客户端短超时断连。
  - MITM → backend 的 ResponseHeaderTimeout 从 60s 增至 5min，匹配 provider stream idle timeout。
- **快速添加模型改为先拉取后选择**：原"拉取并添加全部"改为"拉取模型"，显示列表让用户勾选后再添加。
- **过滤 MITM 层大量 TLS WARN 日志**：静默 Windows `wsarecv: connection forcibly closed` 等连接回收噪声。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」→「仍要运行」即可。这是 Windows 对未购买代码签名证书的应用的标准行为，程序本身安全。