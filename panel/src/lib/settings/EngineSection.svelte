<script>
  import { onDestroy } from 'svelte';
  import { RefreshCw } from 'lucide-svelte';
  import { j, jpost, jput } from '../api';
  import { tooltip } from '../../kit/tooltip.js';
  import EngineMark from '../engines/EngineMark.svelte';
  import { Segmented } from '../../kit/index.js';
  import { PARAMS, EMBED_PARAMS, profileLabel as labelOf } from './vocabulary';

  // ── Engine — which Brain Core answers, and how to change it ──
  //
  // One card per ENGINE (llama.cpp, vLLM); under it the engine's PROFILES —
  // each a whole runtime: model, quantisation, KV dtype, vision, window. Two
  // vLLM profiles are not two engines, so they do not get two cards.
  //
  // The panel never runs systemctl. Restart writes a request the park watchdog
  // acts on: park every other Core, start the chosen one, restart Cerveau. A
  // harness able to stop the engine it is talking to has a failure mode where
  // the machine ends up with no model — so systemd keeps both ends, and the
  // watchdog reports progress through a status file this panel polls.
  let cores = $state({ cores: [], active: '', selected: '', endpoint: '', switch: null });
  let engineSel = $state('');
  let profileSel = $state('');      // intent — a profile chosen, not yet applied
  let applying = $state(null);      // the watchdog's status while a switch runs
  let manual = $state(null);        // a Core with no unit: show the commands instead
  let copied = $state('');
  let applyError = $state('');
  let pollTimer = null;
  // Leaving the section stops the polling, not the switch: the watchdog owns
  // that, and loadCores() picks the status up again on the way back in.
  onDestroy(() => { if (pollTimer) clearInterval(pollTimer); });

  const engines = $derived.by(() => {
    const m = new Map();
    for (const c of cores.cores || []) {
      if (!m.has(c.engine)) m.set(c.engine, []);
      m.get(c.engine).push(c);
    }
    return [...m.entries()].map(([engine, profiles]) => ({ engine, profiles }));
  });
  const activeCore = $derived((cores.cores || []).find((c) => c.id === cores.active));
  const profiles = $derived(engines.find((e) => e.engine === engineSel)?.profiles ?? []);
  const profCore = $derived((cores.cores || []).find((c) => c.id === profileSel));
  const switching = $derived(!!applying && applying.phase !== 'done' && applying.phase !== 'failed');

  const profileLabel = (c) => labelOf(c?.name, c?.engine);

  async function loadCores() {
    try {
      const r = await j('/api/cores');
      if (!r) return;
      cores = r;
      const list = r.cores || [];
      if (!profileSel) profileSel = r.selected || r.active || list[0]?.id || '';
      if (!engineSel) engineSel = list.find((c) => c.id === profileSel)?.engine || list[0]?.engine || '';
      // a switch left running when the page was closed: resume watching it
      if (r.switch && r.switch.phase !== 'done' && r.switch.phase !== 'failed' && !pollTimer) {
        applying = r.switch; watchSwitch();
      }
    } catch { /* core older than /api/cores */ }
  }
  loadCores();

  function pickEngine(e) {
    engineSel = e.engine;
    profileSel = (e.profiles.find((c) => c.id === cores.active) || e.profiles[0]).id;
  }

  // Restart: same Core → re-applies saved parameter changes; another Core →
  // parks the current one, starts this one. Both restart Cerveau.
  async function apply() {
    manual = null; applyError = '';
    let res;
    // a refusal (the core says why) must reach the user, not the console
    try { res = await jpost('/api/cores/apply', { id: profileSel }); }
    catch (e) { applyError = e instanceof Error ? e.message : String(e); return; }
    if (!res) return;
    if (res.manual) { manual = res.core; await loadCores(); return; }
    applying = res.status || { core: profileSel, phase: 'queued' };
    watchSwitch();
  }
  function watchSwitch() {
    if (pollTimer) clearInterval(pollTimer);
    const started = Date.now();
    pollTimer = setInterval(async () => {
      // Cerveau itself restarts near the end: failed polls are expected, keep going.
      const r = await j('/api/cores').catch(() => null);
      if (r) { cores = r; if (r.switch) applying = r.switch; }
      const done = applying && (applying.phase === 'done' || applying.phase === 'failed');
      if (done || Date.now() - started > 20 * 60 * 1000) { clearInterval(pollTimer); pollTimer = null; }
    }, 2000);
  }
  const PHASES = ['queued', 'stopping', 'starting', 'restarting', 'done'];

  // Model, context and rationale for the chosen profile — on demand, not
  // competing with the choice itself.
  function coreTip(c) {
    const bits = [];
    if (c.model) bits.push(c.model);
    if (c.ctx) bits.push(`${Math.round(c.ctx / 1024)}K context`);
    return [bits.join('  ·  '), c.notes].filter(Boolean).join('\n');
  }

  // ── Profile parameters — what the chosen profile's unit runs with ──
  //
  // Read from cores.json (install.sh copied the unit's Environment= lines).
  // Overrides go to a file the unit reads at its next start, so "save" is
  // half the job and Restart above is the other half.
  // what each parameter means is said once, in vocabulary.ts — the Rig page
  // reads the same words
  let params = $state(null);        // { id, defaults, overrides, effective, file, embed:{defaults,overrides,effective} }
  let draft = $state({});
  let edraft = $state({});          // embedder settings being edited
  let paramsBusy = $state(false);
  let paramsNote = $state('');
  const dirty = $derived(!!params && (
    Object.keys(draft).some((k) => draft[k] !== params.effective[k]) ||
    Object.keys(edraft).some((k) => edraft[k] !== (params.embed?.effective || {})[k])));
  const hasOverrides = $derived(!!params && (
    Object.keys(params.overrides || {}).length > 0 || Object.keys(params.embed?.overrides || {}).length > 0));
  const embedKeys = $derived(params?.embed?.defaults ? Object.keys(params.embed.defaults) : []);

  async function loadParams(id) {
    params = null; paramsNote = '';
    if (!id) return;
    try {
      const p = await j(`/api/cores/${id}/params`);
      if (p && p.defaults && Object.keys(p.defaults).length) {
        params = p; draft = { ...p.effective }; edraft = { ...(p.embed?.effective || {}) };
      }
    } catch { /* core older than /params */ }
  }
  $effect(() => { loadParams(profileSel); });

  async function saveParams() {
    paramsBusy = true;
    try {
      const p = await jput(`/api/cores/${params.id}/params`, { overrides: draft, embed_overrides: edraft });
      if (p?.defaults) {
        params = p; draft = { ...p.effective }; edraft = { ...(p.embed?.effective || {}) };
        paramsNote = 'saved — Restart to apply';
      }
      else paramsNote = p?.error || 'not saved';
    } finally { paramsBusy = false; }
  }
  async function resetParams() {
    draft = { ...params.defaults }; edraft = { ...(params.embed?.defaults || {}) };
    await saveParams();
    paramsNote = 'back to the profile — Restart to apply';
  }

  async function copyStart(cmd) {
    try { await navigator.clipboard.writeText(cmd); copied = cmd; setTimeout(() => (copied = ''), 1600); }
    catch { /* clipboard blocked — the command is on screen to type */ }
  }
