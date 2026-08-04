# Linux.do 推广帖

## 推荐标题

【开源推广】Cursor助手：把自己的模型 API 接入 Cursor，本地代理、统计与 Multitask 工作台

## 正文

#### 本帖使用社区开源推广，符合推广要求。我申明并遵循社区要求的以下内容：

* **我的帖子已经打上 #开源推广::tag 标签：** 是
* **我的开源项目完整开源，无未开源部分：** 是
* **我的开源项目已链接认可 LINUX DO 社区：** 是
* **我帖子内的项目介绍，AI生成、润色内容部分已截图发出：** 是
* **以上选择我承诺是永久有效的，接受社区和佬友监督：** 是

*以下为项目介绍正文内容，AI生成、润色内容已使用截图方式发出*

# Cursor助手：把自己的模型 API 接入 Cursor

最近整理了一个面向 Cursor 的本地模型适配工具：**Cursor助手**。

它不替代 Cursor 的编辑器和 Agent，而是把模型供应商配置、本地代理、用量统计、Multitask 委派和问题排查集中到一个本地工作台里。你可以继续使用 Cursor 的编辑、工具调用和 Agent 工作流，同时接入自己的 API、兼容网关或自建服务。

## 项目地址

- GitHub 仓库：https://github.com/Sakana-yuyu/cursor-byok
- GitHub Releases：https://github.com/Sakana-yuyu/cursor-byok/releases
- Linux.do 社区：https://linux.do/

## 主要功能

- 支持 OpenAI 兼容接口、Anthropic 原生协议、Gemini 原生协议和常见第三方网关。
- 本地运行代理网关，负责 Cursor 与模型供应商之间的请求转发和流式事件转换。
- 支持工具调用、Cursor Task、Agent 工作流和 Multitask 委派。
- 支持 Explorer 子代理、视觉委派和任务状态同步。
- 支持拉取供应商模型目录、模型可用性测试和上下文窗口自动匹配。
- 提供请求数、输入/输出 Token、缓存命中率、站点消耗和费用估算等统计。
- 保存脱敏后的请求链路、模型调用、工具调用和终态信息，方便排查 401、404、模型不支持、结果丢失等问题。
- 提供 Windows、Linux 和 macOS 桌面发行包。

## 截图与 mock 数据

下面的截图和 GIF 来自项目的浏览器预览模式，使用的是本地 mock 数据，不会发起真实 API 请求。

模型配置截图中的 `sk-b***demo` 和 `AIza****demo` 都是占位符，不是可用密钥；帖内不会展示任何真实 API Key、Cookie、账户信息或完整日志。

示例统计数据仅用于说明界面展示形式：

```text
示例请求数：128
成功请求：124    异常请求：4
输入 Token：186,400    输出 Token：42,800
缓存命中率：63.4%    估算费用：$2.18
```

以上数值均为虚构演示数据，不代表任何真实账户、供应商余额或实际消费。

## 使用方法

1. 从 [GitHub Releases](https://github.com/Sakana-yuyu/cursor-byok/releases) 下载对应平台版本。
2. 打开“模型配置”，添加供应商并填写 API 地址、API Key 和模型 ID。
3. 优先点击“拉取模型”或“测试”，确认供应商入口和模型可用。
4. 启动本地服务，点击“修复 Cursor 配置”。
5. 完全退出并重新打开 Cursor。
6. 在 Cursor 中发送请求，确认回复、工具调用和 Agent 工作流正常。

不同供应商的接口路径和鉴权方式可能不同。OpenAI 兼容接口、Anthropic 原生接口、Gemini 接口以及第三方中转网关，通常不能直接套用同一个 URL。

如果模型列表为空，建议依次检查：

- API 地址是否包含正确的版本路径；
- 鉴权方式是否匹配；
- 供应商是否支持模型目录接口；
- 模型 ID 是否属于当前账号或网关；
- 是否把 Anthropic 或 Gemini 原生接口误配成 OpenAI 兼容接口。

## 视频与动态图

项目 README 中包含首页预览、模型配置截图和 GIF 动态演示：

https://github.com/Sakana-yuyu/cursor-byok#视频与动态图

演示内容包括模型供应商配置、模型目录拉取、本地代理启动、用量统计、请求明细以及 Agent/Multitask 工作流。

## 隐私与安全

项目提供本地配置和本地转发能力，不托管你的 API Key。请注意：

- 不要把 API Key、访问令牌、Cookie 或私钥提交到 Git 仓库；
- 不要把 `ca.key`、完整请求日志或本地历史上传到 Issue；
- 分享 Debug 日志前，先删除请求文本、鉴权信息和敏感模型参数；
- 第三方供应商的计费、余额和数据保留策略，以供应商自身规则为准。

## 欢迎反馈

目前比较关注以下实际使用反馈：

- 不同中转站的真实 API 地址和路径规则；
- Cursor 本地模式下的工具调用、Agent 状态同步和 stopped 问题；
- Windows、Linux、macOS 的安装体验；
- 模型目录拉取、上下文窗口自动匹配和余额查询；
- 用量统计、请求日志和异常定位是否符合实际使用。

欢迎提交 Issue 或 Pull Request。反馈时请提供操作系统、Cursor 版本、Cursor助手版本、供应商类型、模型 ID 和脱敏后的错误信息，不要发布 API Key、Cookie、私钥或完整请求内容。

原作者公开地址：https://space.bilibili.com/311706663/upload/video

项目基于 MIT License 开源，欢迎大家体验、反馈和参与贡献。
