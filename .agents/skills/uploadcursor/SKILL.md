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

**release-notes.md 必须包含「下载哪个文件？（按系统选择）」段落**，逐系统推荐下载文件名，并说明如何判断系统位数。完整模板见 v0.0.79 的 release-notes.md。

### 发布产物命名规范（不可随意更改）

用户可见的产物名 / update.json 平台 key 使用面向用户的命名，禁止出现 `386`/`amd64`/`darwin` 等技术代号：

| 平台 | 产物前缀（文件名 / update.json key） | 备注 |
| --- | --- | --- |
| Windows 64 位 | `windows-x64` | 绝大多数 Windows |
| Windows ARM64 | `windows-arm64` | 骁龙/麒麟 ARM 电脑 |
| Windows 32 位 | `windows-x32` | 极老电脑，`386` 是内部值 |
| macOS Apple Silicon | `macos-arm64` | M1/M2/M3/M4 |
| macOS Intel | `macos-x64` | |
| Linux 64 位 | `linux-x64` | |

约束：
- **内部实现可保留技术值**（GOARCH `386`、task 名 `build:windows:386`、NSIS 脚本 `project-386.nsi`），但**产物文件名、update.json 平台 key、release 资产名必须用上表命名**
- 改动命名必须**五处同步**：`Taskfile.yml` 产物名、`.github/workflows/build.yml`（matrix artifact / copy_asset / jq 平台校验）、`scripts/release/main.go` 的 `releaseAssets`、`internal/updater/manager.go` 的 `currentPlatformKey()`、`release-notes.md` 推荐下载名
- NSIS 打包的 makensis `File` 指令**不支持 `..` 段或混合分隔符路径**（报 `no files found`）；必须先复制二进制到 `build/windows/nsis/` 再用本地文件名，参考 `create:nsis:installer` 的复制步骤

### 4. 提交并创建 tag

**⚠️ 创建 tag 前必须自检版本号一致性**——否则会触发 `missing build asset` 报错（release job 按 tag 算版本号找文件名，build job 按 config.yml 构建文件名，macOS/Linux 产物名含版本号，对不上就失败）：

```bash
# config.yml 实际版本号必须 == 目标 tag 号（去掉 v 前缀），否则 STOP
TARGET="0.0.50"
ACTUAL="$(go run ./scripts/release version -config ./build/config.yml)"
if [ "$ACTUAL" != "$TARGET" ]; then
  echo "✗ config.yml=$ACTUAL 但目标 tag=v$TARGET，不一致！请回到步骤 2 改对版本号"
  exit 1
fi
echo "✓ 版本号一致，可以打 tag"
```

自检通过后，再提交并创建 tag：

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

1. 构建 6 个平台资产（Windows x64/arm64/x32、Linux x64、macOS arm64/x64 的 ZIP/DMG/tar.gz，命名见上表）
2. 生成 `update.json`（包含所有平台 checksum，平台 key 用上表命名）
3. 创建 GitHub Release（使用 `release-notes.md` 作为发布说明，必须含「下载哪个文件？」推荐段落）
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
- **版本号必须与 tag 一致**：macOS/Linux 产物名含版本号（`cursor-byok-X.Y.Z-macos-arm64.tar.gz`），config.yml 与 tag 不一致会导致 `missing build asset` 报错；步骤 4 的自检就是防这个。修复办法：改对 config.yml 版本号，强推 tag（`git tag -f vX && git push origin vX --force`）重新触发构建