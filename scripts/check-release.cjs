const { execSync } = require('child_process');

function api(url) {
  const out = execSync(`curl.exe -s "${url}"`, { encoding: 'utf8', maxBuffer: 30 * 1024 * 1024 });
  return JSON.parse(out);
}

const rel = api('https://api.github.com/repos/Sakana-yuyu/cursor-byok/releases/tags/v0.0.78');
console.log('=== release ===');
if (rel.id) {
  console.log('name:', rel.name);
  console.log('assets:', rel.assets.map((a) => a.name).join('\n  '));
} else {
  console.log('NOT FOUND:', rel.message || 'no release');
}

console.log('=== run jobs ===');
const run = api('https://api.github.com/repos/Sakana-yuyu/cursor-byok/actions/runs/30789537124/jobs?per_page=20');
for (const job of run.jobs || []) {
  console.log(`job: ${job.name} | status=${job.status} | conclusion=${job.conclusion}`);
  if (job.steps) {
    for (const s of job.steps) {
      console.log(`  step: ${s.name} | ${s.status} ${s.conclusion || ''}`);
    }
  }
}