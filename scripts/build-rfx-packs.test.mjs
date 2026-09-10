import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, symlinkSync, readFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { copyVerifiedTree, inventoryTree, buildNativeRfx, fingerprintGoSources } from './build-rfx-packs.mjs';

const temporary = () => mkdtempSync(join(tmpdir(), 'rfx-bundle-test-'));

test('Go source fingerprint is deterministic, covers bridge and changes, and rejects symlinks', () => {
  const root=temporary(); mkdirSync(join(root,'internal'));
  writeFileSync(join(root,'go.mod'),'module fixture\ngo 1.25.0\n');
  writeFileSync(join(root,'go.sum'),'fixture sums\n');
  writeFileSync(join(root,'internal','app.go'),'package fixture\n');
  writeFileSync(join(root,'internal','bridge.mjs'),'export const value = 1;\n');
  const paths=['go.mod','go.sum','internal/app.go','internal/bridge.mjs'];
  const first=fingerprintGoSources(root,paths);
  assert.equal(first.sha256,fingerprintGoSources(root,[...paths].reverse()).sha256);
  assert.equal(first.files.length,4); assert.equal(first.files.find(file=>file.path==='internal/bridge.mjs').bytes,24);
  writeFileSync(join(root,'internal','app.go'),'package changed\n');
  assert.notEqual(first.sha256,fingerprintGoSources(root,paths).sha256);
  writeFileSync(join(root,'internal','bridge.mjs'),'export const value = 2;\n');
  assert.notEqual(first.files.find(file=>file.path==='internal/bridge.mjs').sha256,fingerprintGoSources(root,paths).files.find(file=>file.path==='internal/bridge.mjs').sha256);
  symlinkSync(join(root,'internal','app.go'),join(root,'alias.go'));
  assert.throws(()=>fingerprintGoSources(root,[...paths,'alias.go']),/symlink/);
  assert.throws(()=>fingerprintGoSources(root,[...paths,'../outside.go']),/source path/);
});
test('copy is exact, excludes tests, records provenance and rejects symlinks', () => {
  const root=temporary(), source=join(root,'source'), dest=join(root,'dest');
  mkdirSync(source); writeFileSync(join(source,'pack.yaml'),'pack: fixture\n'); writeFileSync(join(source,'pack_test.go'),'test');
  const copied=[]; copyVerifiedTree(source,dest,{label:'fixture',copied,excludeTests:true});
  assert.equal(readFileSync(join(dest,'pack.yaml'),'utf8'),'pack: fixture\n');
  assert.equal(existsSync(join(dest,'pack_test.go')),false); assert.equal(copied.length,1); assert.match(copied[0].sha256,/^[a-f0-9]{64}$/);
  symlinkSync(join(source,'pack.yaml'),join(source,'alias'));
  assert.throws(()=>copyVerifiedTree(source,join(root,'other'),{label:'fixture',copied:[]}),/symlink/);
});

test('inventory records only explicitly allowed internal helper symlink', () => {
  const root=temporary(); mkdirSync(join(root,'bin')); mkdirSync(join(root,'bin','devcheck'));
  writeFileSync(join(root,'bin','devcheck','rfx-devcheck.mjs'),'#!/usr/bin/env node\n');
  symlinkSync('devcheck/rfx-devcheck.mjs',join(root,'bin','rfx-devcheck'));
  const items=inventoryTree(root); assert.equal(items.find(x=>x.path==='bin/rfx-devcheck').type,'symlink');
  symlinkSync('/etc/passwd',join(root,'outside')); assert.throws(()=>inventoryTree(root),/symlink/);
});

test('builder rejects absent input before compiling or creating a bundle', () => {
  const root=temporary(); let invoked=false;
  assert.throws(()=>buildNativeRfx({repoRoot:root,dgvRoot:join(root,'missing'),compile:()=>{invoked=true;}}),/missing|ENOENT/);
  assert.equal(invoked,false); assert.equal(existsSync(join(root,'build')),false);
});
