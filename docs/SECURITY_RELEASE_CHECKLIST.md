# 发布前安全检查

这份清单用于每次推送 GitHub、打包 Release 或向社区发布截图/日志之前的人工检查。

## 不得上传

- API Key、访问令牌、Cookie、密码和私钥
- 用户目录下的 config.yaml、ca.key、历史记录、数据库和完整日志
- bin/、frontend/dist/、frontend/node_modules/ 等本地构建产物
- .superpowers/、.worktrees/、.reasonix/、临时监控脚本和运行状态文件
- 包含真实请求内容、供应商返回内容或账号标识的截图和录屏

## 推送前命令

    git status --short
    git ls-files | Select-String -Pattern '(^|/)(\.env|.*\.key|.*\.pem|logs?|dist|bin|node_modules)(/|$)'
    $sk = ((115,107,45 | ForEach-Object { [char]$_ }) -join '')
    $google = ((65,73,122,97 | ForEach-Object { [char]$_ }) -join '')
    $github = ((103,104,112,95 | ForEach-Object { [char]$_ }) -join '')
    $privateKey = ('-----' + 'BEGIN .*PRIVATE KEY' + '-----')
    $pattern = ($sk + '[A-Za-z0-9]{12,}|' + $google + '[A-Za-z0-9_-]{20,}|' + $github + '[A-Za-z0-9]{20,}|' + $privateKey)
    rg -n --hidden --glob '!frontend/node_modules/**' --glob '!frontend/dist/**' $pattern .

如果发现已经进入 Git 历史的凭据，先立即撤销/轮换凭据，再根据仓库发布策略清理历史；仅删除工作区文件不足以让已经公开的密钥失效。
