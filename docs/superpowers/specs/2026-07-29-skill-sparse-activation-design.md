# 内置 Skills 调用链稀疏激活注入 — 设计

日期：2026-07-29
状态：已批准（用户选 A+B 组合）

## 目标

把内置 skills 从「全量饱和注入」改为「调用链稀疏激活注入」：每个请求只注入语义最相关的少量技能（Top-3 + 阈值），元技能 `find-skills` 常驻；子代理请求重打分但以父激活集作保底。同时把这套实现流程沉淀成一个可复用 skill，后续对任意技能做同类改造时直接调用。

## 现状（已查证）

- `SkillStore.BuildAgentSkillsPromptSection()`（`skill_store.go:79`）每次扫描**全部**技能，拼成 `<agent_skills>` 块注入每个非 DEBUG 请求的 system prompt（`compiler.go:87`）。这是饱和注入。
- `Compile(conversation, mode, latestUserText, modelName)` 已具备：`latestUserText`（打分输入）、`conversation.SubagentTypeName`（子代理判定）、`conversation.ParentConversationID`（父定位）。
- `ConversationFileStore` 有 `LoadConversation`（读父）、`UpdateConversationMeta`（写回）。
- `docs/subagent-smart-routing-design.md` 指出 cursor-byok 无法向运行中子代理注入消息——但本设计的激活发生在「子代理请求自身编译时」，不是向运行中子代理注入，因此可行。

## 方案：A+B 组合

- **方案 A 主体**：纯 Go BM25 向量打分 + 内存缓存 + 注入点改造 + 子代理重打分&父集保底 + 元技能常驻。
- **方案 B 组件**：引入轻量中文分词库 `github.com/wangbin/jiebago` 处理中文 description/query，英文走空格+小写+去标点。合并为统一 `tokenize(text) []string`。

## 数据流

```
Compile(conversation, latestUserText)
  ├─ 子代理? (conversation.SubagentTypeName != "")
  │    ├─ 是：queryText=latestUserText 重打分 + 读父会话 LastActivatedSkills
  │    │       激活集 = TopK(重打分 ∪ parentActivated, k=3)
  │    └─ 否：queryText=latestUserText 打分，激活集 = TopK(score≥0, k=3)
  ├─ SkillActivator.Activate(queryText, parentActivated) -> []GlobalSkill
  │    ├─ BM25：queryText vs 每技能预计算向量
  │    ├─ find-skills 永远置顶（不占 Top-3 名额）
  │    └─ Top-3 且 score ≥ 0
  └─ BuildActivatedSkillsPromptSection(activated) -> <agent_skills> 块
       （非子代理会话 best-effort 写回 LastActivatedSkills 到 conversation）
```

## 打分器（BM25）

```
预计算（懒加载，指纹失效重建）：
  terms[s] = tokenize(description) 去停用词
  docLen[s] = len(terms[s])
  df[t] = 含词 t 的技能数
  N = 技能总数；avgDocLen = 平均 docLen
  idf[t] = ln((N - df[t] + 0.5)/(df[t] + 0.5) + 1)

请求：
  qTerms = tokenize(queryText) 去停用词
  score(s) = Σ_{t∈qTerms∩terms[s]} idf[t] * (tf*(k1+1)) / (tf + k1*(1 - b + b*docLen[s]/avgDocLen))
  k1=1.2, b=0.75
```

阈值 = 0.0（BM25 非负，0=无词命中）。Top-K=3。最终注入 = `{find-skills} ∪ Top3`，去重后截断到 4。

## 子代理保底

- `Activate(queryText, parentActivatedNames)`：`candidates = Top3(重打分) ∪ parentActivatedNames`，按重打分排序取 Top-3。
- 父激活集存 `ConversationFile.LastActivatedSkills []string`（技能名）。
- 只在非子代理会话写回；best-effort，失败仅日志。
- 父记录缺失 -> `parentActivated=nil`，退化为纯重打分。
- 多层子代理：孙代理读直接父的 `LastActivatedSkills`，链路自然传递。
- `latestUserText` 为空 -> 激活集 = `{find-skills} ∪ parentActivated`。

## 改动清单

1. **`internal/backend/forwarder/skill_activator.go`（新增）**：`SkillActivator` 结构，BM25 索引 + 缓存 + `Activate()`；`tokenize()` 用 jiebago + 英文规则。
2. **`skill_store.go`**：`SkillStore` 加 `store *ConversationFileStore`（可选 setter）+ `activator *SkillActivator`；新增 `BuildActivatedSkillsPromptSection(activated []GlobalSkill)`；旧 `BuildAgentSkillsPromptSection` 保留供 debug 回退。
3. **`types.go`**：`ConversationFile` 加 `LastActivatedSkills []string`。
4. **`compiler.go`**：第 87 行改调 `skills.BuildActivatedSkillsSectionFor(latestUserText, conversation)`（内部判子代理+父集+写回）。
5. **`service.go`**：`NewSkillStore` 后调用 `skills.SetConversationStore(store)` 注入 store。
6. **`go.mod`**：`go get github.com/wangbin/jiebago`。
7. **产出 skill**：`internal/skills/bundled/skill-sparse-activation/SKILL.md`（随程序分发）+ `.agents/skills/skill-sparse-activation/SKILL.md`。

## Prefix Cache 稳定性

- 激活是**确定性**的：相同 queryText + 相同技能集 + 相同父集 -> 相同激活集 -> 相同注入。同一 turn 的重试/重编译 prefix 稳定。
- 注入位置不变（system prompt 的 skills 区域，顺序在 shared rules 后）。
- `LastActivatedSkills` 是 best-effort 元数据，不进 model-visible history，不影响 replay prefix。

## 验证

- `go build ./...` + `go vet ./...` 通过。
- `CompileSummary` 含 `activated_skills=N` 可观测。
- 非 DEBUG 请求注入的技能数 ≤ 4（find-skills + Top3）。
