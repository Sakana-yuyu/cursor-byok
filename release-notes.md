## v0.0.75

### 修复
- **Multitask 自动委派**：选择 Multitask 后，普通只读探索请求会自动创建 Explorer 子代理，不再要求用户额外输入“使用子代理”。
- **Cursor 委派状态同步**：通过 conversation checkpoint 同步子代理的运行中、成功、失败和取消状态，修复 Cursor 卡片显示 stopped 但后端仍在运行，或任务完成后不显示结果的问题。
- **执行身份追踪**：统一记录 request_id、conversation_id、model_call_id、tool_call_id 和 exec_id，降低终态错配、重复注册和超时恢复误判。
- **Prompt replay 与压缩**：合并上游 Cursor command replay、summarize 和 compaction 修复，减少上下文恢复后工具调用状态丢失。

### 优化
- **子代理模型显示**：委派卡片优先显示模型名称，保留模型 ID 作为内部追踪字段。
- **调试日志**：补充委派启动、checkpoint 接受/拒绝、终态收口和 provider pass 日志，便于根据真实请求定位传输链路。
- **文档**：README 更新为独立项目说明，补充 Multitask、状态同步和本地调试流程。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击“更多信息”后选择“仍要运行”。
