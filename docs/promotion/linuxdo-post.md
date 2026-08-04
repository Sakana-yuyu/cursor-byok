# Linux.do 推广帖

## 推荐标题

Cursor助手：把自己的模型 API 接入 Cursor，本地完成代理、统计和 Multitask 委派

## 正文

最近整理了一个面向 Cursor 的本地模型适配工具：**Cursor助手**。

它的定位不是重新做一个聊天客户端，而是把 Cursor 仍然擅长的编辑器和 Agent 工作流保留下来，同时把模型连接、供应商配置和本地调试集中管理。

### 主要能力

- 支持 OpenAI 兼容接口、Anthropic 原生协议、Gemini 原生协议和常见第三方网关
- 本地代理转发，支持流式事件、工具调用和 Cursor Task 工作流
- 模型目录拉取、模型可用性测试、上下文窗口自动匹配
- 请求数、输入/输出 token、缓存命中率、站点消耗和费用估算
- Multitask 自动委派、Explorer 子代理、视觉委派和状态同步
- Debug 日志与请求链路记录，方便定位 401、404、模型不支持、工具结果丢失等问题
- Windows、Linux、macOS 桌面发行包

### 使用方式

1. 从 GitHub Releases 下载对应平台版本：<https://github.com/Sakana-yuyu/cursor-byok/releases>
2. 打开“模型配置”，填写 API 地址、API Key 和模型 ID
3. 点击“拉取模型”或“测试”，确认供应商入口和模型可用
4. 启动本地服务，修复 Cursor 代理配置并重启 Cursor
5. 在 Cursor 中继续使用 Agent、工具调用和 Multitask

项目主页：<https://github.com/Sakana-yuyu/cursor-byok>

演示视频与 GIF 已放在 README：<https://github.com/Sakana-yuyu/cursor-byok#视频与动态图>

### 我比较关注的反馈

- 不同中转站的真实 URL 路径和鉴权规则
- Cursor 本地模式下工具调用、子代理和终态同步问题
- Windows/macOS/Linux 安装体验
- 余额查询、模型目录和上下文窗口自动匹配是否符合实际使用

反馈时请不要贴 API Key、Cookie、私钥或完整请求日志；提供脱敏后的错误信息、系统版本、Cursor 版本和模型 ID 就够了。

## 发布前检查

- [ ] 替换为最新 Release 链接
- [ ] 确认 README 中的视频/GIF 可以打开
- [ ] 确认没有把 API Key、ca.key、日志或本地配置上传
- [ ] 只发布真实可验证的功能，不夸大供应商兼容性
- [ ] 及时回复安装和配置问题
