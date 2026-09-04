<script>
  import { Volume2, VolumeX, Play, Zap, RefreshCw } from 'lucide-svelte';
  import { play, isMuted, setMuted, getVolume, setVolume, getSoundVolume, setSoundVolume, available } from './sound.js';
  import { j, jpost, jput } from './api';
  import { tooltip } from '../kit/tooltip.js';
  import EngineMark from './engines/EngineMark.svelte';
  import { Segmented } from '../kit/index.js';

  // Settings — deliberately simple. First (and so far only) section: sounds.
  const TYPES = [
    { name: 'done',    label: 'Done',         desc: 'a turn completes successfully' },
    { name: 'error',   label: 'Error',        desc: 'an error card appears' },
    { name: 'ask',     label: 'Ask',          desc: 'the agent asks you a question' },
    { name: 'confirm', label: 'Confirm',      desc: 'a rename or delete succeeds' },
    { name: 'notify',  label: 'Notification', desc: 'a new session is created' }
  ];
  const have = new Set(available());

  let muted = $state(isMuted());
  let master = $state(getVolume());
  let vols = $state(Object.fromEntries(TYPES.map((t) => [t.name, getSoundVolume(t.name)])));

  function toggleMute() { muted = !muted; setMuted(muted); }
  function onMaster(v) { master = v; setVolume(v); }
  function onVol(name, v) { vols[name] = v; setSoundVolume(name, v); }
  // test always audible (force), so you can tune while muted
  function test(name) { play(name, { force: true }); }

  // ── Engine section — which Brain Core answers, and how to change it ──
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
  let pollTimer = null;

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

  // "vLLM · BF16 · TP=4" under the vLLM card is just "BF16 · TP=4".
  function profileLabel(c) {
    if (!c) return '';
    const pre = (c.engine || '').replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    return c.name.replace(new RegExp(`^${pre}\\s*·\\s*`, 'i'), '') || c.name;
  }

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
    manual = null;
    const res = await jpost('/api/cores/apply', { id: profileSel });
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
  const PARAM_TIP = {
    KV: 'KV cache dtype. bf16 = exact; fp8 = half the KV bytes, twice the concurrent context, KLD ≈ 0.',
    VISION: '1 loads the vision tower so the Core reads screenshots; 0 drops it (--language-model-only).',
    MAX_LEN: 'Context window in tokens. Must fit the KV pool or vLLM refuses to start.',
    DRAFT_TOKENS: 'MTP speculative drafts per step. 0 = off. Lossless either way.',
    GPU_UTIL: 'Fraction of each GPU vLLM may claim. Rank 0 also carries the API server — leave headroom.',
    TP: 'Tensor-parallel ranks. Head counts must divide by it.',
    CUDA_VISIBLE_DEVICES: 'GPU indices for the tensor-parallel group.',
    MAX_PIXELS: 'Image resolution cap before the vision encoder (1638400 = 1280×1280, ~2k tokens per image).',
    IMAGES_PER_PROMPT: 'Upper bound on images in one request; sizes the encoder cache.',
    MAX_SEQS: 'Concurrent requests.',
    BATCHED_TOKENS: 'Prefill chunk. Larger = faster TTFT on long prompts, more activation memory.'
  };
  const EMBED_TIP = {
    EMBED_DEVICE: 'cpu or cuda. On the lab rig the embedder takes the 3060 so recall runs at GPU speed and leaves the 3090s to the Core.',
    CUDA_VISIBLE_DEVICES: 'Which GPU the embedder may see (index from nvidia-smi -L). Keep it off the Core\'s cards.',
    EMBED_THREADS: 'CPU threads when EMBED_DEVICE=cpu; also the tokeniser threads on GPU.'
  };
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

  // ── sampling: the SESSION DEFAULT. A single turn can override it from the
  // chat bar; this is the value everything else uses. No restart — temperature
  // is a per-request field.
  let sampling = $state({ active: 'strict', presets: [] });
  const SAMPLING_TIP = {
    default:  'No temperature or top_p sent — the model uses its own generation config (Qwen3.8-27B: temperature 1.0, top_p 0.95, top_k 20).',
    strict:   'temperature 0.2, no top_p. The measured default — every good benchmark run this project has produced used it. Best for code.',
    neutral:  'temperature 0.55, top_p 0.85. Looser, for drafting and exploration. Not benchmarked here.',
    creative: 'temperature 0.7, top_p 0.9. Widest spread, for when there is no single correct answer. Not benchmarked here.'
  };

  async function loadSampling() {
    try { sampling = await j('/api/sampling'); } catch { /* older core */ }
  }
  loadSampling();

  async function setSampling(name) {
    if (name === sampling.active) return;
    sampling = { ...sampling, active: name };          // optimistic: it is instant
    try { await jpost('/api/sampling', { name }); } catch { await loadSampling(); }
  }

  // ── thinking: which turns reason before answering, and how hard ──
  // A per-call template argument, like sampling: changes apply to the next
  // model call, no restart. Chat stays direct; a build gets to think.
  let thinking = $state({ mode: 'plan', effort: 'low', modes: [], efforts: [] });
  const THINK_MODE_TIP = {
    off: 'Never think. Fastest; the model reasons in its answer text if at all.',
    plan: 'Think only while dividing a build into steps. Measured: the planning call reasons ~500 tokens and plans well; code-writing calls reason ~10k and overflow. The default.',
    autopilot: 'Think on every autopilot call, steps included. Slower; each code step reasons for minutes.',
    always: 'Think on every turn, chat included. Slowest.'
  };
  const THINK_EFFORT_TIP = {
    low: 'Brief thinking, straight to the conclusion. The default.',
    medium: 'Longer reasoning. In testing one planning step thought for 13k tokens.',
    xhigh: 'The model\'s default: validate assumptions, weigh alternatives. Thousands of tokens per call.'
  };
  async function loadThinking() {
    try { const t = await j('/api/thinking'); if (t?.modes) thinking = t; } catch { /* older core */ }
  }
  loadThinking();
  async function setThinking(patch) {
    const next = { ...thinking, ...patch };
    thinking = next;                                    // optimistic: it is instant
    try { await jpost('/api/thinking', { mode: next.mode, effort: next.effort }); } catch { await loadThinking(); }
  }

  async function copyStart(cmd) {
    try { await navigator.clipboard.writeText(cmd); copied = cmd; setTimeout(() => (copied = ''), 1600); }
    catch { /* clipboard blocked — the command is on screen to type */ }
  }

  // ── RFX section — reflex/talent management (the base UI for RFX) ──
  let rfx = $state({ packs: [], reflexes: [], notices: [], errors: [] });
  let rfxBusy = $state('');

  async function loadRfx() {
    try { rfx = await j('/api/rfx'); } catch { /* core older than RFX */ }
  }
  loadRfx();

  async function toggleRfx(name, enabled) {
    rfxBusy = name;
    try {
      await jpost('/api/rfx/toggle', { name, enabled });
      await loadRfx();
    } finally { rfxBusy = ''; }
  }

  // group reflexes by pack (standalone first)
  const rfxGroups = $derived.by(() => {
    const g = new Map();
    for (const r of rfx.reflexes ?? []) {
      const key = r.pack || '';
      if (!g.has(key)) g.set(key, []);
      g.get(key).push(r);
    }
    const keys = [...g.keys()].sort((a, b) => (a === '' ? -1 : b === '' ? 1 : a.localeCompare(b)));
    const descOf = Object.fromEntries((rfx.packs ?? []).map((p) => [p.name, p]));
    return keys.map((k) => ({ key: k, pack: descOf[k], items: g.get(k) }));
  });
