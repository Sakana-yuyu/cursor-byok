---
name: plan-docs
description: >-
  为项目生成可执行的波次计划文档。当用户进入 Plan 模式、要求开发计划、任务拆分、
  并行执行、依赖排序、实施波次或 AI/Subagent 协作时使用。简单且范围明确的短任务
  保持主线执行；跨模块、调用链未知、存在兼容性或多个独立工作面的任务生成依赖图、
  Wave 顺序、并行边界和完成门。兼容 Cursor Agent Skills 与 cursor-byok-source。
metadata:
  version: "1.0.0"
---

# plan-docs

把用户目标转成可独立阅读、可按依赖执行、可恢复的 Markdown 计划。

## 使用顺序

1. 判断任务是否简单。
2. 简单任务直接调查并生成精简计划，不派 Subagent。
3. 非平凡任务先做有限侦察；若存在至少两个独立调查角度，按运行环境能力并行派遣只读 Explore。
4. 收齐调查证据后，构建任务 DAG 和执行 Wave。
5. 使用 `CreatePlan` 提交完整计划；Markdown 是跨 Cursor 环境的事实源。
6. 用户启动计划后，只执行最早未完成 Wave；通过完成门后再推进下一 Wave。

## 简单任务判定

仅当以下条件全部满足时不派 Subagent：

- 目标可由 1–2 个明确文件或单一调用链完成。
- 不需要比较多个实现方案。
- 不涉及协议、持久化、兼容性、迁移或跨模块状态。
- 不需要独立证据复核。

“描述很短”不等于简单。满足任一升级信号时，按非平凡任务处理。

## 计划格式

必须使用 [templates/wave-plan.md](templates/wave-plan.md) 的结构，并遵守 [references/wave-execution.md](references/wave-execution.md)。

每个任务必须包含：

- 稳定 ID，例如 `T1`。
- 目标与范围。
- `depends_on`。
- `owned_paths`。
- 执行者：`main`、`subagent:explore` 或 `subagent:generalPurpose`。
- 产出、验收与并行限制。

每个 Wave 必须包含：

- 准入条件。
- 同波任务及 parallel/sequential 标记。
- 完成门。
- 失败策略。

## 执行契约

- `main`：短链路、共享文件、强耦合或无法证明并行安全的任务。
- `subagent:explore`：只读调查，只回传结论、文件证据和风险；调用 `Task` 时必须使用 `subagent_type="explore"` 与 `access_mode="inspect"`。
- `subagent:generalPurpose`：独立实现；调用 `Task` 时必须使用 `subagent_type="generalPurpose"` 与 `access_mode="act"`，要求在 `owned_paths` 内实际落盘并执行验收；普通实现优先显式选择 `model="gpt-5.6-terra"`，只有高难度推理、架构决策或高质量审查才升级为 `model="gpt-5.6-sol"`；`thinking_effort` 按任务需要选择 `disabled|low|medium|high|xhigh|max`，未指定时继承父运行配置；只有 `owned_paths` 不重叠且可独立验收时并行。
- 同波直接 Subagent 不超过 4 个；超过时拆成批次或后续 Wave。
- 父代理不重复执行已委派工作；等待同波全部结果后统一综合。
- Task 启动不等于完成。只有结果返回且验收通过，任务才能完成。
- 任一必需任务失败、超时或结果不明时，当前 Wave 标记 blocked，不得跳波。
- 每次推进前更新 Todo；Todo ID 与计划任务 ID 对齐。
- 用户明确禁止 Subagent 时，主线按 Wave 顺序完成全部任务。

## 兼容边界

- 不依赖私有 JSON 字段、tool call ID、Agent ID 或客户端内部路径。
- `phases`、todo `dependencies` 等结构化字段可以作为增强投影，但不能取代 Markdown。
- 官方 Cursor 无额外服务端支持时，Agent 仍应仅依据计划文本执行。
- cursor-byok-source 会增强 ExecutePlan 的计划承接，但 Skill 不假设该增强必然存在。