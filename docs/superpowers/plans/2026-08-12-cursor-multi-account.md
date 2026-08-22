# Cursor 多账户管理 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 Go/Wails/Vue 项目中实现完整的 Cursor 多账户登录、导入导出、控制面授权选择和经确认的本机 Cursor 客户端账号切换。

**Architecture:** 将 `cursoraccount.Manager` 从单凭据文件升级为账户库加当前账户指针，并保持它仍实现控制面 `Authorization`。真实 Cursor `state.vscdb` 的读取、备份、白名单注入和恢复留在 `internal/cursor`；`ProxyService` 只返回无敏感账户摘要，Vue 面板通过 Wails 调用完成管理操作。

**Tech Stack:** Go 1.26、Wails v3、Vue 3、现有 `modernc.org/sqlite`、现有静态 i18n 扫描器和 Playwright 浏览器预览。

## Global Constraints

- 只迁移 Cockpit 的 Cursor 多账户登录/导入/导出/切换逻辑；不引入其它 IDE、实例、配额、自动轮换或远端同步。
- 完整凭据只保存于 `appdata.DataRootPath()` 下权限为 `0600` 的后端文件；不得通过 Wails DTO、浏览器控制台、日志、测试名称或 Git 输出。
- 所有账户库写入通过临时文件加 rename 原子完成；账户 ID 仅允许 UUID/`[A-Za-z0-9._-]`，拒绝路径分隔符与 `..`。
- 写真实 Cursor `state.vscdb` 只能由用户显式“切换到 Cursor”触发，且必须先关闭 Cursor、创建 SQLite/WAL/SHM 备份、写入白名单、读回验证和重启。
- 任何切换阶段失败都必须恢复本次专属备份与切换前 current 指针；不能因失败丢失账户库或改变现有 Cursor 客户端认证。
- UI 可见文本以中文源码接入现有 i18n 扫描；不得手写 catalog message ID。
- 每个 Task 完成后独立提交。不得暂存 `.playwright-cli/`、`frontend/.playwright-cli/`、`output/`、`state.vscdb*`、账户备份、导出包或任何凭据。
- 当前主工作树的 i18n/status 标记和未跟踪目录属于既有状态；不重置、不删除或纳入本计划提交。

---

## 文件结构

| 文件 | 职责 |
| --- | --- |
| `internal/cursoraccount/store.go` | 账户/索引/current 指针的受限持久化、迁移、路径校验和摘要 DTO。 |
| `internal/cursoraccount/store_test.go` | 账户库 CRUD、索引自愈、原子写失败、单账号迁移与去重。 |
| `internal/cursoraccount/manager.go` | OAuth、当前账户授权/刷新、导入/导出编排。 |
| `internal/cursoraccount/manager_test.go` | OAuth 生命周期、当前账户刷新竞态、Token/JSON/本机导入与凭据不外泄测试。 |
| `internal/cursor/state_db.go` | 跨平台 state DB 路径、只读白名单读取、备份、注入、读回和恢复。 |
| `internal/cursor/state_db_test.go` | 临时 SQLite 数据库上的白名单不变量和失败恢复。 |
| `internal/cursoraccount/switch.go` | 关闭/重启 Cursor 的切换编排、operation ID 和回滚。 |
| `internal/client/cursor_account.go` | 供 Wails bridge 调用的非敏感账户操作入口。 |
| `internal/bridge/proxy.go` | 暴露新的 ProxyService Wails 方法与参数校验。 |
| `frontend/src/services/clientApi.js` | 桌面/预览模式的账户调用封装。 |
| `frontend/src/components/CursorAccountCard.vue` | 多账户列表、导入、标签和切换确认 UI。 |
| `frontend/e2e/cursor-account-card.spec.mjs` | 浏览器预览的账户管理关键路径。 |

### Task 1: 账户库、摘要与单账号迁移

**Files:**
- Create: `internal/cursoraccount/store.go`
- Create: `internal/cursoraccount/store_test.go`
- Modify: `internal/cursoraccount/manager.go`
- Modify: `internal/cursoraccount/manager_test.go`
- Modify: `internal/client/service.go`

**Interfaces:**
- Consumes: `appdata.DataRootPath()`、旧 `cursor-account.json` 的 `accessToken`、`refreshToken`、`authId`、`email` 字段。
- Produces: `AccountSummary`、`AccountStore`、`CurrentAccountID()` 和 `LoadCurrentCredentials()`，供 Task 2 的授权与导入逻辑调用。

