# 官方模型选择器展示设计

## 目标

恢复 Cursor 官方原版的模型选择器交互，同时保留现有本地模型、队列自动续跑和最终回复修复：

- 输入框按钮同时显示模型名称与思考强度，例如 `GPT-5.6 Luna Extra High`。
- 点击按钮后先显示 `Fast`、`Effort`、`Model` 参数菜单。
- `Model` 子菜单包含搜索框、`Auto`、模型分组、现有全部模型和 `Add Models`。
- 不修改已安装 Cursor bundle，只通过 cursor-byok 的 Statsig bootstrap 与 AvailableModels 数据契约启用官方能力。

## 已确认的客户端契约

本机 `D:\cursor\resources\app\out\vs\workbench\workbench.desktop.main.js` 表明：

- `model_picker_experiments.effort_first_variant=treatment` 启用参数优先选择器。
- `effort_first_submenu_2026_08.enabled=true` 启用 `Fast / Effort / Model` 子菜单。
- `effort_first_grouped_models_2026_08.enabled=true` 启用官方分组模型列表。
- `effort_first_compact_model_ids` 中的模型会把触发器压缩成只显示参数，因此本地模型不能放入该列表。
- 触发器未被压缩时，客户端使用模型 variant 的 `displayNameOutsidePicker`，可显示模型名称与思考强度。

## 后端调整

1. Statsig bootstrap：
   - 将 `effort_first_variant` 恢复为 `treatment`。
   - `effort_first_compact_model_ids` 固定为空，避免只显示 `Extra High`。
   - 显式启用 submenu 与 grouped-models 两个动态实验。
2. AvailableModels：
   - 紧凑参数请求仍只返回 `low / medium / high / xhigh`，保持现有数据量。
   - 每个紧凑 variant 的 `displayName` 与 `displayNameOutsidePicker` 都包含模型名和强度。
   - 保留现有 adapter 顺序、模型 ID、参数、上下文、Fast 配置和 `isUserAdded` 语义。
3. 模型分组：
   - Cursor 账户来源模型按客户端原生 vendor 规则进入 `Cursor Models`。
   - 第三方和用户配置模型保持真实来源，进入 `Other Models`，不伪装成 Cursor 官方模型。
   - `Auto`、搜索与 `Add Models` 继续由客户端原生组件提供。

## 验证

- 单元测试验证 treatment、空 compact IDs、submenu、grouped-models。
- 单元测试验证紧凑 variant 名称包含模型名和 `Extra High`。
- 完整执行 `go test ./internal/backend/... -count=1`。
- 从干净提交构建 Windows ZIP，校验 ZIP 与 EXE SHA-256。
- 备份并替换 `D:\Cursor助手\Cursor助手.exe`，确认 `/healthz` 返回 `200 / ok`。
- 重启 Cursor 后真实检查：
  - 主按钮显示模型名与思考强度。
  - 参数菜单显示 `Fast / Effort / Model`。
  - Model 子菜单显示搜索、Auto、分组、原模型列表和 Add Models。
  - 队列修复提交仍在构建提交祖先中。

## 风险与回滚

- Statsig 分配会在进程内按身份固定，替换后必须完整重启 Cursor。
- 客户端未来升级若改变实验名，需要重新只读核对 bundle。
- 回滚时恢复安装版时间戳备份，并 revert 本次独立提交；不回退 `3ca85105` 与 `4b290a44` 中的队列修复。
