import fs from 'node:fs';
const html = fs.readFileSync(new URL('../research/pms.html', import.meta.url), 'utf8');
const marker = 'const __redoc_state = ';
const start = html.indexOf(marker) + marker.length;
if (start < marker.length) throw new Error('Plex documentation state not found');
const end = html.indexOf(';', start);
// The generated documentation uses a JSON literal, never evaluate downloaded code.
let state;
for (let pos = end; pos >= 0; pos = html.indexOf(';', pos + 1)) {
  try { state = JSON.parse(html.slice(start, pos)); break; } catch {}
}
if (!state) throw new Error('Could not parse documentation JSON');
const spec = state.spec?.data ?? state.spec;
if (!spec?.paths) throw new Error('OpenAPI paths not found');
fs.writeFileSync(new URL('../research/pms.openapi.json', import.meta.url), JSON.stringify(spec, null, 2));
console.log(JSON.stringify({version: spec.info.version, paths: Object.keys(spec.paths).length}));
