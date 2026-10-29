## v1.0.15

### 新功能

- **修复 Cursor 里选不到「最高」思考档位**：自定义接入（BYOK）模型在 Cursor 的 Effort 下拉里此前最多只到「极高（Extra High）」，即使渠道上限设为「最高」也不显示。现在 Effort 下拉完整提供 Low / Medium / High / Extra High / **Max**，选 Max 即按 `reasoning_effort: "max"` 发送。
- **自定义思考档位（新配置项）**：OpenAI 兼容渠道可在模型配置里添加厂商特有的思考档位（如 `adaptive`、`deep-think`，最多 8 个）。这些档位会同时出现在 Cursor 的 Effort 下拉与模型变体列表中，选中后**按原值透传**给服务端的 `reasoning_effort`，无需再手写「额外请求参数」JSON。渠道默认档位也可直接选自定义档位；显式选择的自定义档位优先于标准上限生效。
- **模型配置页思考档位改为快捷点击**：「思考强度上限」下拉框升级为一排可点击的档位按钮（低 / 中 / 高 / 极高 / 最高），点击即设为默认与上限。Anthropic 渠道同样改为快捷点击按钮（协议 effort 枚举固定，仍为五档标准值）。
- **目录推荐一键添加**：当模型命中内置能力目录且厂商提供非标准原生档位（如 `minimal`）时，编辑页自动显示推荐按钮，一键添加该档位并设为默认，无需手动输入。

### 优化

- 自定义档位带完整校验：仅支持字母/数字开头、可含 `-` 与 `_` 的 1~32 位标识符，不得与标准档位同名；非法值保存时报出具体是哪一项。Gemini 渠道档位映射为固定思考预算、Anthropic 渠道枚举由协议固定，二者的该配置自动忽略。
- 补充新界面词条的多语言目录与中 / 英 / 日 / 俄四语言错误文案。

### 更新说明

- 已识别的厂商兼容层（智谱 GLM / Kimi / 阶跃 / OpenRouter 等）仍按各自档位映射收编请求值：在这些渠道上配置的自定义档位会落到该厂商的默认档位，而非原样透传；自定义档位透传适用于标准 OpenAI 兼容端点与中转网关。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.15-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.15-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.15-windows-arm64-installer.exe` 或 `cursor-byok-1.0.15-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.15-windows-x32-installer.exe` 或 `cursor-byok-1.0.15-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.15-macos-arm64.dmg` 或 `cursor-byok-1.0.15-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.15-macos-x64.dmg` 或 `cursor-byok-1.0.15-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.15-linux-x64.tar.gz`

Windows 系统类型可在「设置 → 系统 → 关于 → 系统类型」查看；macOS 在「关于本机」查看芯片或处理器。
