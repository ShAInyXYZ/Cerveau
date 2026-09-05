// Opt-in real-Core acceptance through the actual Planner iframe and production API.
// Start TestLABRIGRealCoreUIPreview first. No API/model mocks or service changes.
import assert from 'node:assert/strict';
import { readFileSync, writeFileSync } from 'node:fs';
import { basename, join, resolve } from 'node:path';
import { claimEvidence, fetchWithin } from './qa-labrig-safety.mjs';
const { chromium } = await import(process.env.CERVEAU_PLAYWRIGHT_MODULE || 'playwright');
const base = 'http://127.0.0.1:17707';
const get = async path => {
  const response = await fetchWithin(base + path);
  if (!response.ok) throw Error(`${path}: HTTP ${response.status}`);
  return response.json();
};
const info = await get('/__labrig/info');
assert.equal(info.fixture, 'LABRIG real W8A16 acceptance');
assert.equal(resolve(info.root), resolve(process.env.CERVEAU_ACCEPTANCE_ROOT || '/missing-acceptance-root'));
const destination = join(info.root, 'ui');
const build = await get('/api/build');
assert.equal(build.planner.origin, 'builtin');
assert.equal(build.planner.precedence, 'builtin-wins');
assert.equal(build.planner.loaded, true);
assert.ok(build.planner.ignored_installed.length >= 1, 'isolated obsolete-copy fixture must be disclosed');
const results = { fixture: info.fixture, source: build, started: new Date().toISOString(), cases: {} };
claimEvidence(join(destination, 'browser-evidence.json'), results);
const browser = await chromium.launch({ headless: true, executablePath: process.env.CERVEAU_CHROMIUM || undefined });
const errors = [];
const save = () => writeFileSync(join(destination, 'browser-evidence.json'), JSON.stringify(results, null, 2) + '\n', { mode: 0o600 });
async function openCase(context, fixture) {
  const page = await context.newPage();
  page.on('pageerror', error => errors.push(error.message));
  await page.goto(base + '/?rfx=planner');
  // Build identity is checked via /api/build above. Core-health latency must
  // not prevent reaching the Planner command that this acceptance exercises.
  await page.getByRole('button', { name: 'planner panel', exact: true }).waitFor();
  const session = page.getByText(fixture.name, { exact: true });
  if (!await session.isVisible()) {
    await page.locator('.pfolder').filter({ hasText: basename(fixture.workspace) }).click();
  }
  await session.click();
  await page.getByText('Built into Cerveau. Takes precedence over installed copies.', { exact: true }).waitFor();
  await page.getByText('1 installed copy ignored; files unchanged', { exact: true }).waitFor();
  const frame = page.frameLocator('iframe[title="planner panel"]');
  await frame.locator('#all').waitFor();
  // A just-selected session must reach the iframe before a button is pressed.
  await frame.locator('.title').filter({ hasText: fixture.name }).waitFor();
  return { page, frame };
}
try {
  for (const key of ['whole', 'selected']) {
    const fixture = info.cases[key];
    const result = results.cases[key] = { verified: false, session_id: fixture.session_id, plan_event_id: fixture.plan_event_id, started: new Date().toISOString() };
    let context;
    try {
      context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
      const { page, frame } = await openCase(context, fixture);
      const sent = [];
      page.on('request', request => {
        if (request.method() === 'POST' && request.url().endsWith(`/api/sessions/${fixture.session_id}/commands`)) sent.push(request.postDataJSON());
      });
      if (key === 'selected') {
        await frame.locator('label[for="sel0"]').click();
        await frame.locator('label[for="sel1"]').click();
      }
      const responsePromise = page.waitForResponse(response => response.request().method() === 'POST' && response.url().endsWith(`/api/sessions/${fixture.session_id}/commands`));
      await frame.locator(key === 'whole' ? '#all' : '#sel').click();
      const response = await responsePromise;
      assert.equal(response.status(), 202, await response.text());
      const accepted = await response.json();
      assert.equal(sent.length, 1);
      assert.equal(sent[0].kind, key === 'whole' ? 'continue' : 'selected');
      assert.equal(sent[0].plan_event_id, fixture.plan_event_id);
      if (key === 'selected') assert.deepEqual(sent[0].steps, [0, 1]);
      result.command = sent[0]; result.run_id = accepted.run.id;
      // Closing the entire observer cannot own/cancel the accepted execution.
      await context.close(); context = undefined;
      result.observer_closed_after_acceptance = true;
      let snapshot;
      const deadline = Date.now() + 240_000;
      while (Date.now() < deadline) {
        snapshot = await get(`/api/sessions/${fixture.session_id}/state`);
        if (snapshot.run?.id === accepted.run.id && !snapshot.running) break;
        await new Promise(resolve => setTimeout(resolve, 500));
      }
      writeFileSync(join(destination, `${key}-snapshot.json`), JSON.stringify(snapshot, null, 2) + '\n', { mode: 0o600 });
      assert.equal(snapshot.run.id, accepted.run.id);
      assert.equal(snapshot.running, false, 'deadline reached before termination');
      assert.equal(snapshot.run.status, 'completed', snapshot.run.reason);
      assert.equal(snapshot.plan_state.plan_event_id, fixture.plan_event_id);
      assert.deepEqual(snapshot.plan_state.steps.map(step => step.status), key === 'whole' ? ['passed', 'passed'] : ['passed', 'passed', 'pending']);
      result.artifacts = [];
      for (const [index, artifact] of fixture.artifacts.entries()) {
        const file = resolve(fixture.workspace, artifact.path);
        assert.ok(file.startsWith(resolve(fixture.workspace) + '/'));
        if (key === 'selected' && index === 2) {
          assert.equal(snapshot.plan_state.steps[2].attempts, 0);
          assert.throws(() => readFileSync(file), { code: 'ENOENT' });
          result.artifacts.push({ path: artifact.path, exists: false });
        } else {
          const content = readFileSync(file, 'utf8');
          assert.equal(content, artifact.content);
          result.artifacts.push({ path: artifact.path, content });
        }
      }
      result.statuses = snapshot.plan_state.steps.map(step => step.status);
      result.model_calls = snapshot.run.calls;
      result.tool_calls = snapshot.events.filter(event => event.type === 'tool.call').length;
      result.tool_results = snapshot.events.filter(event => event.type === 'tool.result').length;
      assert.equal(result.tool_calls, result.tool_results);
      context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
      const reopened = await openCase(context, fixture);
      await reopened.frame.locator('.count').filter({ hasText: key === 'whole' ? '2/2' : '2/3' }).waitFor();
      await reopened.page.screenshot({ path: join(destination, `${key}-desktop.png`), fullPage: true });
      assert.equal(await reopened.page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      // The existing desktop-only Planner dock stays hidden on mobile; native
      // plan/state remains inspectable. This is not mobile Planner-button proof.
      await reopened.page.setViewportSize({ width: 390, height: 844 });
      await reopened.page.screenshot({ path: join(destination, `${key}-mobile.png`), fullPage: true });
      assert.equal(await reopened.page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      result.verified = true;
      console.log(`${key}: real Planner → one command → detached real-Core execution → ${result.statuses.join('/')} PASS`);
    } catch (error) {
      result.error = String(error);
      console.error(`${key}: UNVERIFIED: ${error}`);
      // Cancel only this fixture's accepted run if it is still active; never
      // leave a failed browser assertion running unattended in the background.
      const state = await get(`/api/sessions/${fixture.session_id}/state`).catch(() => null);
      if (state?.running && state.run?.id === result.run_id) {
        await fetchWithin(base + `/api/sessions/${fixture.session_id}/kill`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ run_id: state.run.id, control_id: crypto.randomUUID(), control_version: state.run.control_version ?? 0 }) });
      }
    } finally {
      await context?.close();
      result.finished = new Date().toISOString();
      result.seconds = (Date.parse(result.finished) - Date.parse(result.started)) / 1000;
      save();
    }
  }
  results.page_errors = errors;
  save();
  if (errors.length || Object.values(results.cases).some(result => !result.verified)) process.exitCode = 1;
} finally {
  await browser.close();
  await fetchWithin(base + '/__labrig/stop', { method: 'POST' }).catch(() => {});
}
