// Run through Devbox after dependency changes. Retain notices from runtime modules.
import {execFileSync} from 'node:child_process';
import {mkdirSync, readFileSync, readdirSync, existsSync, writeFileSync} from 'node:fs';
import {join} from 'node:path';
const raw=execFileSync('go',['list','-deps','-json','./cmd/ted'],{encoding:'utf8',env:{...process.env,GOOS:'linux',GOARCH:'amd64',CGO_ENABLED:'0'}});
const packages=JSON.parse('['+raw.trim().replace(/}\s*\n{/g,'},{')+']');
const modules=new Map();
for(const pkg of packages){if(pkg.Module&&!pkg.Module.Main)modules.set(pkg.Module.Path,pkg.Module);}
mkdirSync('third_party',{recursive:true});
const rows=['# Go runtime dependency notices','','Generated from the pinned modules used by `cmd/ted`. Original license and notice files follow in the linked directories. Regenerate with `devbox run -- node scripts/licenses.mjs` after changing dependencies.','','| Module | Version | Notices |','| --- | --- | --- |'];
for(const [name,m] of [...modules].sort(([a],[b])=>a.localeCompare(b))){
 const files=readdirSync(m.Dir).filter(n=>/^(LICENSE|NOTICE|COPYING)(\..*)?$/i.test(n));
 if(!files.length)throw new Error(`No license found for ${name}`);
 const folder=name.replaceAll('/','_');mkdirSync(join('third_party',folder),{recursive:true});
 for(const file of files){writeFileSync(join('third_party',folder,file),readFileSync(join(m.Dir,file)));}
 rows.push(`| ${name} | ${m.Version} | ${files.map(f=>`[${f}](${folder}/${f})`).join(', ')} |`);
}
const goroot=execFileSync('go',['env','GOROOT'],{encoding:'utf8'}).trim();
mkdirSync('third_party/go',{recursive:true});
if(existsSync(join(goroot,'LICENSE')))writeFileSync('third_party/go/LICENSE',readFileSync(join(goroot,'LICENSE')));
else if(!existsSync('third_party/go/LICENSE'))throw new Error('Go license missing; obtain it from the exact official Go release tag');
writeFileSync('third_party/README.md',rows.join('\n')+'\n\nThe Go standard library is covered by [the Go license](go/LICENSE), retained from the official go1.27.0 source tag when the Nix toolchain omits GOROOT/LICENSE. Debian package notices (including FFmpeg) remain under `/usr/share/doc` in the application image.\n');
