#!/usr/bin/env node
// Build-only packaging: never installs, restarts services, or changes a Core.
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync, mkdirSync, mkdtempSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root=resolve(fileURLToPath(new URL('..',import.meta.url)));
const run=(cmd,args,cwd=root)=>execFileSync(cmd,args,{cwd,stdio:'inherit'});
const output=(cmd,args)=>execFileSync(cmd,args,{cwd:root,encoding:'utf8'}).trim();
const version=readFileSync(join(root,'internal/api/api.go'),'utf8').match(/const Version = "([^"]+)"/)?.[1];
if(version!=='0.6.0-alpha')throw new Error('Review release script/version before another release.');
run('npx',['vitest','run'],join(root,'panel'));
run('npx',['svelte-check','--tsconfig','./tsconfig.json'],join(root,'panel'));
run('npm',['run','build'],join(root,'panel'));
run('go',['test','-race','-timeout','90s','./...']);
run('go',['vet','./...']);
run('git',['diff','--check']);

// Include uncommitted AND untracked sources, and the actual embedded assets.
// HEAD alone cannot identify a working-tree release.
const files=[...new Set([
 ...output('git',['ls-files','-z','--cached','--others','--exclude-standard']).split('\0'),
 ...output('rg',['--files','--hidden','--no-ignore','internal/panel/dist']).split('\n'),
])].filter(Boolean).sort();
const source=createHash('sha256');
for(const file of files){source.update(file+'\0');source.update(readFileSync(join(root,file)));source.update('\0');}
const sourceSHA256=source.digest('hex');
const head=output('git',['rev-parse','HEAD']);
const dirty=output('git',['status','--porcelain']).length>0;
const revision=`${head.slice(0,7)}${dirty?'-dirty':''}-${sourceSHA256.slice(0,12)}`;
mkdirSync(join(root,'build'),{recursive:true});
const dest=mkdtempSync(join(root,'build',`cerveau-${version}-LABRIG-`));
run('go',['build','-trimpath','-ldflags',`-X cerveau/internal/api.BuildRevision=${revision}`,'-o',join(dest,'crv'),'./cmd/crv']);
run('go',['build','-trimpath','-o',join(dest,'crvcli'),'./cmd/crvcli']);
run('tar',['-czf',join(dest,'planner.tar.gz'),'-C',join(root,'rfx'),'planner']);
const manifest={version,codename:'LABRIG',revision,head,dirty,source_sha256:sourceSHA256,built_at:new Date().toISOString(),go:output('go',['version']),node:process.version,planner_version:'1.5.0',deployed:false};
writeFileSync(join(dest,'build.json'),JSON.stringify(manifest,null,2)+'\n');
const sums=['crv','crvcli','planner.tar.gz','build.json'].map(file=>`${createHash('sha256').update(readFileSync(join(dest,file))).digest('hex')}  ${file}`).join('\n')+'\n';
writeFileSync(join(dest,'SHA256SUMS'),sums);
run(join(dest,'crv'),['-version']);
run('sha256sum',['-c','SHA256SUMS'],dest);
console.log(`LABRIG build ready (not installed): ${dest}`);
