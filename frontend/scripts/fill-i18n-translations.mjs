import fs from "fs";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const localesDir = path.resolve(__dirname, "../src/i18n/locales");
const zh = JSON.parse(fs.readFileSync(path.join(localesDir, "zh-CN.json"), "utf8"));
const en = JSON.parse(fs.readFileSync(path.join(localesDir, "en-US.json"), "utf8"));
const ja = JSON.parse(fs.readFileSync(path.join(localesDir, "ja-JP.json"), "utf8"));

const enMap = {
  "全部模型": "All models",
  "· 已启用": "· Enabled",
  "供应商": "Provider",
  "刷新数据": "Refresh data",
  "点击进入 →": "Open →",
  "近24小时": "Last 24 hours",
  "浏览器预览示例": "Browser preview sample",
  "范围内": "In range",
  "删除模型": "Delete model",
  "当前还没有配置任何模型，点击右上角\"新增模型\"开始添加。":
    "No models configured yet. Click \"Add model\" in the top-right to get started.",
  "Codex-X · 自定义 · 中文化 · 默认关闭": "Codex-X · Custom · Software Chinese · Off by default",
  "个供应商 ·": " providers ·",
  "该供应商下暂无模型。点击上方\"拉取模型\"从远程获取，或\"新增模型\"手动添加。":
    "No models under this provider. Click \"Fetch models\" above to load remotely, or \"Add model\" to add manually.",
  "← 返回": "← Back",
  "快速添加模型": "Quick add models",
  "自定义注入": "Custom injection",
  "最多 10000 条": "Up to 10,000 records",
  "自定义": "Custom",
  "填写自定义提示词，启用后追加到系统提示词末尾；与 Codex-X 模板和中文化独立开关。":
    "Enter a custom prompt. When enabled, it is appended to the end of the system prompt. Independent from Codex-X templates and Software Chinese.",
  "例如：https://api.openai.com": "e.g. https://api.openai.com",
  "缓存率(%)": "Cache rate (%)",
  "近7天": "Last 7 days",
  "刷新中...": "Refreshing...",
  "浏览器预览示例模型": "Browser preview sample model",
  "{0}-副本": "{0}-copy",
  "会话分析": "Session analytics",
  "已选": "Selected",
  "下一页": "Next",
  "每页": "Per page",
  "正在读取请求明细…": "Loading request details…",
  "当日": "Today",
  "详情": "Details",
  "近3天": "Last 3 days",
  "当前供应商没有已有模型，无法确定拉取参数":
    "This provider has no existing models, so fetch parameters cannot be determined",
  "确定删除「{0}」吗？": "Delete \"{0}\"?",
  "接口地址（自动补 /v1）": "API base URL (/v1 is appended automatically)",
  "近30天": "Last 30 days",
  "次请求 · 分桶粒度": " requests · bucket size",
  "著者": "Author",
  "输入自定义注入内容，留空则不注入...": "Enter custom injection content. Leave empty to skip...",
  "已记录 usage": "Usage recorded",
  "上一页": "Previous",
  "加载失败": "Failed to load",
  "模型数": "Models",
  "小时/桶": "h/bucket",
  "提示词模式": "Prompt mode",
  "新增模型": "Add model",
  "模型类型": "Model type",
  "总消耗": "Total usage",
  "可选，例如：用于日常代码补全": "Optional, e.g. for daily code completion",
  "条 · 显示": " items · showing",
  "供应商详情": "Provider details",
};

