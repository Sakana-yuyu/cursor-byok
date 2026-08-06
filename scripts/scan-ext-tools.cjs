const fs = require('fs');
for (const f of ['cursor-agent-exec', 'cursor-local-agent-runtime']) {
  const s = fs.readFileSync('d:/cursor/resources/app/extensions/' + f + '/dist/main.js', 'utf8');
  const names = [...s.matchAll(/type:"function",function:\{name:"([^"]+)"/g)].map(m => m[1]);
  console.log(f, 'tool defs:', names.length);
  console.log('names:', names.join(','));
}