- [ ] **Step 1: 写账户库与迁移的失败测试**

在 `store_test.go` 使用 `t.TempDir()` 创建隔离根目录；测试账户文件、索引和 current 指针都不含 token 返回值。

```go
func TestAccountStoreMigratesLegacyCredentialsOnce(t *testing.T) {
    root := t.TempDir()
    legacy := filepath.Join(root, "cursor-account.json")
    writeTestJSON(t, legacy, map[string]string{
        "accessToken": "test-access", "refreshToken": "test-refresh",
        "authId": "auth-a", "email": "a@example.test",
    })
    store := NewAccountStore(root, legacy)
    summaries, err := store.List()
    if err != nil { t.Fatal(err) }
    if len(summaries) != 1 || summaries[0].Email != "a@example.test" || !summaries[0].IsCurrent {
        t.Fatalf("unexpected migrated summaries: %#v", summaries)
    }
    if _, err := os.Stat(filepath.Join(root, "legacy", "cursor-account.json.bak")); err != nil {
        t.Fatalf("expected legacy backup: %v", err)
    }
}

func TestAccountStoreRepairsMissingIndexFromAccountFiles(t *testing.T) {
    store := NewAccountStore(t.TempDir(), "")
    _, err := store.Upsert(testCredential("auth-a", "a@example.test"))
    if err != nil { t.Fatal(err) }
    if err := os.Remove(store.IndexPathForTest()); err != nil { t.Fatal(err) }
    summaries, err := store.List()
    if err != nil || len(summaries) != 1 { t.Fatalf("got %#v, %v", summaries, err) }
}
```

- [ ] **Step 2: 运行测试确认失败原因正确**

Run: `go test ./internal/cursoraccount -run 'TestAccountStore(MigratesLegacyCredentialsOnce|RepairsMissingIndexFromAccountFiles)' -count=1`

Expected: FAIL，原因为 `NewAccountStore`、`AccountStore` 或测试辅助 API 尚未定义；不得因为现有测试或路径权限失败。

- [ ] **Step 3: 实现受限账户存储**

在 `store.go` 定义不包含凭据的摘要和仅供包内使用的完整记录。实现文件路径校验、账号锁、`0600` 原子写、索引自愈和首次迁移。

```go
type AccountSummary struct {
    ID string `json:"id"`
    Email string `json:"email"`
    AuthID string `json:"authId"`
    Tags []string `json:"tags"`
    IsCurrent bool `json:"isCurrent"`
    CreatedAt int64 `json:"createdAt"`
    LastUsedAt int64 `json:"lastUsedAt"`
}

func NewAccountStore(root, legacyPath string) *AccountStore
func (s *AccountStore) List() ([]AccountSummary, error)
func (s *AccountStore) Upsert(value credentials) (AccountSummary, error)
func (s *AccountStore) LoadCurrentCredentials() (credentials, string, error)
func (s *AccountStore) SetCurrent(id string) (AccountSummary, error)
```

`Upsert` 以 `authId` 作为优先去重条件；仅在 `authId` 缺失时用 email 与 access token 一致判断同一账户。迁移只有在账户库目录和 current 指针均不存在时执行；先完成新文件写入再移动旧文件。

- [ ] **Step 4: 将 Manager 初始化改为使用账户库**

修改 `NewManager` 接收 app-data 根目录与旧凭据路径，初始化 `AccountStore`，并将现有 `load()`、`save()`、`snapshotCredentials()` 逻辑改为从当前账户读写。修改 `internal/client/service.go` 以传入 `appdata.DataRootPath()` 与旧文件路径。

```go
service.cursorAccount = cursoraccount.NewManager(
    appdata.DataRootPath(),
    filepath.Join(appdata.DataRootPath(), "cursor-account.json"),
    netproxy.NewHTTPClient(publicAPITimeout),
)
```

- [ ] **Step 5: 运行账户库测试确认通过**

Run: `go test ./internal/cursoraccount -count=1`

Expected: PASS；现有 `TestImportFromCursorBackup` 仍通过，迁移测试确认只生成安全摘要。

- [ ] **Step 6: 审核并提交账户库主题**

