# Verification Ledger: backend-capability-ui-discovery

## Round 0 - propose

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- | --- |
| V-1 | necessity | major | fixed(r0) | 不为已移除的授权/设备操作或不支持的用量查询建立 UI。 | `internal/client/license.go` 的对应方法固定返回“已移除”或 `UNSUPPORTED`；镜像记录已有完整后端接线但无前端引用。 | 提案只保留镜像记录开关，并在“Not in this change”排除这些接口。 |
| V-2 | regression-compat | major | fixed(r0) | 不新增测试文件，避免违反项目任务清单的“不写任何测试”约束。 | `IMPROVEMENT_TASKS.md` 明示不写测试；初版提案包含“补充浏览器预览测试”。 | 改为运行既有 lint、i18n、构建和浏览器预览冒烟检查。 |
| V-3 | regression-compat | minor | open | `mirrorCapture` 必须贯通归一化、持久化 payload、状态回填和缓存，否则保存其他设置可能重置开关或 hosts。 | 现有 `localResponseCache` 已按相同数据流处理；`appState` 当前未含 `mirrorCapture`。 | 实施阶段按提案的第一项逐处核验，并在独立提交前检查配置保存结果。 |

审查说明：当前会话未提供独立审查代理调度工具，按可用能力降级为基于代码与提案的必要性、回归兼容双视角审查。