</script>

<main class="settings">
  <div class="wrap">
    <div class="shead"><span class="label">SETTINGS</span></div>

    <section>
      <div class="sect-title">Engine</div>
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
                <label class="prow" use:tooltip={PARAM_TIP[k] || ''}>
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
                  <label class="prow" use:tooltip={EMBED_TIP[k] || ''}>
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

      {#if sampling.presets?.length}
        <div class="samp">
          <div class="samp-head">
            <span class="samp-label">Sampling</span>
            <span class="samp-hint">applies to every turn · changes instantly</span>
          </div>
          <!-- kit/Segmented, not a local copy: it carries role="tablist" and
               aria-selected, which a hand-rolled row of buttons does not. -->
          <div use:tooltip={SAMPLING_TIP[sampling.active] || ''}>
            <Segmented
              options={sampling.presets.map((p) => ({ value: p, label: p }))}
              value={sampling.active}
              onchange={setSampling} />
          </div>
        </div>
      {/if}
      {#if thinking.modes?.length}
        <div class="samp">
          <div class="samp-head">
            <span class="samp-label">Thinking</span>
            <span class="samp-hint">reason before answering · applies to the next call · costs time, not context</span>
          </div>
          <div class="think-rows">
            <div use:tooltip={THINK_MODE_TIP[thinking.mode] || ''}>
              <Segmented
                options={thinking.modes.map((m) => ({ value: m, label: m }))}
                value={thinking.mode}
                onchange={(mode) => setThinking({ mode })} />
            </div>
            <div class:dimmed={thinking.mode === 'off'} use:tooltip={THINK_EFFORT_TIP[thinking.effort] || ''}>
              <Segmented
                options={thinking.efforts.map((e) => ({ value: e, label: e }))}
                value={thinking.effort}
                onchange={(effort) => setThinking({ effort })} />
            </div>
          </div>
        </div>
      {/if}
    </section>

    <section>
      <div class="sect-title">Sound</div>

      <!-- master row -->
      <div class="master">
        <button class="mute" class:on={muted} onclick={toggleMute} aria-label={muted ? 'unmute' : 'mute'}>
          {#if muted}<VolumeX size={16} />{:else}<Volume2 size={16} />{/if}
        </button>
        <span class="mlabel">{muted ? 'Muted' : 'Master volume'}</span>
        <input type="range" class="slider" min="0" max="1" step="0.05" value={master}
          disabled={muted} oninput={(e) => onMaster(+e.target.value)} />
        <span class="pct mono">{Math.round(master * 100)}%</span>
      </div>

      <!-- per-sound rows -->
      <div class="rows" class:dim={muted}>
        {#each TYPES as t (t.name)}
          <div class="row">
            <button class="test" disabled={!have.has(t.name)} onclick={() => test(t.name)}
              aria-label="test {t.label}">
              <Play size={12} strokeWidth={2.4} />
            </button>
            <div class="rtext">
              <span class="rname">{t.label}</span>
              <span class="rdesc">{t.desc}</span>
            </div>
            {#if have.has(t.name)}
              <input type="range" class="slider small" min="0" max="1" step="0.05" value={vols[t.name]}
                oninput={(e) => onVol(t.name, +e.target.value)} />
              <span class="pct mono">{Math.round(vols[t.name] * 100)}%</span>
            {:else}
              <span class="missing mono">no file</span>
            {/if}
          </div>
        {/each}
      </div>
    </section>

    <section>
      <div class="sect-title rfx-head">
        <span>RFX Talents</span>
        <button class="reload" onclick={loadRfx} aria-label="reload reflexes"><RefreshCw size={13} /></button>
      </div>

      {#if (rfx.reflexes ?? []).length === 0}
        <div class="rfx-empty">
          <Zap size={15} />
          <span>No reflexes installed. Drop <code>.rfx.yaml</code> files (or a pack folder) into
          <code>~/.crv/rfx/</code> — or <code>crvcli rfx install</code> one.</span>
        </div>
      {:else}
        {#each rfxGroups as g (g.key)}
          <div class="pack">
            <div class="pack-name">
              {g.key || 'standalone'}
              {#if g.pack}<span class="pack-desc">v{g.pack.version} · {g.pack.description}</span>{/if}
            </div>
            <div class="rows">
              {#each g.items as r (r.name)}
                <div class="row" class:off={!r.enabled}>
                  <button
                    class="switch"
                    class:on={r.enabled}
                    disabled={rfxBusy === r.name}
                    onclick={() => toggleRfx(r.name, !r.enabled)}
                    aria-label={r.enabled ? 'disable ' + r.name : 'enable ' + r.name}
                    role="switch" aria-checked={r.enabled}>
                    <span class="knob"></span>
                  </button>
                  <div class="rtext">
                    <span class="rname">{r.name}</span>
                    <span class="rdesc">{r.description}</span>
                  </div>
                  <span class="chip" class:chip-dangerous={r.risk === 'dangerous'} class:chip-sensitive={r.risk === 'sensitive'}>{r.risk}</span>
                  <span class="chip">{r.kind}</span>
                  <span class="chip modes">{(r.modes ?? []).join(' ')}</span>
                </div>
              {/each}
            </div>
          </div>
        {/each}
      {/if}

      {#if (rfx.notices ?? []).length > 0 || (rfx.errors ?? []).length > 0}
        <div class="rfx-issues">
          {#each rfx.notices ?? [] as n}<div class="issue notice">{n}</div>{/each}
          {#each rfx.errors ?? [] as e}<div class="issue err">REJECTED — {e}</div>{/each}
        </div>
      {/if}
      <div class="rfx-foot">Toggles write <code>.state.json</code> — a disabled reflex leaves the grammar on the next turn. Files are never edited.</div>
    </section>
  </div>
</main>

<style>
  .settings { flex: 1; overflow-y: auto; display: flex; justify-content: center; }
  .wrap { width: 100%; max-width: 640px; padding: 26px 26px 60px; }
  .shead { margin-bottom: 22px; }

  .sect-title { font-size: var(--fs-title); font-weight: 640; color: var(--text); margin-bottom: 14px; }

  /* ── Engine ── */
  .sect-note {
    margin: -6px 0 14px; font-size: var(--fs-body); line-height: 1.55; color: var(--dim);
    max-width: 60ch;
  }

  /* Cores side by side: the choice is a comparison, and stacking them
     vertically made it read as a list to scroll rather than options to
     weigh. Profiles add more cards; they wrap rather than shrink to nothing.
     Everything explanatory lives in the tooltip. */
  .cores { display: flex; flex-wrap: wrap; gap: 10px; }

  .core {
    flex: 1 1 150px; min-width: 0;
    display: flex; flex-direction: column; align-items: center; gap: 14px;
    cursor: pointer; text-align: center;
    background: var(--panel); border: 1px solid var(--line2); border-radius: 12px;
    padding: 26px 16px; color: inherit; font: inherit;
    transition: border-color .14s, background .14s;
  }
  .core:hover:not(:disabled) { border-color: var(--accent); }
  .core:disabled { opacity: .55; cursor: progress; }
  /* The outline already says which Core is live — an ACTIVE badge underneath
     was the same fact twice, and it made the two cards different heights. */
  .core.on { border-color: var(--accent); background: var(--panel-raised, var(--panel)); }

  .core-name { font-size: var(--fs-title); font-weight: 640; color: var(--text); }
  .core-sub { font-size: var(--fs-small); color: var(--dim); margin-top: -8px; }
  .core-sub.live { color: var(--text); }

  /* ── apply / switch ── */
  .apply { display: flex; align-items: center; gap: 12px; margin-top: 14px; flex-wrap: wrap; }
  .restart-btn {
    display: inline-flex; align-items: center; gap: 7px; cursor: pointer;
    font: inherit; font-size: var(--fs-body); font-weight: 600; color: var(--text);
    background: var(--panel); border: 1px solid var(--accent); border-radius: 8px;
    padding: 8px 14px;
  }
  .restart-btn:hover:not(:disabled) { background: color-mix(in srgb, var(--accent) 12%, transparent); }
  .restart-btn:disabled { opacity: .5; cursor: progress; }
  .phases { display: flex; gap: 14px; flex-wrap: wrap; }
  .phase { font-size: var(--fs-micro); letter-spacing: .08em; text-transform: uppercase; color: var(--dim); }
  .phase.past { color: var(--text); }
  .phase.cur { color: var(--accent); }
  .restart.failed { border-color: var(--err, #c0392b); background: none; }

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
  .copy:disabled { opacity: .45; cursor: default; }
  .copy:disabled:hover { color: var(--dim); border-color: var(--line2); }

  .think-rows { display: flex; gap: 10px; flex-wrap: wrap; align-items: center; }
  .think-rows .dimmed { opacity: .45; }

  /* ── sampling ── */
  .samp { margin-top: 18px; }
  .samp-head {
    display: flex; align-items: baseline; gap: 10px; margin-bottom: 9px;
  }
  .samp-label { font-size: var(--fs-body); font-weight: 620; color: var(--text); }
  .samp-hint { font-size: var(--fs-small); color: var(--dim); }


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
  .copy {
    flex-shrink: 0; font-size: var(--fs-micro); letter-spacing: .06em; cursor: pointer;
    background: none; border: 1px solid var(--line2); border-radius: 4px;
    padding: 3px 8px; color: var(--dim);
  }
  .copy:hover { color: var(--accent); border-color: var(--accent); }

  .empty { font-size: var(--fs-small); color: var(--dim); }

  /* master volume bar */
  .master {
    display: flex; align-items: center; gap: 12px;
    padding: 13px 16px; border-radius: 12px; margin-bottom: 12px;
    background: var(--surface-raised); box-shadow: var(--elev-1);
  }
  .mute {
    display: inline-flex; align-items: center; justify-content: center;
    width: 32px; height: 32px; border: none; border-radius: 8px; cursor: pointer;
    background: var(--s3); color: var(--muted);
    transition: color .12s, background .12s;
  }
  .mute:hover { color: var(--text); }
  .mute.on { color: var(--err); background: color-mix(in srgb, var(--err) 12%, transparent); }
  .mlabel { font-size: var(--fs-body); color: var(--muted); min-width: 96px; }

  /* per-sound rows */
  .rows { display: flex; flex-direction: column; gap: 6px; transition: opacity .15s; }
  .rows.dim { opacity: .45; }
  .row {
    display: flex; align-items: center; gap: 12px;
    padding: 10px 14px; border-radius: 10px;
    background: color-mix(in srgb, #fff 2.5%, transparent);
    box-shadow: inset 0 0 0 1px var(--line);
  }
  .test {
    flex-shrink: 0; display: inline-flex; align-items: center; justify-content: center;
    width: 28px; height: 28px; border: none; border-radius: 50%; cursor: pointer;
    background: var(--accent); color: var(--accent-ink);
    transition: filter .1s, transform .05s;
  }
  .test:hover:not(:disabled) { filter: brightness(1.1); }
  .test:active:not(:disabled) { transform: scale(.92); }
  .test:disabled { opacity: .3; cursor: default; }
  .rtext { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 1px; }
  .rname { font-size: var(--fs-body); font-weight: 550; color: var(--text); }
  .rdesc { font-size: var(--fs-small); color: var(--dim); }
  .missing { font-size: var(--fs-micro); color: var(--faint); }

  /* sliders — themed range input */
  .slider {
    -webkit-appearance: none; appearance: none;
    flex: 1; max-width: 220px; height: 4px; border-radius: 2px;
    background: var(--line2); outline: none; cursor: pointer;
  }
  .slider.small { max-width: 150px; }
  .slider::-webkit-slider-thumb {
    -webkit-appearance: none; appearance: none;
    width: 14px; height: 14px; border-radius: 50%;
    background: var(--accent); border: none;
    box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 18%, transparent);
    transition: transform .1s;
  }
  .slider::-webkit-slider-thumb:hover { transform: scale(1.15); }
  .slider::-moz-range-thumb {
    width: 14px; height: 14px; border-radius: 50%;
    background: var(--accent); border: none;
  }
  .slider:disabled { cursor: default; }
  .slider:disabled::-webkit-slider-thumb { background: var(--faint); box-shadow: none; }

  .pct { width: 38px; text-align: right; font-size: var(--fs-small); color: var(--dim); font-variant-numeric: tabular-nums; }

  /* ── RFX section ── */
  section { margin-bottom: 30px; }
  .rfx-head { display: flex; align-items: center; justify-content: space-between; }
  .reload {
    display: inline-flex; align-items: center; justify-content: center;
    width: 24px; height: 24px; border: none; border-radius: 6px; cursor: pointer;
    background: transparent; color: var(--faint); transition: color .12s, background .12s;
  }
  .reload:hover { color: var(--text); background: var(--s3); }
  .rfx-empty {
    display: flex; align-items: flex-start; gap: 10px;
    padding: 14px 16px; border-radius: 10px; font-size: var(--fs-body); color: var(--dim);
    background: color-mix(in srgb, #fff 2.5%, transparent); box-shadow: inset 0 0 0 1px var(--line);
  }
  .rfx-empty code, .rfx-foot code { font-size: var(--fs-small); color: var(--accent); }
  .pack { margin-bottom: 14px; }
  .pack-name {
    font-size: var(--fs-small); font-weight: 650; letter-spacing: .06em; text-transform: uppercase;
    color: var(--muted); margin: 0 2px 7px; display: flex; align-items: baseline; gap: 10px;
  }
  .pack-desc { font-weight: 400; text-transform: none; letter-spacing: 0; color: var(--faint); }
  .row.off { opacity: .45; }
  .chip {
    flex-shrink: 0; font-size: var(--fs-micro); font-weight: 600; letter-spacing: .04em;
    padding: 3px 8px; border-radius: 6px; color: var(--dim);
    background: var(--s3); box-shadow: inset 0 0 0 1px var(--line);
  }
  .chip-sensitive { color: var(--amber, #b87a00); }
  .chip-dangerous { color: var(--err); }
  .chip.modes { font-family: var(--mono, monospace); font-weight: 500; }
  .switch {
    flex-shrink: 0; position: relative; width: 34px; height: 19px; border: none; border-radius: 10px;
    cursor: pointer; background: var(--line2); transition: background .15s;
  }
  .switch:disabled { cursor: wait; }
  .switch .knob {
    position: absolute; top: 2.5px; left: 3px; width: 14px; height: 14px; border-radius: 50%;
    background: var(--paper, #faf9f6); transition: transform .15s;
    box-shadow: 0 1px 2px rgba(0,0,0,.3);
  }
  .switch.on { background: var(--accent); }
  .switch.on .knob { transform: translateX(14px); }
  .rfx-issues { margin-top: 12px; display: flex; flex-direction: column; gap: 6px; }
  .issue {
    font-size: var(--fs-small); padding: 9px 12px; border-radius: 8px;
    background: color-mix(in srgb, #fff 2.5%, transparent); box-shadow: inset 0 0 0 1px var(--line);
  }
  .issue.notice { color: var(--amber, #b87a00); }
  .issue.err { color: var(--err); }
  .rfx-foot { margin-top: 12px; font-size: var(--fs-small); color: var(--faint); }
</style>
