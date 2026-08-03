#!/usr/bin/env node
// 清空视觉委派排查的干扰日志：
//   - app.log（forwarder 运行日志）
//   - crash.log
//   - history/<convId>/debug/*.jsonl（bidi/runSSE/provider 原始记录，体积最大）
// 仅删除本次测试前留下的旧记录，让新日志从零开始，方便对照 [VDBG] 时序。
const fs = require('fs');
const path = require('path');

const ROOT = 'C:/Users/Administrator/.cursor-local-assistant-v2';
const TARGETS = [
  path.join(ROOT, 'logs', 'app.log'),
  path.join(ROOT, 'logs', 'crash.log'),
];

let cleared = 0;
const skipped = [];

for (const file of TARGETS) {
  try {
    if (fs.existsSync(file)) {
      fs.rmSync(file, { force: true });
      console.log(`[clear] removed ${file}`);
      cleared++;
    } else {
      skipped.push(file);
    }
  } catch (err) {
    console.error(`[clear] FAILED ${file}: ${err.message}`);
  }
}

const historyDir = path.join(ROOT, 'history');
try {
  if (fs.existsSync(historyDir)) {
    for (const conv of fs.readdirSync(historyDir, { withFileTypes: true })) {
      if (!conv.isDirectory()) continue;
      const debugDir = path.join(historyDir, conv.name, 'debug');
      if (!fs.existsSync(debugDir)) continue;
      for (const f of fs.readdirSync(debugDir)) {
        if (!f.endsWith('.jsonl')) continue;
        const fp = path.join(debugDir, f);
        try {
          fs.rmSync(fp, { force: true });
          console.log(`[clear] removed ${fp}`);
          cleared++;
        } catch (err) {
          console.error(`[clear] FAILED ${fp}: ${err.message}`);
        }
      }
    }
  }
} catch (err) {
  console.error(`[clear] history scan failed: ${err.message}`);
}

for (const s of skipped) console.log(`[clear] skip (not exists): ${s}`);
console.log(`[clear] done. removed ${cleared} files.`);