```powershell
git add -- internal/cursoraccount/store.go internal/cursoraccount/store_test.go internal/cursoraccount/manager.go internal/cursoraccount/manager_test.go internal/client/service.go
git diff --cached --name-only
git diff --cached --check
git commit -m "feat(cursor): add account store and legacy migration"
```

Expected: 暂存区只含五个账户库相关文件；不包含账户真实数据、i18n 标记或未跟踪目录。

### Task 2: OAuth、导入导出与当前账户授权

**Files:**
- Modify: `internal/cursoraccount/manager.go`
- Modify: `internal/cursoraccount/manager_test.go`
- Modify: `internal/client/cursor_account.go`
- Create: `internal/client/cursor_account_test.go`

**Interfaces:**
- Consumes: Task 1 的 `AccountStore`、现有 `buildLoginURL`、`pollOnce`、`fetchProfile`、`Authorization` 刷新流程和 `cursor.ReadCursorAuthBackupValues`。
- Produces: `ListAccounts`、`ImportFromLocal`、`ImportToken`、`ImportJSON`、`Export`、`UpdateTags`、`Delete`、`SetCurrent` 和不泄露凭据的 client 方法，供 Task 4 的 Wails/UI 使用。

- [ ] **Step 1: 写 OAuth 多账户与导入的失败测试**

在 `manager_test.go` 为 Manager 注入测试 HTTP transport，不访问真实 Cursor API。

```go
func TestManagerOAuthCompletionAddsAccountWithoutOverwritingCurrent(t *testing.T) {
    manager := newTestManager(t, pollReturns("new-access", "new-refresh", "auth-b"))
    _, err := manager.AddCredentialsForTest(credentials{AccessToken: "old", AuthID: "auth-a", Email: "a@example.test"}, true)
    if err != nil { t.Fatal(err) }
    status, err := manager.StartLogin()
    if err != nil || status.State != StateWaiting { t.Fatalf("got %#v, %v", status, err) }
    waitFor(t, func() bool { return len(manager.ListAccounts()) == 2 })
    if manager.CurrentAccountID() != "auth-a" { t.Fatal("OAuth import changed current account") }
}

func TestManagerImportJSONRejectsUnknownCredentialContainers(t *testing.T) {
    manager := newTestManager(t, nil)
    _, err := manager.ImportJSON(`{"cookies":["secret"],"accessToken":"token"}`)
    if err == nil { t.Fatal("expected schema rejection") }
}

func TestClientAccountListNeverContainsTokens(t *testing.T) {
    service := newTestProxyServiceWithAccount(t, testCredential("auth-a", "a@example.test"))
    got, err := service.ListCursorAccounts()
    if err != nil || len(got) != 1 { t.Fatalf("got %#v, %v", got, err) }
    encoded, _ := json.Marshal(got)
    if bytes.Contains(encoded, []byte("test-access")) { t.Fatal("token leaked through client DTO") }
}
```

- [ ] **Step 2: 运行新测试确认失败**

Run: `go test ./internal/cursoraccount ./internal/client -run 'Test(ManagerOAuthCompletionAddsAccountWithoutOverwritingCurrent|ManagerImportJSONRejectsUnknownCredentialContainers|ClientAccountListNeverContainsTokens)' -count=1`

Expected: FAIL，原因为多账户接口未定义；不得调用真实网络。

- [ ] **Step 3: 实现 OAuth、白名单导入与受控导出**

保留现有 PKCE 轮询，成功后改为 `store.Upsert`。OAuth 成功只把第一个账户设为当前，已有当前账户时不改变选择。实现受控本机/Token/JSON 导入、标签、删除和导出。

```go
func (m *Manager) ListAccounts() ([]AccountSummary, error)
func (m *Manager) CurrentAccountID() string
func (m *Manager) ImportFromLocal() (AccountSummary, error)
func (m *Manager) ImportToken(ctx context.Context, raw string) (AccountSummary, error)
func (m *Manager) ImportJSON(content string) ([]AccountSummary, error)
func (m *Manager) Export(ids []string, includeCredentials bool) ([]byte, error)
func (m *Manager) UpdateTags(id string, tags []string) (AccountSummary, error)
func (m *Manager) Delete(ids []string, clearCurrent bool) error
```

JSON 必须为版本化恢复包或单个白名单字段对象；拒绝 `cookies`、`localStorage`、`browserData`、路径及其它顶级键。摘要导出永远不含 token；`includeCredentials=true` 时生成版本化恢复包但由 Task 4 的 UI 二次确认后才调用。

