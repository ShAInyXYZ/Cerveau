#!/usr/bin/env node
// Build-only native Reflex bundle. No installation, downloads or services.
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmodSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, readlinkSync, symlinkSync, writeFileSync } from 'node:fs';
import { dirname, isAbsolute, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const testFile = name => name.endsWith('_test.go') || name.endsWith('.test.mjs');
const sourceInfo = path => {
  const stat=lstatSync(path);
  if(stat.isSymbolicLink()) throw new Error(`refusing source symlink: ${path}`);
  if(!stat.isDirectory()&&!stat.isFile()) throw new Error(`refusing nonregular source: ${path}`);
  return stat;
};

/** Copy bytes without transformation, then verify against the observed source. */
export function copyVerifiedTree(source,destination,{label,copied,excludeTests=false},sub='') {
  const stat=sourceInfo(source);
  if(stat.isDirectory()) {
    mkdirSync(destination,{recursive:true,mode:0o755});
    for(const name of readdirSync(source).sort()) {
      if(excludeTests&&testFile(name)) continue;
      copyVerifiedTree(join(source,name),join(destination,name),{label,copied,excludeTests},sub?`${sub}/${name}`:name);
    }
  } else {
    const bytes=readFileSync(source),sha256=hash(bytes);
    mkdirSync(dirname(destination),{recursive:true,mode:0o755});
    writeFileSync(destination,bytes,{flag:'wx',mode:stat.mode&0o111?0o755:0o644});
    if(hash(readFileSync(destination))!==sha256||hash(readFileSync(source))!==sha256) throw new Error(`copy verification failed or source changed: ${source}`);
    copied.push({source:label,path:sub||label,sha256,bytes:bytes.length});
  }
}

/** Inventory regular bytes and the one intentional internal launcher symlink. */
export function inventoryTree(root,sub='') {
  const items=[];
  for(const name of readdirSync(join(root,sub)).sort()) {
    const path=sub?`${sub}/${name}`:name,abs=join(root,path),stat=lstatSync(abs);
    if(stat.isSymbolicLink()) {
      const target=readlinkSync(abs);
      if(path!=='bin/rfx-devcheck'||target!=='devcheck/rfx-devcheck.mjs') throw new Error(`unexpected bundle symlink: ${path}`);
      items.push({path,type:'symlink',target,sha256:hash(Buffer.from(target))});
    } else if(stat.isDirectory()) items.push(...inventoryTree(root,path));
    else if(stat.isFile()) { const bytes=readFileSync(abs); items.push({path,type:'file',sha256:hash(bytes),bytes:bytes.length}); }
    else throw new Error(`unexpected bundle entry: ${path}`);
  }
  return items;
}

function scanSources(source,excludeTests=false) {
  const stat=sourceInfo(source);
  if(stat.isDirectory()) for(const name of readdirSync(source)) { if(excludeTests&&testFile(name)) continue; scanSources(join(source,name),excludeTests); }
}

function sourcePath(root,path) {
  let current=root;
  for(const part of path.split('/')) { current=join(current,part); sourceInfo(current); }
  return current;
}

/** Deliberately broader than the two helpers: all visible repository Go files,
 * tests included, module locks and the helper's embedded JavaScript bridge.
 * This identifies dirty/untracked input bytes instead of relying on HEAD. */
export function fingerprintGoSources(root,paths) {
  sourceInfo(root);
  const sorted=[...new Set(paths)].sort();
  if(sorted.length>10000) throw new Error('Go source inventory exceeds 10000 files');
  const files=[]; let total=0;
  for(const path of sorted) {
    if(isAbsolute(path)||path.includes('\\')||path.includes('\0')||path.split('/').some(part=>!part||part==='.'||part==='..')) throw new Error(`invalid source path: ${path}`);
    const file=sourcePath(root,path),stat=sourceInfo(file);
    if(!stat.isFile()||stat.size>16*1024*1024) throw new Error(`invalid or oversized Go source: ${path}`);
    const bytes=readFileSync(file); total+=bytes.length;
    if(total>128*1024*1024) throw new Error('Go source inventory exceeds 128 MiB');
    files.push({path,sha256:hash(bytes),bytes:bytes.length});
  }
  const aggregate=createHash('sha256');
  for(const file of files) aggregate.update(`${file.path}\0${file.sha256}\0${file.bytes}\0`);
  return {scope:'All Git-visible repository Go source and test files, go.mod, go.sum and internal/rfxdgv/bridge.mjs; intentionally broader than the helper dependency closure',sha256:aggregate.digest('hex'),files};
}

function goSourceSnapshot(repoRoot) {
  const raw=execFileSync('git',['-c','core.fsmonitor=false','ls-files','-z','--cached','--others','--exclude-standard','--','*.go','go.mod','go.sum','internal/rfxdgv/bridge.mjs'],{cwd:repoRoot,encoding:'utf8',maxBuffer:4*1024*1024,env:{...process.env,GIT_OPTIONAL_LOCKS:'0'}});
  const paths=raw.split('\0').filter(Boolean);
  for(const required of ['go.mod','go.sum','cmd/rfx-dgv/main.go','cmd/rfx-github/main.go','internal/rfxdgv/bridge.mjs']) if(!paths.includes(required)) throw new Error(`required Go source not in inventory: ${required}`);
  return fingerprintGoSources(repoRoot,paths);
}

function output(program,args,cwd) { return execFileSync(program,args,{cwd,encoding:'utf8',maxBuffer:1024*1024}).trim(); }

export function buildNativeRfx({repoRoot,dgvRoot,outputParent,compile}={}) {
  repoRoot=resolve(repoRoot??fileURLToPath(new URL('..',import.meta.url)));
  dgvRoot=resolve(dgvRoot??process.env.CERVEAU_DGV_SOURCE??join(dirname(repoRoot),'Dia-GramV'));
  outputParent=resolve(outputParent??join(repoRoot,'build'));
  sourceInfo(repoRoot); sourceInfo(dgvRoot);
  const packages=['dgv','github','devcheck'];
  const engineSources=['package.json','LICENSE','packages/core/package.json','packages/core/src','node_modules/@dagrejs/dagre','node_modules/@dagrejs/graphlib'];
  for(const source of engineSources) scanSources(sourcePath(dgvRoot,source));
  for(const pack of packages) scanSources(sourcePath(repoRoot,`rfx/${pack}`),true);
  scanSources(sourcePath(repoRoot,'scripts/devcheck'),true);
  for(const helper of ['rfx-dgv','rfx-github']) sourcePath(repoRoot,`cmd/${helper}/main.go`);
  const engine=JSON.parse(readFileSync(join(dgvRoot,'packages/core/package.json'),'utf8'));
  if(engine.name!=='@dgv/core') throw new Error('unexpected DGV engine identity');
  const dependencies={};
  for(const name of ['dagre','graphlib']) {
    const metadata=JSON.parse(readFileSync(join(dgvRoot,'node_modules/@dagrejs',name,'package.json'),'utf8'));
    if(metadata.name!==`@dagrejs/${name}`) throw new Error(`unexpected dependency identity: ${name}`);
    dependencies[metadata.name]=metadata.version;
    if(!readdirSync(join(dgvRoot,'node_modules/@dagrejs',name)).some(file=>/^licen[sc]e/i.test(file))) throw new Error(`missing dependency license: ${name}`);
  }
  const versions=Object.fromEntries(packages.map(pack=>{
    const source=readFileSync(join(repoRoot,'rfx',pack,'pack.yaml'),'utf8');
    const name=source.match(/^pack:\s*([a-z0-9-]+)\s*$/m)?.[1];
    const version=source.match(/^version:\s*([0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?)\s*$/m)?.[1];
    if(name!==pack||!version) throw new Error(`invalid pack identity: ${pack}`);
    return [pack,version];
  }));
  const revision=output('git',['rev-parse','HEAD'],dgvRoot);
  const dirty=output('git',['status','--porcelain','--','package.json','LICENSE','packages/core'],dgvRoot).length>0;
  const sourceRevision=output('git',['rev-parse','HEAD'],repoRoot);
  const sourceDirty=output('git',['status','--porcelain'],repoRoot).length>0;
  // The PATH launcher may be older than the cached module toolchain. Resolve
  // that already-installed toolchain with downloads disabled, then invoke it
  // directly with local-only mode. Missing toolchains fail before copying.
  const offline={...process.env,GOPROXY:'off',GOSUMDB:'off',GOTOOLCHAIN:'local'};
  const localGo=JSON.parse(execFileSync('go',['env','-json','GOROOT','GOVERSION','GOMODCACHE','GOOS','GOARCH'],{cwd:repoRoot,env:offline,encoding:'utf8',maxBuffer:1024*1024}));
  const required=readFileSync(join(repoRoot,'go.mod'),'utf8').match(/^go\s+([0-9.]+)\s*$/m)?.[1];
  if(!required) throw new Error('missing explicit Go toolchain requirement');
  const versionScore=v=>{ const parts=v.replace(/^go/,'').split('.'); return [0,1,2].reduce((score,i)=>score*1000+Number(parts[i]??0),0); };
  const goRoot=versionScore(localGo.GOVERSION)>=versionScore(required)?localGo.GOROOT:
    join(localGo.GOMODCACHE,`golang.org/toolchain@v0.0.1-go${required}.${localGo.GOOS}-${localGo.GOARCH}`);
  const goBinary=join(goRoot,'bin/go'); sourceInfo(goBinary);
  const goSources=goSourceSnapshot(repoRoot);
  mkdirSync(outputParent,{recursive:true,mode:0o755});
  const dest=mkdtempSync(join(outputParent,'rfx-native-'));
  const copied=[];
  for(const path of engineSources) copyVerifiedTree(join(dgvRoot,path),join(dest,'bin/dgv-engine',path),{label:`dgv/${path}`,copied});
  for(const pack of packages) copyVerifiedTree(join(repoRoot,'rfx',pack),join(dest,'rfx',pack),{label:`cerveau/rfx/${pack}`,copied,excludeTests:true});
  copyVerifiedTree(join(repoRoot,'scripts/devcheck'),join(dest,'bin/devcheck'),{label:'cerveau/scripts/devcheck',copied,excludeTests:true});
  chmodSync(join(dest,'bin/devcheck/rfx-devcheck.mjs'),0o755);
  symlinkSync('devcheck/rfx-devcheck.mjs',join(dest,'bin/rfx-devcheck'));
  const compiler=compile??((helper,target)=>execFileSync(goBinary,['build','-trimpath','-o',target,`./cmd/${helper}`],{cwd:repoRoot,stdio:'inherit',env:{...offline,GOTOOLCHAIN:'local'}}));
  for(const helper of ['rfx-dgv','rfx-github']) compiler(helper,join(dest,'bin',helper));
  if(goSourceSnapshot(repoRoot).sha256!==goSources.sha256) throw new Error('Go source changed during compilation; incomplete bundle retained, rebuild after writers settle');
  const provenance={schema:'cerveau.rfx-native-bundle.v1',built_at:new Date().toISOString(),installed:false,
    cerveau:{revision:sourceRevision,dirty:sourceDirty,go_source:goSources},packs:versions,
    dgv:{revision,dirty,version:engine.version,dependencies,source:dgvRoot,files:copied.filter(file=>file.source.startsWith('dgv/'))},
    source_files:copied.filter(file=>!file.source.startsWith('dgv/')),
    runtime:{node:process.version,go:output(goBinary,['version'],repoRoot),requirements:['Node on trusted PATH','Git for Github talents; authenticated gh only for explicit GitHub actions','DevCheck: existing Playwright module and Chromium, configured with CERVEAU_PLAYWRIGHT_MODULE and CERVEAU_CHROMIUM; no downloads included']},
    helpers:['bin/rfx-dgv','bin/rfx-github','bin/rfx-devcheck'],
  };
  writeFileSync(join(dest,'provenance.json'),JSON.stringify(provenance,null,2)+'\n',{flag:'wx'});
  const inventory=inventoryTree(dest);
  writeFileSync(join(dest,'inventory.json'),JSON.stringify({schema:'cerveau.rfx-native-inventory.v1',files:inventory},null,2)+'\n',{flag:'wx'});
  const checksums=[...inventory.filter(file=>file.type==='file').map(file=>`${file.sha256}  ${file.path}`),`${hash(readFileSync(join(dest,'inventory.json')))}  inventory.json`];
  writeFileSync(join(dest,'SHA256SUMS'),checksums.join('\n')+'\n',{flag:'wx'});
  return {directory:dest,provenance,inventory};
}

if(process.argv[1]&&resolve(process.argv[1])===fileURLToPath(import.meta.url)) {
  if(process.argv.length>3) throw new Error('usage: node scripts/build-rfx-packs.mjs [original-Dia-GramV-checkout]');
  const built=buildNativeRfx({dgvRoot:process.argv[2]});
  console.log(`Native RFX bundle ready (not installed): ${built.directory}`);
}
