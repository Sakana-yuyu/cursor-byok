# Index: backend-capability-ui-discovery

Requirement source: 用户会话，2026-08-11

## Requirements
- R-1: 将没有设计界面的代码设计对应的界面同时保留易用性。 | source: 用户消息
- R-2: 特别是抓包cursor的代码。我需要后续抓包去完成和cursor的对接。 | source: 用户消息

## Assets
- A-1: Vue Router 与既有路由视图 | use: pattern
- A-2: `frontend/src/services/clientApi.js` | use: reuse
- A-3: 设置分类与 `Settings.vue` | use: pattern
- A-4: `Diagnostics.vue` | use: pattern
- A-5: 浏览器预览 mock 与 Playwright 入口 | use: reuse
- A-6: ProxyService Wails bindings | use: extend
- A-7: ProxyState、Cursor 代理修复与镜像记录文件 | use: extend
- A-8: WindowService 本地目录打开能力 | use: extend

## Exemplars
- E-1: 新增能力发现/高级操作入口 -> `Settings.vue` 与既有设置分类组件
- E-2: 新增会改变本地配置的操作确认 -> `Diagnostics.vue` 与现有 Modal/消息反馈模式
- E-3: 镜像抓包状态与显式操作入口 -> `AdvancedSettings.vue` 与既有 SettingsRow 反馈模式
