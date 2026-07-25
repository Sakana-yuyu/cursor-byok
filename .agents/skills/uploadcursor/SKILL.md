---
name: uploadcursor
description: 发布 cursor-byok 新版本。当需要发布 release、更新版本号、推送 tag 触发 GitHub Action 自动构建时使用。
---

# 发布 cursor-byok 新版本

## 前置条件

- 工作区干净（所有变更已提交）
- 当前分支为 `main`
- Git Credential Manager 已配置（`git push` 可直接认证）
- `gh` CLI 不需要（GitHub Action 自动创建 Release）

## 发布步骤

### 1. 获取当前版本号

```bash
go run ./scripts/release version -config ./build/config.yml
```

输出如 `0.0.49`。新版本号 = 当前版本号 +1 patch（如 `0.0.50`）。

### 2. 更新版本号

编辑 `build/config.yml`，修改 `version` 字段：

```yaml
info:
  version: "0.0.50"  # 从 0.0.49 改为 0.0.50
```

### 3. 更新发布说明

编辑 `release-notes.md`，替换为新版本的发布说明。格式：

```markdown
## v0.0.50

### 修复
- **简述修复内容**

### 新功能
- **简述新功能**

### 优化
- **简述优化内容**

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。
```

### 4. 提交并创建 tag

```bash
git add build/config.yml release-notes.md
git commit -m "release: v0.0.50"
git tag v0.0.50
```

### 5. 推送代码和 tag

```bash
git push origin main
git push origin v0.0.50
```

### 6. 等待 GitHub Action 自动构建

推送 `v*` tag 后，`.github/workflows/build.yml` 自动触发：

1. 构建 5 个平台资产（Windows amd64/386、Linux amd64、macOS arm64/amd64）
2. 生成 `update.json`（包含所有平台 checksum）
3. 创建 GitHub Release（使用 `release-notes.md` 作为发布说明）
4. 上传所有发布资产

在 GitHub 仓库的 **Actions** 页面查看进度。完成后 Release 自动出现在 **Releases** 页面。

## 验证

- GitHub Actions 页面确认构建成功
- Releases 页面确认新版本和资产已上传
- `update.json` 已更新（应用内自动更新会检查此文件）

## 注意事项

- 版本号只递增 patch（如 0.0.49 -> 0.0.50），除非有重大变更
- `release-notes.md` 每次完全替换为新版本的内容（不是追加）
- tag 必须以 `v` 开头（如 `v0.0.50`），否则 GitHub Action 不会触发
- 推送后无法撤销（GitHub Action 会立即开始构建）