- [ ] **Step 4: 修改 Authorization 的当前账户刷新语义**

把 `Authorization(ctx)` 的凭据读取改为 `LoadCurrentCredentials()`。获取刷新锁前保存 account ID；刷新完成后的持久化必须校验 `CurrentAccountID()` 仍等于该 ID，若已切换则丢弃旧刷新结果并重新读取当前账户。

```go
creds, accountID, err := manager.store.LoadCurrentCredentials()
if err != nil { return "", err }
updated, shouldLogout, err := manager.refresh(ctx, creds)
if err != nil { return "", err }
if manager.store.CurrentAccountID() != accountID { return manager.Authorization(ctx) }
if err := manager.store.UpdateCredentials(accountID, updated); err != nil { return "", err }
```

- [ ] **Step 5: 暴露仅摘要的 client 方法**

在 `internal/client/cursor_account.go` 增加 `ListCursorAccounts`、导入、标签、当前选择、删除和导出方法。请求参数为空、长度超过 1 MiB 的 JSON 或超过 8 KiB 的 token 必须拒绝；返回类型只使用 `cursoraccount.AccountSummary` 和 `CursorAccountExportResult`。

- [ ] **Step 6: 运行测试确认通过**

```powershell
go test ./internal/cursoraccount ./internal/client -count=1
go vet ./internal/cursoraccount ./internal/client
```

Expected: PASS；测试 transport 断言没有真实 HTTP 请求，序列化摘要不含测试 token。

- [ ] **Step 7: 审核并提交账户操作主题**

```powershell
git add -- internal/cursoraccount/manager.go internal/cursoraccount/manager_test.go internal/client/cursor_account.go internal/client/cursor_account_test.go
git diff --cached --name-only
git diff --cached --check
git commit -m "feat(cursor): manage multiple account credentials"
```

Expected: 暂存区只含本 Task 的四个文件。

### Task 3: Cursor state.vscdb 的白名单切换事务与恢复

**Files:**
- Modify: `internal/cursor/state_db.go`
- Modify: `internal/cursor/state_db_test.go`
- Create: `internal/cursoraccount/switch.go`
- Create: `internal/cursoraccount/switch_test.go`
- Modify: `internal/client/cursor_account.go`
- Modify: `internal/client/cursor_account_test.go`

**Interfaces:**
- Consumes: Task 1 的账户 store、现有 `cursorAuthBackupKeys`、state DB 平台路径和 Cursor 进程工具。
- Produces: `SwitchCursorClientAccount(id string) (CursorClientSwitchResult, error)`，供 Task 4 的“切换到 Cursor”按钮调用。

- [ ] **Step 1: 写白名单注入和失败恢复的测试**

在 `state_db_test.go` 用临时 SQLite `ItemTable` 写入目标认证键和一个无关键。

```go
func TestReplaceCursorAuthKeepsUnrelatedState(t *testing.T) {
    db := newTestCursorStateDB(t, map[string]string{
        "cursorAuth/accessToken": "old",
        "cursorAuth/cachedEmail": "old@example.test",
        "workbench.colorTheme": "dark",
    })
    err := ReplaceCursorAuth(db.Path, CursorAuthValues{AccessToken: "new", Email: "new@example.test"})
    if err != nil { t.Fatal(err) }
    if got := readItem(t, db.Path, "workbench.colorTheme"); got != "dark" { t.Fatalf("unrelated key changed: %q", got) }
    if got := readItem(t, db.Path, "cursorAuth/accessToken"); got != "new" { t.Fatalf("got %q", got) }
}

func TestSwitchRestoresBackupWhenRestartFails(t *testing.T) {
    switcher := newTestSwitcher(t, restartReturns(errors.New("start failed")))
    before := switcher.ReadStateForTest()
    _, err := switcher.SwitchCursorClientAccount(switcher.TargetAccountID())
    if err == nil { t.Fatal("expected restart failure") }
    if got := switcher.ReadStateForTest(); !reflect.DeepEqual(got, before) { t.Fatal("state database was not restored") }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/cursor ./internal/cursoraccount -run 'Test(ReplaceCursorAuthKeepsUnrelatedState|SwitchRestoresBackupWhenRestartFails)' -count=1`

Expected: FAIL，因为 `ReplaceCursorAuth`、切换器或测试注入点尚未定义。

