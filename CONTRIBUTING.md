# 贡献指南 / Contributing Guide

感谢你考虑为 cursor-byok 做出贡献！
Thank you for considering contributing to cursor-byok!

---

## 开发环境 / Prerequisites

| 依赖 / Dependency | 版本要求 / Version |
|------|---------|
| Go | >= 1.25 |
| Node.js | >= 20 |
| Yarn | 1.x (classic) |
| [Task](https://taskfile.dev) | >= 3 |
| [Wails v3 CLI](https://v3alpha.wails.dev) | alpha.74+ |

Linux 额外依赖 / Additional Linux dependencies: `libgtk-3-dev`, `libwebkit2gtk-4.1-dev`.

## 快速开始 / Quick Start

```bash
# 安装前端依赖 / Install frontend dependencies
cd frontend && yarn install --frozen-lockfile && cd ..

# 启动开发模式（热重载）/ Start dev mode (hot reload)
task dev

# 构建当前平台分发包 / Build for current platform
task build
```

## 项目结构 / Project Structure

```
├── main.go                 # 入口 / Entry point
├── internal/               # Go 后端（代理、转发、客户端管理）/ Go backend
├── frontend/               # Vue 3 + Vite + Tailwind 前端 / Frontend
│   ├── src/
│   │   ├── views/          # 页面 / Pages
│   │   ├── components/     # 组件 / Components
│   │   ├── i18n/           # 国际化 / i18n (zh-CN / en-US / ja-JP / ru-RU)
│   │   └── state/          # 全局状态 / Global state
│   └── plugins/            # Vite 插件 / Vite plugins
├── prompt/                 # 内置 Agent prompt 模板 / Built-in agent prompts
├── proto/                  # Protobuf 定义 / Protobuf definitions
├── build/                  # 构建配置与平台 Taskfile / Build configs
├── scripts/                # 辅助脚本 / Helper scripts
└── Taskfile.yml            # 顶层任务编排 / Top-level task orchestration
```

## 开发规范 / Development Guidelines

### 提交信息 / Commit Messages

采用 [Conventional Commits](https://www.conventionalcommits.org/) 风格：

```
feat(proxy): 支持自定义 upstream 超时
fix(i18n): 补全日语翻译缺失 key
release: 0.0.42
```

### 代码风格 / Code Style

- Go：遵循 `gofmt` / `go vet`，不引入额外 linter 配置。
  Follow `gofmt` / `go vet`; no additional linter config.
- 前端：Vue SFC + Composition API，Tailwind 工具类优先。
  Frontend: Vue SFC + Composition API, Tailwind utility-first.
- 新增 UI 文案必须同步更新所有 locale 文件（`frontend/src/i18n/locales/`）。
  New UI strings must be added to ALL locale files.

### 分支与 PR / Branching & PRs

1. 从 `main` 创建功能分支 / Create feature branches from `main`: `feat/xxx`, `fix/xxx`.
2. 保持 PR 小而聚焦 / Keep PRs small and focused.
3. PR 描述中说明动机和测试方式 / Describe motivation and how to test.

## 构建与发布 / Build & Release

```bash
# 构建全平台（仅 macOS 主机）/ Build all platforms (macOS host only)
task build:all

# 准备发布资产 / Prepare release assets
task release:prepare

# 发布到 GitHub Releases / Publish to GitHub Releases
task release:github
```

## 许可证 / License

提交代码即表示你同意以 [MIT License](./LICENSE) 授权你的贡献。
By contributing, you agree that your contributions will be licensed under the [MIT License](./LICENSE).