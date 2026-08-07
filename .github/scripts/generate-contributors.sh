#!/usr/bin/env bash
# generate-contributors.sh 从本仓库 git 提交历史生成 README 贡献者头像网格。
# 只统计往本仓库提交过的人（git log），不依赖上游。
# 头像来源：
#   - GitHub noreply email（id+user@users.noreply.github.com）→ GitHub avatar API
#   - 其他 email → Gravatar MD5（无需 GitHub 账号关联）
# 输出写入 README.md 的 <!-- contributors-start --> ... <!-- contributors-end --> 标记块。
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

README="README.md"
START="<!-- contributors-start -->"
END="<!-- contributors-end -->"

# 排除名单：不在 Contributors 里展示的提交者。
# 默认空 = 展示全部提交者（含上游原作者 leookun）。
# 用环境变量 CONTRIBUTOR_EXCLUDE 覆盖（空格分隔），如 CONTRIBUTOR_EXCLUDE="someone"。
EXCLUDE_DEFAULT=""
EXCLUDE="${CONTRIBUTOR_EXCLUDE:-$EXCLUDE_DEFAULT}"
exclude_match() {
  local needle="$1"
  [ -z "$EXCLUDE" ] && return 1
  local lower_needle
  lower_needle="$(printf '%s' "$needle" | tr '[:upper:]' '[:lower:]' | tr -d ' ')"
  [ -z "$lower_needle" ] && return 1
  for item in $EXCLUDE; do
    local lower_item
    lower_item="$(printf '%s' "$item" | tr '[:upper:]' '[:lower:]' | tr -d ' ')"
    [ "$lower_item" = "$lower_needle" ] && return 0
  done
  return 1
}

# 收集贡献者：按 email 去重，保留提交数最多的 name；排除名单跳过。
# 格式：count<TAB>name<TAB>email
declare -A best_count
declare -A best_name
while IFS=$'\t' read -r count name email; do
  [ -z "$email" ] && continue
  # 排除名单：按 name 和 email 中的 GitHub user 部分双重匹配。
  # email 形如 <id>+<user>@users.noreply.github.com → user 部分参与匹配。
  user_from_email=""
  case "$email" in
    *@users.noreply.github.com)
      user_from_email="${email%@users.noreply.github.com}"
      user_from_email="${user_from_email##*+}"  # 去掉 <id>+ 前缀（若有）
      ;;
  esac
  if exclude_match "$name" || exclude_match "$user_from_email" || exclude_match "$email"; then
    continue
  fi
  prev="${best_count[$email]:-0}"
  if [ "$count" -gt "$prev" ]; then
    best_count[$email]=$count
    best_name[$email]=$name
  fi
done < <(
  # %aN/%aE：应用 .mailmap 后的 name/email
  git log --use-mailmap --no-merges --pretty=format:'1	%aN	%aE' 2>/dev/null \
    | awk -F'\t' '{ c[$2"\t"$3]++ } END { for (k in c) print c[k]"\t"k }'
)

# 按 count 降序生成行
rows=""
for email in "${!best_count[@]}"; do
  count="${best_count[$email]}"
  name="${best_name[$email]}"
  # 转义 name 中的特殊字符
  name="${name//|/｜}"
  printf '%s\t%s\t%s\n' "$count" "$name" "$email"
done | sort -t$'\t' -k1,1 -rn | while IFS=$'\t' read -r count name email; do
  avatar=""
  link=""
  # GitHub noreply email：<id>+<user>@users.noreply.github.com 或 <user>@users.noreply.github.com
  if [[ "$email" =~ ^([0-9]+)\+[^@]+@users\.noreply\.github\.com$ ]]; then
    uid="${BASH_REMATCH[1]}"
    avatar="https://avatars.githubusercontent.com/u/${uid}?v=4&s=80"
  elif [[ "$email" =~ ^([^+]+)@users\.noreply\.github\.com$ ]]; then
    user="${BASH_REMATCH[1]}"
    avatar="https://avatars.githubusercontent.com/${user}?v=4&s=80"
    link="https://github.com/${user}"
  else
    # Gravatar：MD5(email 小写去空白)
    md5=$(printf '%s' "$email" | tr '[:upper:]' '[:lower:]' | tr -d ' ' | md5sum | cut -d' ' -f1)
    avatar="https://secure.gravatar.com/avatar/${md5}?d=identicon&s=80"
  fi
  # 默认链接：noreply 已解析为用户主页；其他 email 无可靠 GitHub 账号，链接到仓库主页
  if [ -z "$link" ]; then
    repo="${REPO:-$(git remote get-url origin 2>/dev/null | sed -n 's#.*github\.com[/:]\(.*\)\.git$#\1#p')}"
    link="https://github.com/${repo}"
  fi
  printf '| <a href="%s"><img src="%s" width="48" height="48" alt="%s" title="%s (%s 次提交)"/></a>\n' \
    "$link" "$avatar" "$name" "$name" "$count"
done > /tmp/contributors_rows.txt

# 组装新块（单行水平排列的头像网格，更紧凑美观）
new_block="${START}"$'\n'
new_block+='<table><tr>'$'\n'
# 把每个贡献者作为一列
prev_count=""
while IFS= read -r line; do
  [ -z "$line" ] && continue
  new_block+="<td>${line#| }</td>"$'\n'
done < /tmp/contributors_rows.txt
new_block+='</tr></table>'$'\n'
new_block+="${END}"

# 写回 README 标记块（若不存在则追加到末尾）
if grep -qF "${START}" "$README" 2>/dev/null; then
  # 用 awk 安全替换两个标记之间的内容
  tmp=$(mktemp)
  awk -v start="${START}" -v end="${END}" -v block="${new_block}" '
    $0 == start { print block; in_block=1; next }
    $0 == end { in_block=0; next }
    !in_block { print }
  ' "$README" > "$tmp"
  mv "$tmp" "$README"
else
  printf '\n%s\n' "${new_block}" >> "$README"
fi

echo "✓ Contributors grid generated: $(wc -l < /tmp/contributors_rows.txt) contributors"