- [ ] **Step 3: 实现 state DB 白名单适配层**

在 `internal/cursor/state_db.go` 提供只处理指定认证键的路径 API；读写真实 DB 的调用者必须能指定路径以便临时库测试。

```go
type CursorAuthValues struct {
    AccessToken string
    RefreshToken string
    Email string
    AuthID string
    MembershipType string
    SubscriptionState string
    SignUpType string
}

func ReadCursorAuth(path string) (CursorAuthValues, error)
func ReplaceCursorAuth(path string, values CursorAuthValues) error
func BackupCursorStateFiles(dbPath, destination string) (CursorStateBackup, error)
func RestoreCursorStateFiles(backup CursorStateBackup) error
```

`ReplaceCursorAuth` 在一个 SQLite transaction 内 upsert 必填 access token/email，upsert 非空可选字段，并删除空可选字段对应的旧认证键。禁止遍历或复制无关 `ItemTable` 项。备份必须复制 `state.vscdb`、存在的 `-wal` 与 `-shm`，manifest 仅记录文件名、大小和 SHA-256。

- [ ] **Step 4: 实现账户切换编排与依赖注入**

在 `switch.go` 维护独立切换锁，并通过窄接口注入关闭/启动 Cursor 的行为以便测试。

```go
type CursorRuntime interface {
    Stop(ctx context.Context) error
    Start(ctx context.Context) error
}

type CursorClientSwitchResult struct {
    Account AccountSummary `json:"account"`
    OperationID string `json:"operationId"`
    Restarted bool `json:"restarted"`
    Recoverable bool `json:"recoverable"`
}

func (m *Manager) SwitchCursorClientAccount(ctx context.Context, id string) (CursorClientSwitchResult, error)
```

顺序固定为：验证账户 -> `runtime.Stop` -> `BackupCursorStateFiles` -> `ReplaceCursorAuth` -> `ReadCursorAuth` 校验 -> `store.SetCurrent` -> `runtime.Start`。失败时按反序执行：停止已启动进程、恢复备份、恢复旧 current 指针。恢复失败错误须用 `errors.Join` 保留两个阶段，但文本不得含认证字段值。

- [ ] **Step 5: 接入 client 切换方法并确保 bridge 只返回摘要**

在 `internal/client/cursor_account.go` 实现：

```go
func (s *ProxyService) SwitchCursorClientAccount(id string) (cursoraccount.CursorClientSwitchResult, error) {
    if strings.TrimSpace(id) == "" {
        return cursoraccount.CursorClientSwitchResult{}, errors.New("Cursor 账户 ID 不能为空")
    }
    return s.cursorAccount.SwitchCursorClientAccount(context.Background(), id)
}
```

client 测试将结果 JSON 序列化并断言不含 account store 里写入的测试 access/refresh token。

- [ ] **Step 6: 运行状态库与切换测试确认通过**

```powershell
go test ./internal/cursor ./internal/cursoraccount ./internal/client -count=1
go vet ./internal/cursor ./internal/cursoraccount ./internal/client
```

Expected: PASS；临时 SQLite 测试证明无关键不变、重启失败恢复 DB 和 current 指针。

- [ ] **Step 7: 审核并提交切换事务主题**

```powershell
git add -- internal/cursor/state_db.go internal/cursor/state_db_test.go internal/cursoraccount/switch.go internal/cursoraccount/switch_test.go internal/client/cursor_account.go internal/client/cursor_account_test.go
git diff --cached --name-only
git diff --cached --check
git commit -m "feat(cursor): switch local client account safely"
```

Expected: 暂存区只含 state DB 和切换事务文件。

### Task 4: Wails bridge、账户管理 UI 与 i18n

**Files:**
- Modify: `internal/bridge/proxy.go`
- Modify: `frontend/src/services/clientApi.js`
- Modify: `frontend/src/components/CursorAccountCard.vue`
- Create: `frontend/e2e/cursor-account-card.spec.mjs`
- Modify: `frontend/src/i18n/locales/zh-CN.json`
- Modify: `frontend/src/i18n/locales/en-US.json`
- Modify: `frontend/src/i18n/locales/ja-JP.json`
- Modify: `frontend/src/i18n/locales/ru-RU.json`
- Modify: `frontend/src/i18n/generated/catalog.json`

