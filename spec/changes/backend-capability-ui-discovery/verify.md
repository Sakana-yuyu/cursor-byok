# Verification Ledger: backend-capability-ui-discovery

## Round 0 - propose

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- | --- |
| V-1 | necessity | major | fixed(r0) | 不为已移除的授权/设备操作或不支持的用量查询建立 UI。 | `internal/client/license.go` 的对应方法固定返回“已移除”或 `UNSUPPORTED`；镜像记录已有完整后端接线但无前端引用。 | 提案只保留镜像记录开关，并在“Not in this change”排除这些接口。 |
| V-2 | regression-compat | major | fixed(r0) | 不新增测试文件，避免违反项目任务清单的“不写任何测试”约束。 | `IMPROVEMENT_TASKS.md` 明示不写测试；初版提案包含“补充浏览器预览测试”。 | 改为运行既有 lint、i18n、构建和浏览器预览冒烟检查。 |
| V-3 | regression-compat | minor | fixed(r1) | `mirrorCapture` 必须贯通归一化、持久化 payload、状态回填和缓存，否则保存其他设置可能重置开关或 hosts。 | `b662392` 将该配置加入 `normalizeConfig`、保存 payload、缓存 payload、状态回填和 browser-preview 默认配置；`8ca55f9` 的开关只更新 `enabled` 并保留 hosts。2026-08-12 复核 `yarn test:unit`（29/29）、`yarn lint`、`yarn build` 均通过；全量 E2E 为 68/69，唯一失败是无关的更新说明 Markdown 弹窗。四个语言包均为 1,435 个目录键，且 `missing/extra/empty/placeholder_mismatch` 均为 0。 | 保存路径保留既有 hosts，开关默认关闭；不新增测试文件，符合 `IMPROVEMENT_TASKS.md`。 |

审查说明：当前会话未提供独立审查代理调度工具，按可用能力降级为基于代码与提案的必要性、回归兼容双视角审查。真实桌面端的官方请求写入 `history/_debug/mirror/official.raw.jsonl` 尚未在本次会话中人工验证；浏览器预览与既有 E2E 只能证明前端路径和 mock 保存行为。

## Round 1 - implementation evidence

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- | --- |
| V-4 | correctness | minor | open | 全量浏览器 E2E 未全绿：更新说明 Markdown 弹窗未进入 DOM。该失败不经过本变更的高级设置、镜像状态 API、目录打开 binding 或浏览器 preview mock。 | `frontend/yarn test:e2e -- --workers=1` 退出码 1；68 通过、1 失败。失败固定在 `frontend/e2e/modal-markdown-lazy.spec.mjs:25`，预期 `发现新版本` 对话框中的 `性能优化` 标题，实际找不到该元素。 | 不在本镜像抓包变更中夹带修复；保留为基线验证缺口。 |
| V-5 | runtime-evidence | minor | open | 真实桌面端尚未证明 Cursor 的一次官方模型请求已写入本地记录。 | 本轮未改动真实 Cursor 配置、未启动桌面代理、未向官方 API 发起测试请求；浏览器 preview 仅注入 `fileExists/sizeBytes/modifiedAtUnixMs` 元数据。 | 需用户在桌面端显式开启镜像记录、启动服务或修复代理、重启 Cursor，并自行发起一次官方模型请求后确认状态变为“已记录”。 |

本轮证据：

- `go test ./internal/bridge ./internal/client`、`go build ./...`、`go vet ./...`：退出码 0。
- `frontend/yarn lint`：退出码 0。
- `frontend/yarn test:unit`：29/29 通过。
- `frontend/yarn build`：退出码 0，i18n 扫描与构建输出断言通过。
- 语言包结构化核对：`zh-CN`、`en-US`、`ja-JP`、`ru-RU` 均为 1,435 键，`missing=0`、`extra=0`、`empty=0`、`placeholder_mismatch=0`。
- 浏览器 preview：未启用态显示“未启用/刷新/打开记录目录”；注入只读元数据后显示“已记录”、`1.5 KB` 和更新时间；375px 宽度 `scrollWidth=375`、`innerWidth=375`；页面中未出现样例正文、HTTP URL 或 `authorization`；点击目录按钮记录到 `OpenMirrorCaptureDirectory` mock，控制台错误数为 0。
- `ast-grep` 未安装，未执行 AST 规则包；已对 `AdvancedSettings.vue`、`clientApi.js`、`browserBindings.js` 进行 charter 手工模式扫描。新增的状态/操作错误均写入界面错误状态，未发现“新逻辑失败后静默回退旧逻辑”或数据写入路径吞错返回默认值。
- 独立 `spec-verifier` agent 调度工具在当前会话不可用，因此本轮没有独立验证结论；上述检查为实现会话的自检证据，不能替代独立审查。
