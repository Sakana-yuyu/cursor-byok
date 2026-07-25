## v0.0.49

### 修复
- **子代理运行期间主进程提前关闭**：跳过 subagent 类型的 exec watchdog，主进程不再因 10 分钟超时强制结束 turn。子代理生命周期由 Cursor 客户端管理，主进程会等待子代理完成后继续。
- **Multitask Mode 请求排队**：`run` intent 改用异步发送，主进程可在模型生成期间接收新消息。
- **Use Multiple Models 不生效**：`conversation_action` 路径保留 `SubagentModelOverrides`。
- **commit message 生成闪退**：修复 MITM streaming 转发 panic。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。