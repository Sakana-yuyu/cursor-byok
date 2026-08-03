# ccSwitch 供应商目录对齐与首字母排序实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 将普通模型供应商目录校正为 ccSwitch `main` 的 69 个非 Claude Desktop 条目，并在四个基础模板之后按显示名首字母稳定排序。

**Architecture:** `frontend/src/utils/supplierCatalog.js` 继续作为唯一供应商注册表。上游条目保留稳定 supplierID 和能力元数据，由注册表构造阶段移除多余条目、校正显示名，再用固定 `Intl.Collator` 对普通条目排序；基础模板和兼容模板的行为不变。

**Tech Stack:** Vue 3、ES modules、Vite、静态 i18n 扫描。

## Global Constraints

- 只对齐 ccSwitch `main` 的普通供应商目录，不加入 Claude Desktop 专用配置。
- 普通供应商必须为 69 个，包含 `deepseek`，不包含 `Unity2.ai`。
- 保持已有 supplierID 和旧兼容 supplierID，不迁移用户配置。
- 保留四个基础模板 `custom`、`openai`、`anthropic`、`gemini` 在列表顶部。
- 普通供应商按显示名使用不区分大小写、数字友好的稳定排序。
- 不新增测试文件；使用模块断言、i18n 扫描、构建、lint 和 diff 检查验证。
- 不修改工作区中与本任务无关的已有改动。

---

### Task 1: 校正 ccSwitch 供应商矩阵

**Files:**
- Modify: `frontend/src/utils/supplierCatalog.js:105-174`
- Modify: `docs/superpowers/specs/2026-08-02-ccswitch-supplier-alignment-design.md`

**Interfaces:**
- Consumes: 当前 `catalogRows` 的供应商定义。
- Produces: 69 条与 ccSwitch 普通目录一致的 `supplierRows` 输入。

- [x] **Step 1: 删除多余条目**

从 `catalogRows` 删除 `unity2` / `Unity2.ai`，并在 `compatibilityTemplates` 中保留同 ID 的兼容模板，确保旧配置仍可读取但不再出现在正常下拉。

- [x] **Step 2: 校正上游显示名**

仅修改展示字段，不修改稳定 ID、Base URL 或用户已保存字段：

```js
["shengsuanyun", "Shengsuanyun", ...]
["ucloud", "Compshare", ...]
["ucloud_coding", "Compshare Coding Plan", ...]
["qiniu", "Qiniu", ...]
["xai", "xAI (Grok)", ...]
["xiaomi_mimo_token_plan_cn", "Xiaomi MiMo Token Plan (China)", ...]
```

将 `volcengine_agent` 的显示名保留为仓库已有的 `火山Agent Plan`，复用现有多语言翻译；不修改稳定 ID。

- [x] **Step 3: 更新规格文档**

把规格中的“普通目录顺序”改为“上游登记顺序”，新增说明：UI 实际顺序在四个基础模板之后按显示名首字母排序，避免规格中的来源顺序与用户看到的顺序冲突。

- [x] **Step 4: 运行矩阵断言**

运行以下命令确认校正前的失败条件被消除：

```powershell
node --input-type=module -e "import { supplierSelectOptions } from './src/utils/supplierCatalog.js'; const labels = supplierSelectOptions().slice(4).map(({ label }) => label); if (labels.length !== 69 || labels.includes('Unity2.ai')) process.exit(1);"
```

Expected: 命令退出码为 0。

---

### Task 2: 实现普通供应商首字母排序

**Files:**
- Modify: `frontend/src/utils/supplierCatalog.js` near `supplierRows` construction

**Interfaces:**
- Consumes: Task 1 生成的 69 条 `catalogRows`。
- Produces: 已排序的 `supplierRows`，供 `SUPPLIER_TEMPLATES`、模型编辑、模型目录和供应商详情共同使用。

- [x] **Step 1: 添加稳定比较器**

在模块级定义固定比较器，避免每个页面重复排序：

```js
const SUPPLIER_LABEL_COLLATOR = new Intl.Collator("en", {
  numeric: true,
  sensitivity: "base",
});

function compareSupplierRows(left, right) {
  const labelOrder = SUPPLIER_LABEL_COLLATOR.compare(left[1], right[1]);
  return labelOrder || String(left[0]).localeCompare(String(right[0]));
}
```

- [x] **Step 2: 只排序普通条目**

保持基础模板在前，排序逻辑放在 `catalogRows.map(...)` 之前：

```js
const supplierRows = catalogRows
  .slice()
  .sort(compareSupplierRows)
  .map(([id, label, type, websiteURL, apiKeyURL, baseURL, models, options]) =>
    createTemplate({ id, label, type, websiteURL, apiKeyURL, baseURL, models, ...options }),
  );
```

不要排序 `coreTemplates`，避免破坏“自定义供应商”入口和既有编辑流程；不要排序兼容模板追加逻辑。

- [x] **Step 3: 运行排序断言**

运行以下命令验证基础模板位置、数量、唯一性、DeepSeek 和排序：

```powershell
node --input-type=module -e "import { supplierSelectOptions } from './src/utils/supplierCatalog.js'; const options = supplierSelectOptions(); const labels = options.slice(4).map(({ label }) => label); const sorted = [...labels].sort((a, b) => a.localeCompare(b, 'en', { numeric: true, sensitivity: 'base' })); const ids = options.map(({ value }) => value); if (labels.length !== 69 || labels.join('|') !== sorted.join('|') || new Set(ids).size !== ids.length || !ids.includes('deepseek') || ids.includes('unity2')) process.exit(1);"
```

Expected: 命令退出码为 0。

---

### Task 3: 构建与静态验证

**Files:**
- Inspect: `frontend/src/utils/supplierCatalog.js`
- Inspect: `docs/superpowers/specs/2026-08-02-ccswitch-supplier-alignment-design.md`

**Interfaces:**
- Consumes: Task 1 和 Task 2 的目录实现。
- Produces: 可构建的前端资源和可审阅的最小 diff。

- [x] **Step 1: 检查编辑文件 lint**

运行针对修改文件的诊断，修复由本次改动引入的错误：

```text
ReadLints: frontend/src/utils/supplierCatalog.js
```

- [x] **Step 2: 运行 i18n 扫描**

```powershell
npm --prefix frontend run i18n:scan
```

Expected: 退出码为 0；不产生与供应商名称无关的目录变更。

- [x] **Step 3: 运行前端构建**

```powershell
npm --prefix frontend run build
```

Expected: 退出码为 0。

- [x] **Step 4: 检查 diff 格式和范围**

```powershell
git diff --check
git status --short
```

Expected: 无空白错误；只包含本任务文件和用户已存在的无关改动，不回退无关改动。

- [x] **Step 5: 完成结果核对**

确认以下行为：

- 四个基础模板仍在普通供应商之前。
- `A6API`、`AICodeMirror`、`AiHubMix` 等按首字母靠前展示。
- `DeepSeek` 出现在 D 区域。
- `Xiaomi MiMo Token Plan (China)` 和 `Zhipu GLM` 出现在尾部区域。
- 旧兼容 supplierID 仍能通过 `supplierTemplate` 查找。