const { execSync } = require('child_process');
const out = execSync('curl.exe -s "https://api.github.com/repos/Sakana-yuyu/cursor-byok/releases?per_page=10"', { encoding: 'utf8', maxBuffer: 30 * 1024 * 1024 });
const rels = JSON.parse(out);
for (const r of rels) {
  console.log(`tag=${r.tag_name} name=${r.name} draft=${r.draft} prerelease=${r.prerelease} assets=${r.assets.length} created=${r.created_at} url=${r.html_url}`);
}
if (!Array.isArray(rels) || rels.length === 0) console.log('no releases or error:', rels.message || rels);