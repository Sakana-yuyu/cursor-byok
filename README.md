# Cursor助手（Sakana）

<img width="820" alt="Cursor助手首页" src="https://github.com/user-attachments/assets/2e1710b0-cdbd-4576-bd24-1614df016219" />

<img width="820" alt="模型配置" src="https://github.com/user-attachments/assets/00885453-6a91-4052-aadf-f686daeec881" />

<img width="820" alt="请求统计" src="https://github.com/user-attachments/assets/a607be84-a738-4e33-9750-13352e74001c" />

## 项目说明

这是由 Sakana 维护的 Cursor API 适配工具。它将模型配置、请求转发、提示词注入和使用统计集中到本地服务中，让你可以自由选择模型 API，而不被单一平台、订阅或计费方式绑定。

## 0.0.41 更新内容

- 支持 GPT-5.6 系列模型。
- 修复 Windows 构建流程中的 proto 生成、依赖安装和格式化脚本稳定性问题。
- 修复 Windows 安装包和压缩包产物生成流程。
- 更新应用发布者标识为 `Sakana`，并切换到 Sakana 的应用标识。
- 更新自动检查更新、发布地址和 Releases 地址，后续版本统一从本仓库发布和更新。

## 下载与更新

- [Sakana Releases](https://github.com/Sakana-yuyu/cursor-byok/releases)
- 应用内自动更新使用本仓库的 Releases；不再从原始仓库检查或下载版本。
- [详细使用教程](https://dcne38qm5vlg.feishu.cn/wiki/JeP7wdGnziBXuikNaF5czWbrn8c)

## 为什么做这个项目

公司喜欢把 Agent 服务与模型绑定在一起，让用户只能在指定模型、指定订阅和指定计费方式下使用工具。

我希望打破这种绑定关系：模型应该可以自由选择。开发者应该能够把自己的模型 API 接入到任何 IDE、Chat、Agent 或开发工具中，也可以自托管整套服务，避免被单一平台锁定。

这个项目的目标，是让模型选择权重新回到用户手里。

## 路线图

[正式版路线图](https://github.com/Sakana-yuyu/cursor-byok/discussions/32)

## 后续

后续会继续扩展更多工具和使用场景，包括但不限于：

- 支持更多 IDE 接入
- 支持更多 Chat 类应用
- 支持更多 Agent 工具和工作流
- 提供更完善的自托管部署方式
- 持续优化不同模型 API 的兼容性
- 降低接入成本，让已有模型额度可以被更充分地利用

最终希望做到：让你的模型 API 可以自由接入到你想使用的任何工具中。

## 作者主页

Sakana 主页：[Bilibili](https://space.bilibili.com/311706663/upload/video)

## Star History

<a href="https://www.star-history.com/?repos=Sakana-yuyu%2Fcursor-byok&type=timeline&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=Sakana-yuyu/cursor-byok&type=timeline&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=Sakana-yuyu/cursor-byok&type=timeline&legend=top-left" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=Sakana-yuyu/cursor-byok&type=timeline&legend=top-left" />
 </picture>
</a>
