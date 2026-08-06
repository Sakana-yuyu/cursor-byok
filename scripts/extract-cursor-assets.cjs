// extract-cursor-assets.cjs 从本机 Cursor 客户端扩展提取原生资产：
// 1. Auto-review 安全分类器提示词（cursor-agent-exec / cursor-local-agent-runtime）
// 2. 本地执行工具定义（ReadFile/ListDir/Grep/Glob）JSON
// 输出到 prompt/native/ 目录，供 cursor-byok 校准提示词与工具描述。
const fs = require('fs');
const path = require('path');

const EXT_DIR = 'd:/cursor/resources/app/extensions';
const OUT_DIR = path.join(__dirname, '..', 'prompt', 'native');

const SOURCES = ['cursor-agent-exec', 'cursor-local-agent-runtime'];

// Auto-review 分类器提示词的特征开头（bundle 中单引号转义为 \'）
const CLASSIFIER_START = "Auto-review security classifier";

function extractClassifier(src) {
  const i = src.indexOf(CLASSIFIER_START);
  if (i < 0) return null;
  // 找到包含该特征的字符串字面量起点（向前找最近的 ' 或 " 定界符）
  let start = -1;
  let quote = '';
  for (let j = i; j >= 0; j--) {
    const c = src[j];
    if (c === "'" || c === '"') {
      // 跳过被反斜杠转义的定界符
      if (j > 0 && src[j - 1] === '\\') continue;
      start = j;
      quote = c;
      break;
    }
  }
  const end = (() => {
    // 跳过被反斜杠转义的定界符（如单引号字符串中的 \'s）
    for (let j = i + CLASSIFIER_START.length; j < src.length; j++) {
      if (src[j] === quote && src[j - 1] !== '\\') return j;
    }
    return -1;
  })();
  if (start < 0 || end <= i) return null;
  let text = src.slice(start + 1, end);
  // 反转义
  text = text.replace(/\\n/g, '\n').replace(/\\t/g, '\t').replace(/\\\\/g, '\\').replace(/\\'/g, "'").replace(/\\"/g, '"');
  // 处理字符串拼接（`'...' + '...'` 或变量赋值后拼接）
  let cursor = end;
  let guard = 0;
  while (guard++ < 50) {
    const after = src.slice(cursor, cursor + 40);
    const m = after.match(/^\s*\+\s*(['"])([\s\S]*?)\1/);
    if (!m) break;
    text += m[2].replace(/\\n/g, '\n').replace(/\\t/g, '\t').replace(/\\\\/g, '\\').replace(/\\'/g, "'").replace(/\\"/g, '"');
    cursor += after.indexOf(m[0]) + m[0].length;
  }
  return text;
}

function extractTools(src) {
  // 提取 {type:"function",function:{name,description,parameters}} 结构
  const results = [];
  const re = /type:"function",function:\{name:"([^"]+)",description:"([\s\S]*?)",parameters:(\{[\s\S]*?\})\}/g;
  let m;
  while ((m = re.exec(src)) !== null) {
    let desc = m[2].replace(/\\n/g, '\n').replace(/\\"/g, '"').replace(/\\'/g, "'").replace(/\\\\/g, '\\');
    let params = m[3].replace(/\\"/g, '"').replace(/\\'/g, "'").replace(/\\\\/g, '\\');
    try { params = JSON.parse(params); } catch { params = { raw: params.slice(0, 500) }; }
    results.push({ name: m[1], description: desc, parameters: params });
  }
  return results;
}

function main() {
  fs.mkdirSync(OUT_DIR, { recursive: true });
  let bestClassifier = null;
  const toolsByName = new Map();
  for (const srcName of SOURCES) {
    const file = path.join(EXT_DIR, srcName, 'dist', 'main.js');
    if (!fs.existsSync(file)) { console.log('skip missing:', file); continue; }
    const src = fs.readFileSync(file, 'utf8');
    const classifier = extractClassifier(src);
    if (classifier && (!bestClassifier || classifier.length > bestClassifier.length)) {
      bestClassifier = classifier;
    }
    for (const t of extractTools(src)) {
      if (!toolsByName.has(t.name)) toolsByName.set(t.name, t);
    }
  }

  if (bestClassifier) {
    const out = path.join(OUT_DIR, 'auto_review_classifier.md');
    fs.writeFileSync(out, bestClassifier + '\n');
    console.log('classifier:', bestClassifier.length, 'chars ->', out);
  } else {
    console.log('classifier: NOT FOUND');
  }

  if (toolsByName.size > 0) {
    const out = path.join(OUT_DIR, 'local_exec_tools.json');
    fs.writeFileSync(out, JSON.stringify([...toolsByName.values()], null, 2));
    console.log('tools:', [...toolsByName.keys()].join(','), '->', out);
  } else {
    console.log('tools: NOT FOUND');
  }
}

main();