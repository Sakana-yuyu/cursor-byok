# Tasks: cursor-multi-account-management

> deps omitted = sequential, follows the previous item; owner appears because this is cross-stack work

- [ ] 1. 合同与敏感边界
  - [ ] 1.1 固化账户摘要、导入/导出、确认令牌、切换结果和稳定错误码合同
  - [ ] 1.2 增加账户库、状态库、备份和恢复包的精确 Git 忽略与暂存审计规则
- [ ] 2. 账户库与控制面授权
  - [ ] 2.1 实现账户 store、索引自愈、current 指针和旧文件完成式迁移 owner: backend deps: 1.1
  - [ ] 2.2 将 OAuth、资料补全、刷新锁和 `Authorization` 适配到当前账户 owner: backend deps: 2.1
  - [ ] 2.3 实现本机/Token/恢复 JSON 导入、删除和非敏感摘要 owner: backend deps: 2.1
  - [ ] 2.4 实现准备/确认式凭据恢复包导出及敏感日志测试 owner: backend deps: 2.3
- [ ] 3. Cursor 客户端切换事务
  - [ ] 3.1 抽取现有进程探测、关闭和启动为可注入运行时适配 owner: backend deps: 1.1
  - [ ] 3.2 增加纯认证白名单读写及 DB/WAL/SHM 备份恢复 owner: backend deps: 3.1
  - [ ] 3.3 实现准备、确认、执行、读回校验和反向恢复 owner: backend deps: 2.2, 3.2
- [ ] 4. Wails 与账户界面
  - [ ] 4.1 暴露新 bridge 方法、生成 bindings 并保留旧接口适配 owner: backend deps: 2.4, 3.3
  - [ ] 4.2 扩展 `clientApi` 与 browser-preview 测试计划 owner: frontend deps: 1.1
  - [ ] 4.3 实现紧凑账户列表、导入、当前选择、客户端切换、删除和凭据导出二次确认 owner: frontend deps: 4.1, 4.2
  - [ ] 4.4 同步静态 i18n 目录和关键路径 Playwright owner: frontend deps: 4.3
- [ ] 5. 集成、恢复演练与交付
  - [ ] 5.1 运行定向及全量 Go、vet、build、前端 unit/lint/build/i18n/E2E deps: 2.4, 3.3, 4.4
  - [ ] 5.2 用临时 SQLite 和账户根目录演练成功切换、各阶段失败恢复及凭据不外泄 deps: 5.1
  - [ ] 5.3 经用户再次确认后执行一次真实 Cursor 切换；未授权则保留为明确未验证 deps: 5.2
  - [ ] 5.4 按主题审核暂存并逐项提交，不推送、不发布 deps: 5.2
