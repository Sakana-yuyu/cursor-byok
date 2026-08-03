const { execSync } = require('child_process');

function query() {
  try {
    const out = execSync('curl.exe -s "https://api.github.com/repos/Sakana-yuyu/cursor-byok/actions/runs/30789537124"', { encoding: 'utf8', maxBuffer: 20 * 1024 * 1024 });
    const json = JSON.parse(out);
    return { status: json.status, conclusion: json.conclusion, updated: json.updated_at, run: json.run_number };
  } catch (e) {
    return { status: 'query_error', conclusion: e.message.slice(0, 120), updated: '', run: 0 };
  }
}

(async () => {
  for (let i = 0; i < 30; i++) {
    const r = query();
    console.log(`poll ${i}: run=${r.run} status=${r.status} conclusion=${r.conclusion} updated=${r.updated}`);
    if (r.status === 'completed') process.exit(0);
    await new Promise((resolve) => setTimeout(resolve, 60000));
  }
  process.exit(1);
})();