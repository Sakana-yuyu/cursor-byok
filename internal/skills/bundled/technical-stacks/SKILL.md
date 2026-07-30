---
name: technical-stacks
description: 使用 Go、Vue、TypeScript、Wails、协议服务、Agent、MCP 或模型适配器开发时使用；帮助遵循对应技术栈的工程约束。
---

# Technical Stacks

先读取仓库现有实现和依赖版本，再选择技术方案。

- Go 代码保持包边界清晰，显式处理 context、错误和并发生命周期。
- Vue 代码复用组件和状态管理，避免把业务逻辑堆进单个页面。
- Wails/协议改动保持 DTO、binding、客户端和服务端字段兼容。
- Agent、工具和 MCP 调用必须有唯一 ID、终态、取消和超时语义。