**Interfaces:**
- Consumes: Task 2 的 client 账户管理方法和 Task 3 的 `SwitchCursorClientAccount`。
- Produces: 账户列表、四种导入、标签编辑、受控导出、删除和切换 UI；所有 UI 数据只使用 `AccountSummary` 与 `CursorClientSwitchResult`。

- [ ] **Step 1: 写前端关键路径的失败 E2E 测试**

创建 `frontend/e2e/cursor-account-card.spec.mjs`，采用项目现有 preview mock 约定，为 `clientApi` 提供只包含账户摘要的 mock。

```js
test('账户面板可选择账户并在确认后请求切换', async ({ page }) => {
  await seedPreviewTestPlan(page, {
    cursorAccounts: [
      { id: 'account-a', email: 'a@example.test', authId: 'auth-a', tags: ['工作'], isCurrent: true },
      { id: 'account-b', email: 'b@example.test', authId: 'auth-b', tags: [], isCurrent: false },
    ],
  });
  await page.goto('/');
  await page.getByRole('button', { name: '切换到 Cursor' }).nth(1).click();
  await expect(page.getByRole('dialog', { name: '切换 Cursor 账号' })).toBeVisible();
  await page.getByRole('button', { name: '关闭并切换' }).click();
  await expect(page.getByText('已切换并重新启动 Cursor')).toBeVisible();
});
```

- [ ] **Step 2: 运行 E2E 确认失败**

Run: `npm run test:e2e -- cursor-account-card.spec.mjs` from `frontend/`.

Expected: FAIL，因为账户列表、按钮或 preview mock 路由尚未实现；失败不得依赖桌面应用或真实凭据。

- [ ] **Step 3: 暴露 Wails bridge 与前端 client API**

在 `internal/bridge/proxy.go` 为 Task 2/3 的 client 方法添加同名转发，拒绝空账户 ID、超长 token/JSON 和无效标签。`frontend/src/services/clientApi.js` 增加 preview fallback，返回空账户列表或明确“不支持预览模式”错误，不能创建假的 token。

```js
export async function listCursorAccounts() {
  return desktopCall('ListCursorAccounts', [], []);
}

export async function switchCursorClientAccount(accountId) {
  return desktopCall('SwitchCursorClientAccount', [accountId]);
}
```

- [ ] **Step 4: 将账户卡片升级为多账户管理面板**

保留现有页面位置与卡片层级，替换单账号状态展示为当前账户行和紧凑列表。

- “添加账户”按钮使用菜单提供 OAuth、本机 Cursor、Token、JSON。
- Token/JSON 使用模态输入，输入值只保存在组件局部 `ref`，请求结束、关闭模态或异常时立即清空。
- 每行提供设为当前、切换到 Cursor、标签、导出、删除 icon 按钮和 tooltip。
- “切换到 Cursor”确认模态明确提示将关闭并重启 Cursor，确认按钮文本为“关闭并切换”。
- 凭据导出使用第二个确认模态，说明包含登录凭据；默认导出调用摘要模式。
- 删除当前账户时要求先选择另一个账户或确认“只清除助手当前选择”。

不得把操作说明堆叠为卡片内长文；说明仅在对应模态或 tooltip 出现。

- [ ] **Step 5: 执行 i18n 扫描与目录一致性检查**

Run from `frontend/`:

```powershell
npm run build
node scripts/check-i18n.mjs
```

Expected: PASS；扫描更新 `catalog.json` 和全部 locale，所有 locale keys 一致、非中文翻译不为空且占位符一致。若项目的实际检查脚本名称不同，先用 `package.json` 中定义的 i18n 检查命令替代，不能手改生成 catalog 位置。

- [ ] **Step 6: 运行前端 E2E 并确认通过**

Run from `frontend/`:

```powershell
npm run test:e2e -- cursor-account-card.spec.mjs
```

Expected: PASS；确认切换调用、取消操作和错误反馈可见；网络日志、DOM 和截图中均不含 mock credential 值。

- [ ] **Step 7: 审核并提交 UI 主题**

```powershell
git add -- internal/bridge/proxy.go frontend/src/services/clientApi.js frontend/src/components/CursorAccountCard.vue frontend/e2e/cursor-account-card.spec.mjs frontend/src/i18n/generated/catalog.json frontend/src/i18n/locales/zh-CN.json frontend/src/i18n/locales/en-US.json frontend/src/i18n/locales/ja-JP.json frontend/src/i18n/locales/ru-RU.json
git diff --cached --name-only
git diff --cached --check
git commit -m "feat(ui): manage Cursor accounts"
```

