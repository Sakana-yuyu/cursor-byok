# v0.0.68 发布说明

## 🐛 Skills 扫描失败修复

### 根因
v0.0.67 引入的跨工具 Skills 扫描无法发现任何技能，根因有两个：

1. **frontmatter 解析器格式不兼容**：`parseSKILLFrontmatter` 假设 SKILL.md 以 `key: value` 行开头、到第一个 `---` 结束（旧式格式）。但实际 SKILL.md 使用标准 YAML frontmatter，以 `---` 开头：
   ```
   ---           <- frontmatter 开始（解析器误判为结束，立即 break）
   name: find-skills
   description: ...
   ---           <- frontmatter 结束
   ```
   导致 `name`/`description` 永远为空，所有技能被静默丢弃。

2. **符号链接目录被跳过**：ZCode/Claude 等工具用 symlink 指向共享 `.agents/skills` 下的技能目录。`os.ReadDir` 的 `entry.IsDir()` 对 symlink 返回 `false`（基于 Lstat），导致 symlink 技能目录被跳过。

### 修复
- **`parseSKILLFrontmatter`**：支持标准 YAML frontmatter（`---` 开头 + 第二个 `---` 结束）和旧式格式（无前置 `---`）两种；值支持引号去除（`"..."` / `'...'`）
- **`scanOneSkillRoot` + `scanLegacySkillRoot`**：对 `IsDir()` 返回 false 的条目，用 `os.Stat` 跟踪符号链接判断目标是否目录

### 验证
- 修复前：所有 17 个技能 `[empty name]` -> 全部丢弃 -> `<agent_skills>` 为空
- 修复后：17 个技能全部正确解析 name/description，经 BM25 Top-K 稀疏激活注入

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。