</script>

<section>
  <h2 class="sect-title">Engine</h2>
  <p class="sect-note">
    A <b>Brain Core</b> is a whole inference runtime — engine, quantisation,
    KV format — behind one endpoint. Cerveau only picks a URL.
  </p>

  {#if engines.length}
    <div class="cores">
      {#each engines as e (e.engine)}
        {@const live = e.profiles.some((c) => c.id === cores.active)}
        <button class="core" class:on={e.engine === engineSel} onclick={() => pickEngine(e)}
          use:tooltip={live ? `${activeCore?.name} is answering` : `${e.profiles.length} profile${e.profiles.length === 1 ? '' : 's'} — none active`}>
          <EngineMark engine={e.engine} size={64} />
          <span class="core-name">{e.engine}</span>
          <span class="core-sub" class:live>
            {live ? `${profileLabel(activeCore)} · active` : `${e.profiles.length} profile${e.profiles.length === 1 ? '' : 's'}`}
          </span>
        </button>
      {/each}
    </div>

    <div class="prof">
      <div class="samp-head">
        <span class="samp-label">Profile</span>
        <span class="samp-hint">
          {#if profileSel === cores.active}answering now{:else}not active — {activeCore?.name || 'nothing'} is answering{/if}
        </span>
      </div>
      {#if profiles.length}
        <div use:tooltip={profCore ? coreTip(profCore) : ''}>
          <Segmented
            options={profiles.map((c) => ({ value: c.id, label: profileLabel(c) }))}
            value={profileSel}
            onchange={(id) => { profileSel = id; manual = null; }} />
        </div>
      {/if}

      {#if params}
        {#if embedKeys.length}
          <div class="pgroup"><span class="samp-label">Core</span><span class="samp-hint">what the engine unit runs with</span></div>
        {/if}
        <div class="prows">
          {#each Object.keys(params.defaults) as k (k)}
            <label class="prow" use:tooltip={PARAMS[k]?.tip || ''}>
              <span class="pkey mono">{k}</span>
              <input class="pval mono" spellcheck="false" value={draft[k] ?? ''}
                oninput={(e) => { draft[k] = e.target.value; }} />
              <span class="pdef mono" class:changed={draft[k] !== params.defaults[k]}>
                {draft[k] !== params.defaults[k] ? `profile: ${params.defaults[k]}` : 'profile'}
              </span>
            </label>
          {/each}
        </div>
        {#if embedKeys.length}
          <div class="pgroup">
            <span class="samp-label">Embedder</span>
            <span class="samp-hint">where recall's embedder runs under this profile · restarted on Restart</span>
          </div>
          <div class="prows">
            {#each embedKeys as k (k)}
              <label class="prow" use:tooltip={EMBED_PARAMS[k]?.tip || ''}>
                <span class="pkey mono">{k}</span>
                <input class="pval mono" spellcheck="false" value={edraft[k] ?? ''}
                  oninput={(e) => { edraft[k] = e.target.value; }} />
                <span class="pdef mono" class:changed={edraft[k] !== params.embed.defaults[k]}>
                  {edraft[k] !== params.embed.defaults[k] ? `profile: ${params.embed.defaults[k]}` : 'profile'}
                </span>
              </label>
            {/each}
          </div>
        {/if}
        <div class="pactions">
          <button class="copy" disabled={paramsBusy || !dirty} onclick={saveParams}>save</button>
          <button class="copy" disabled={paramsBusy || !hasOverrides} onclick={resetParams}>reset to profile</button>
          {#if paramsNote}<span class="samp-hint">{paramsNote}</span>{/if}
        </div>
      {/if}

      <div class="apply">
        <button class="restart-btn" disabled={switching || !profCore} onclick={apply}>
          <RefreshCw size={14} />
          {profileSel === cores.active ? 'Restart Core' : `Switch to ${profileLabel(profCore)}`}
        </button>
        <span class="samp-hint">
          {profileSel === cores.active
            ? 'reloads this Core with the saved parameters, then restarts Cerveau'
            : 'parks the other Cores, starts this one, restarts Cerveau'}
        </span>
      </div>

      {#if applyError}<p class="alert" role="alert">{applyError}</p>{/if}

      {#if applying}
        <div class="restart" class:failed={applying.phase === 'failed'}>
          <div class="phases">
            {#each PHASES as ph, idx}
              {@const cur = PHASES.indexOf(applying.phase)}
              <span class="phase" class:cur={ph === applying.phase} class:past={cur > idx}>{ph}</span>
            {/each}
            {#if applying.phase === 'failed'}<span class="phase cur">failed</span>{/if}
          </div>
          {#if applying.detail}<p class="restart-why">{applying.detail}</p>{/if}
        </div>
      {/if}

      {#if manual}
        <div class="restart">
          <div class="restart-head">
            <RefreshCw size={14} />
            <span><b>{manual.name}</b> has no systemd unit in cores.json — run it by hand</span>
          </div>
          {#if manual.start}
            <div class="cmd">
              <code class="mono">{manual.start}</code>
              <button class="copy" onclick={() => copyStart(manual.start)}>{copied === manual.start ? 'copied' : 'copy'}</button>
            </div>
          {/if}
          <div class="cmd">
            <code class="mono">systemctl --user restart cerveau.service</code>
            <button class="copy" onclick={() => copyStart('systemctl --user restart cerveau.service')}>
              {copied === 'systemctl --user restart cerveau.service' ? 'copied' : 'copy'}
            </button>
          </div>
        </div>
      {/if}
    </div>
  {:else}
    <p class="empty mono">
      No cores.json — running on {cores.endpoint || 'the configured endpoint'}.
    </p>
  {/if}
</section>

<style>
  /* Cores side by side: the choice is a comparison, and stacking them
     vertically made it read as a list to scroll rather than options to
     weigh. Profiles add more cards; they wrap rather than shrink to nothing.
     Everything explanatory lives in the tooltip. */
  .cores { display: flex; flex-wrap: wrap; gap: 10px; }

  .core {
    flex: 1 1 150px; min-width: 0;
    display: flex; flex-direction: column; align-items: center; gap: 14px;
    cursor: pointer; text-align: center;
    background: var(--s1); border: 1px solid var(--line2); border-radius: 12px;
    padding: 26px 16px; color: inherit; font: inherit;
    transition: border-color .14s, background .14s;
  }
  .core:hover:not(:disabled) { border-color: var(--accent); }
  .core:disabled { opacity: .55; cursor: progress; }
  /* The outline already says which Core is live — an ACTIVE badge underneath
     was the same fact twice, and it made the two cards different heights. */
  .core.on { border-color: var(--accent); background: var(--s2); }

  .core-name { font-size: var(--fs-title); font-weight: 640; color: var(--text); }
  .core-sub { font-size: var(--fs-small); color: var(--dim); margin-top: -8px; }
  .core-sub.live { color: var(--text); }

  /* ── apply / switch ── */
  .apply { display: flex; align-items: center; gap: 12px; margin-top: 14px; flex-wrap: wrap; }
  .restart-btn {
    display: inline-flex; align-items: center; gap: 7px; cursor: pointer;
    font: inherit; font-size: var(--fs-body); font-weight: 600; color: var(--text);
    background: var(--s1); border: 1px solid var(--accent); border-radius: 8px;
    padding: 8px 14px;
  }
  .restart-btn:hover:not(:disabled) { background: color-mix(in srgb, var(--accent) 12%, transparent); }
  .restart-btn:disabled { opacity: .5; cursor: progress; }
  .phases { display: flex; gap: 14px; flex-wrap: wrap; }
  .phase { font-size: var(--fs-micro); letter-spacing: .08em; text-transform: uppercase; color: var(--dim); }
  .phase.past { color: var(--text); }
  .phase.cur { color: var(--accent); }
  .restart.failed { border-color: var(--err); background: none; }

  /* ── profile parameters ── */
  .prof { margin-top: 18px; }
  .prows {
    display: grid; grid-template-columns: max-content minmax(0, 1fr) max-content;
    gap: 6px 12px; align-items: center;
  }
  .prow { display: contents; cursor: default; }
  .pkey { font-size: var(--fs-small); color: var(--dim); }
  .pval {
    min-width: 0; font-size: var(--fs-small); color: var(--text);
    background: var(--bg); border: 1px solid var(--line2); border-radius: 6px;
    padding: 5px 8px;
  }
  .pval:focus { outline: none; border-color: var(--accent); }
  /* "profile" when the value is the installed default; the default itself
     once it has been changed, so the user can see what they left. */
  .pdef { font-size: var(--fs-micro); letter-spacing: .04em; color: var(--dim); white-space: nowrap; }
  .pdef.changed { color: var(--accent); }
  .pactions { display: flex; align-items: center; gap: 8px; margin-top: 10px; }
  .pgroup { display: flex; align-items: baseline; gap: 10px; margin: 14px 0 8px; }

  /* ── restart prompt ── */
  .restart {
    margin-top: 14px; padding: 13px 15px;
    border: 1px solid var(--accent); border-radius: 9px;
    background: color-mix(in srgb, var(--accent) 7%, transparent);
  }
  .restart-head {
    display: flex; align-items: center; gap: 8px;
    font-size: var(--fs-body); font-weight: 600; color: var(--text);
  }
  .restart-why {
    margin: 7px 0 11px; font-size: var(--fs-small); line-height: 1.55; color: var(--dim);
    max-width: 60ch;
  }
  .cmd {
    display: flex; align-items: center; gap: 8px; margin-top: 7px;
    background: var(--bg); border: 1px solid var(--line2); border-radius: 6px;
    padding: 7px 9px;
  }
  .cmd code {
    flex: 1; min-width: 0; font-size: var(--fs-small); color: var(--text);
    overflow-x: auto; white-space: nowrap;
  }

  /* On a phone the three-column parameter grid runs off the side: drop the
     "profile" column under the value instead. */
  @media (max-width: 640px) {
    .prows { grid-template-columns: max-content minmax(0, 1fr); }
    .pdef { grid-column: 2; margin-top: -3px; }
  }
</style>