Expected: 暂存区只含 bridge、账户 UI、E2E 与 i18n 生成文件。

### Task 5: 端到端验证、可恢复性复核与交付证据

**Files:**
- Modify: 无功能文件。
- Evidence: 临时测试数据库、临时账户根目录和浏览器截图只在本机验证目录中存在，不进入 Git。

**Interfaces:**
- Consumes: Tasks 1-4 的已提交实现、用户允许关闭/重启 Cursor 的确认和可用的本地 Cursor 安装。
- Produces: 已区分单元、构建、浏览器预览和真实 Cursor 切换的验收结论。

- [ ] **Step 1: 运行全量静态和 Go 验证**

Run from repository root:

```powershell
go test ./...
go vet ./...
go build ./...
git diff --check
```

Expected: 每条命令退出码 0。若已有非相关失败，记录命令、模块、错误与和本变更的关联判断，不能宣称全量通过。

- [ ] **Step 2: 创建临时可恢复性演练环境**

使用 `t.TempDir()` 等测试根目录创建含 `state.vscdb`、`-wal`、`-shm` 和白名单/无关键的数据库。执行一次成功 `SwitchCursorClientAccount` 和一次注入后故意启动失败的切换；比较三份文件 SHA-256，失败场景必须与切换前逐文件一致。

Run: `go test ./internal/cursor ./internal/cursoraccount -run 'TestSwitch|TestReplaceCursorAuth|TestBackup' -count=1 -v`

Expected: PASS，并且测试日志不输出认证键值。

- [ ] **Step 3: 验证桌面 UI 关键路径**

启动本地开发服务或桌面预览，依次验证：空账户、OAuth 等待/取消、从本机导入、标签、设为当前、删除确认、切换确认、失败回滚提示和凭据导出二次确认。使用 Preview/mock 账户完成 UI 流程，不在截图或控制台输入真实 token。

Run: `npm run test:e2e -- cursor-account-card.spec.mjs` from `frontend/`.

Expected: PASS，截图中显示切换确认模态和成功/失败状态，布局不重叠。

- [ ] **Step 4: 取得用户重新确认后进行一次真实 Cursor 切换**

在真正停止 Cursor 前再次询问用户确认，因为该操作会关闭其当前编辑器窗口。获准后：

1. 记录当前 `state.vscdb`、`-wal`、`-shm` 的非敏感 SHA-256 和大小到临时验证目录。
2. 在应用账户列表选择用户指定的已保存账户，执行“切换到 Cursor”。
3. 确认 Cursor 重启且应用显示所选摘要为当前账户。
4. 检查本次 backup manifest 已生成，且不包含 token。
5. 不把数据库、manifest、日志、截图或账户文件加入暂存区。

Expected: 真实客户端切换成功；若失败，确认应用已执行恢复并如实报告失败阶段。

- [ ] **Step 5: 发布前审计并提交验证文档（仅在需要时）**

若真实验证暴露可修复缺陷，先为缺陷写失败测试，按对应 Task 新建单主题修复提交并重新执行本 Task。若无需文档变更，不创建空提交。

```powershell
git status --short
git diff --cached --name-only
git diff --check
```

Expected: 暂存区为空，或只包含用户明确要求保留的验证说明；绝不包含凭据、数据库、备份或既有未跟踪目录。

## 自查

- **设计覆盖**：Task 1 覆盖账户库、索引自愈、权限和单账号迁移；Task 2 覆盖 OAuth、四类导入、导出、标签、删除和控制面当前授权；Task 3 覆盖 Cursor 客户端白名单注入、进程关闭、备份、读回、重启与恢复；Task 4 覆盖 bridge/UI/i18n；Task 5 覆盖静态、临时库、浏览器和经再次确认的真实 Cursor 验证。
- **占位扫描**：无 TBD、TODO、以后实现、模糊的“适当处理”步骤；每个实现步骤均给出文件、接口、测试或命令。
- **类型一致性**：Task 1 定义 `AccountSummary`/`AccountStore`，Task 2 使用其账户库接口，Task 3 定义 `CursorClientSwitchResult`，Task 4 和 Task 5 仅使用摘要及该切换结果，不依赖完整凭据 DTO。
