# Verification Ledger: backend-capability-ui-discovery

## Round 0 - propose

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- | --- |
| V-1 | necessity | major | fixed(r0) | 不为已移除的授权/设备操作或不支持的用量查询建立 UI。 | `internal/client/license.go` 的对应方法固定返回“已移除”或 `UNSUPPORTED`；镜像记录已有完整后端接线但无前端引用。 | 提案只保留镜像记录开关，并在“Not in this change”排除这些接口。 |
| V-2 | regression-compat | major | fixed(r0) | 不新增测试文件，避免违反项目任务清单的“不写任何测试”约束。 | `IMPROVEMENT_TASKS.md` 明示不写测试；初版提案包含“补充浏览器预览测试”。 | 改为运行既有 lint、i18n、构建和浏览器预览冒烟检查。 |
| V-3 | regression-compat | minor | fixed(r1) | `mirrorCapture` 必须贯通归一化、持久化 payload、状态回填和缓存，否则保存其他设置可能重置开关或 hosts。 | `b662392` 将该配置加入 `normalizeConfig`、保存 payload、缓存 payload、状态回填和 browser-preview 默认配置；`8ca55f9` 的开关只更新 `enabled` 并保留 hosts。`yarn test:unit`（29/29）、`yarn lint`、`yarn build` 与 `yarn test:e2e`（69/69）均通过；四个语言包的 1,415 个目录键均存在且非空。 | 保存路径保留既有 hosts，开关默认关闭；不新增测试文件，符合 `IMPROVEMENT_TASKS.md`。 |

审查说明：当前会话未提供独立审查代理调度工具，按可用能力降级为基于代码与提案的必要性、回归兼容双视角审查。真实桌面端的官方请求写入 `history/_debug/mirror/official.raw.jsonl` 尚未在本次会话中人工验证；浏览器预览与既有 E2E 只能证明前端路径和 mock 保存行为。
