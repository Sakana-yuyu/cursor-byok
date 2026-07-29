---
name: skill-sparse-activation
description: Use when the user wants to convert built-in skills from full/saturated injection into call-chain sparse activation injection, or asks to make skill injection relevance-based / Top-K / sparse, or to apply the same sparse-activation treatment to skills as the existing framework. Reproduces the exact implementation steps for the sparse skill activation framework.
---

# Skill Sparse Activation（技能调用链稀疏激活注入）

## 用途

把内置 skills 从「每个请求全量注入所有技能」改为「按当前请求文本语义相关性，只稀疏激活 Top-K 相关技能注入」。本 skill 是该改造流程的可复用手册：当用户要求对 skills 做「稀疏激活 / 按相关性注入 / Top-K 激活 / 调用链激活」时，按此流程执行。

## 核心设计（三级要点）

1. **打分**：BM25 语义相关性，query 文本 vs 每个技能 `Name + Description` 合并分词后的文档。
2. **稀疏**：Top-3 且得分 ≥ 阈值；元技能 `find-skills` 始终常驻（不占 Top-3 名额）；最终注入上限 4。
3. **调用链**：子代理请求重打分，并以父会话最近激活集作保底候选（父集保底进候选，仍按相关性裁到 Top-3）。

## 实现步骤（按序执行）

### 1. 引入分词库

```bash
go get github.com/wangbin/jiebago@latest
```

jiebago 需从文件加载词典。**不要**依赖模块缓存里 5MB 的 `dict.txt`（路径不稳定、体积大）。改为嵌入一个覆盖技能领域常用词的精简词典（见 `skill_activator.go` 的 `builtinSkillDict` 常量），运行时写入临时文件再 `LoadDictionary`，加载失败退化纯英文分词。

### 2. 新增 `internal/backend/forwarder/skill_activator.go`

核心组件：

- `tokenize(text)`：小写化 → jiebago 中文分词（含 CJK 时）+ 拉丁字母数字边界切分；去停用词、去标点（用 `hasWordChar` 过滤纯符号）。
- `skillIndex`：BM25 倒排索引快照。**文档文本 = Name + Description 合并**（让英文技能名如 debugging/i18n 也能匹配）。含 `nameToDoc` 映射（技能名→docs 索引），**打分时必须用 nameToDoc 定位文档，不能用 others 切片下标**（否则索引错位导致全部 0 分）。
- `SkillActivator.Activate(queryText, parentActivated)`：
  - 分离元技能（find-skills）与其余技能。
  - 对其余技能 BM25 打分；父集技能若打分为 0 给极小正值保底入选。
  - 排序（分数降序 → 父集优先 → 名称稳定序），取 Top-3。
  - 合并元技能 + Top-3，去重，截断到 4。
- BM25 参数：`k1=1.2, b=0.75`；`idf=ln((N-df+0.5)/(df+0.5)+1)`；阈值 `0.0`。
- 缓存：`ensureIndex` 按指纹（name+description 拼接）失效重建，懒加载。

### 3. 改造 `skill_store.go`

- `SkillStore` 加字段 `activator *SkillActivator`（`NewSkillStore` 时创建）、`convStore *ConversationFileStore`。
- 加 `SetConversationStore(convStore)` setter。
- 新增 `BuildActivatedSkillsPromptSection(queryText, conversation)`：
  - 子代理会话（`conversation.SubagentTypeName != ""`）→ 读父 `LastActivatedSkills` 作 parentActivated。
  - 调 `activator.Activate`。
  - 非子代理会话 → best-effort 写回 `conversation.LastActivatedSkills`。
  - activator 为 nil 时退化为旧 `BuildAgentSkillsPromptSection`（全量）。
- 旧 `BuildAgentSkillsPromptSection` 保留作 debug 回退。
- 辅助方法 `readParentActivatedSkills`（`convStore.LoadConversation(parentID)`）、`writeActivatedSkillsToConversation`（`convStore.UpdateConversationMeta`）。

### 4. 加会话字段 `types.go`

`ConversationFile` 加：
```go
LastActivatedSkills []string `json:"last_activated_skills,omitempty"`
```
不进 model-visible history，不影响 replay prefix（见 prefix-cache-stability）。

### 5. 改注入点 `compiler.go`

把 `compiler.skills.BuildAgentSkillsPromptSection()` 改为：
```go
compiler.skills.BuildActivatedSkillsPromptSection(latestUserText, conversation)
```
`Compile` 已有 `latestUserText` 和 `conversation` 参数，无需改签名。`CompileSummary` 的 `global_skills=N` 改 `activated_skills=N`。

### 6. 注入会话存储 `service.go`

`NewService` 里 `NewSkillStore(...)` 后加：
```go
skills.SetConversationStore(store)
```

## 关键约束

- **确定性**：相同 queryText + 相同技能集 + 相同父集 → 相同激活集。保证同 turn 重试/重编译 prefix 稳定（prefix-cache-stability）。
- **降级安全**：分词器加载失败→纯英文分词；父记录缺失→纯重打分；无匹配→仅 find-skills。任何环节失败不阻断编译。
- **不写测试**：本仓库禁止测试文件。验证用一次性 `main` 程序（带 `//go:build ignore` 或独立临时目录），验证后删除。

## 验证方法

写一个临时 `main` 程序，构造临时技能目录，调用激活，确认：
- 调试类请求 → 激活 systematic-debugging
- 发布类请求 → 激活 uploadcursor
- 中文查询无英文匹配 → 仅 find-skills（安全降级）
- 子代理请求带父集 → 父集技能入选

验证后删除临时程序。最后 `go build ./...` + `go vet ./...` 通过。

## 参考文件

- 设计文档：`docs/superpowers/specs/2026-07-29-skill-sparse-activation-design.md`
- 实现：`internal/backend/forwarder/skill_activator.go`、`skill_store.go`、`compiler.go`、`types.go`、`service.go`