const jaMap = {
  "全部模型": "すべてのモデル",
  "· 已启用": "· 有効",
  "供应商": "プロバイダー",
  "刷新数据": "データを更新",
  "点击进入 →": "開く →",
  "近24小时": "直近24時間",
  "浏览器预览示例": "ブラウザプレビュー例",
  "范围内": "範囲内",
  "删除模型": "モデルを削除",
  "当前还没有配置任何模型，点击右上角\"新增模型\"开始添加。":
    "まだモデルがありません。右上の「モデルを追加」から設定を開始してください。",
  "Codex-X · 自定义 · 中文化 · 默认关闭": "Codex-X · カスタム · ソフト中国語化 · 既定はオフ",
  "个供应商 ·": " 件のプロバイダー ·",
  "该供应商下暂无模型。点击上方\"拉取模型\"从远程获取，或\"新增模型\"手动添加。":
    "このプロバイダーにモデルがありません。上の「モデルを取得」でリモート取得するか、「モデルを追加」で手動追加してください。",
  "← 返回": "← 戻る",
  "快速添加模型": "モデルをクイック追加",
  "自定义注入": "カスタム注入",
  "最多 10000 条": "最大 10000 件",
  "自定义": "カスタム",
  "填写自定义提示词，启用后追加到系统提示词末尾；与 Codex-X 模板和中文化独立开关。":
    "カスタムプロンプトを入力します。有効化するとシステムプロンプト末尾に追記されます。Codex-X テンプレートおよびソフト中国語化とは独立したスイッチです。",
  "例如：https://api.openai.com": "例：https://api.openai.com",
  "缓存率(%)": "キャッシュ率(%)",
  "近7天": "直近7日",
  "刷新中...": "更新中...",
  "浏览器预览示例模型": "ブラウザプレビュー用サンプルモデル",
  "{0}-副本": "{0}-コピー",
  "会话分析": "セッション分析",
  "已选": "選択済み",
  "下一页": "次へ",
  "每页": "ページあたり",
  "正在读取请求明细…": "リクエスト明細を読み込み中…",
  "当日": "本日",
  "详情": "詳細",
  "近3天": "直近3日",
  "当前供应商没有已有模型，无法确定拉取参数":
    "このプロバイダーに既存モデルがないため、取得パラメータを決定できません",
  "确定删除「{0}」吗？": "「{0}」を削除しますか？",
  "接口地址（自动补 /v1）": "API アドレス（/v1 を自動補完）",
  "近30天": "直近30日",
  "次请求 · 分桶粒度": " 件のリクエスト · バケット粒度",
  "著者": "著者",
  "输入自定义注入内容，留空则不注入...": "カスタム注入内容を入力。空の場合は注入しません...",
  "已记录 usage": "usage を記録済み",
  "上一页": "前へ",
  "加载失败": "読み込みに失敗しました",
  "模型数": "モデル数",
  "小时/桶": "時間/バケット",
  "提示词模式": "プロンプトモード",
  "新增模型": "モデルを追加",
  "模型类型": "モデル種別",
  "总消耗": "合計使用量",
  "可选，例如：用于日常代码补全": "任意。例：日常のコード補完用",
  "条 · 显示": " 件 · 表示",
  "供应商详情": "プロバイダー詳細",
};

let filledEn = 0;
let filledJa = 0;
const missingEn = [];
const missingJa = [];

for (const [id, source] of Object.entries(zh)) {
  if (!en[id]) {
    if (Object.prototype.hasOwnProperty.call(enMap, source)) {
      en[id] = enMap[source];
      filledEn += 1;
    } else {
      missingEn.push([id, source]);
    }
  }
  if (!ja[id]) {
    if (Object.prototype.hasOwnProperty.call(jaMap, source)) {
      ja[id] = jaMap[source];
      filledJa += 1;
    } else {
      missingJa.push([id, source]);
    }
  }
}

function sortObject(obj) {
  return Object.fromEntries(Object.keys(obj).sort().map((key) => [key, obj[key]]));
}

fs.writeFileSync(path.join(localesDir, "en-US.json"), `${JSON.stringify(sortObject(en), null, 2)}\n`);
fs.writeFileSync(path.join(localesDir, "ja-JP.json"), `${JSON.stringify(sortObject(ja), null, 2)}\n`);

console.log(JSON.stringify({ filledEn, filledJa, missingEn, missingJa }, null, 2));
if (missingEn.length || missingJa.length) {
  process.exitCode = 1;
}