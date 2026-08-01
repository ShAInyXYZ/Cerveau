<script>
  import { Zap, Play, ChevronDown, ChevronUp, Loader, CircleAlert } from 'lucide-svelte';
  import { j, jpost } from './api.js';

  // RfxPackCard — one talent pack's cockpit in the RFX dock (docs/RFX-UI.md).
  // Chrome is shared and fixed; content is the pack's ui: widget list,
  // rendered IN AUTHOR ORDER (the manifest is the layout). Visual identity:
  // hardware panels — mono metrics, inset rings, one accent per card.
  let { pack, members } = $props(); // members: ALL of this pack's reflexes (enabled or not)

  let open = $state(true);
  let status = $state({ rows: [], age: null, error: '' });
  let fields = $state({});
  let lastRun = $state({ label: '', output: '', err: false });
  let running = $state('');          // label of the in-flight button ('' = idle)
  let elapsed = $state(0);
  let armed = $state('');            // dangerous tier: two-click arm/confirm

  const widgets = $derived(pack.ui?.widgets ?? []);
  const enabledSet = $derived(new Set(members.filter((m) => m.enabled !== false).map((m) => m.name)));
  const statusW = $derived(widgets.find((w) => w.type === 'status'));
  const fieldWs = $derived(widgets.filter((w) => w.type === 'field'));
  const maxRisk = $derived(members.some((m) => m.risk === 'dangerous') ? 'dangerous'
    : members.some((m) => m.risk === 'sensitive') ? 'sensitive' : 'safe');

  // Consecutive buttons flow into one row; everything else breaks the row.
  const groups = $derived.by(() => {
    const out = [];
    for (const w of widgets) {
      const last = out[out.length - 1];
      if (w.type === 'button' && last?.kind === 'buttons') last.items.push(w);
      else if (w.type === 'button') out.push({ kind: 'buttons', items: [w] });
      else out.push({ kind: w.type, w });
    }
    return out;
  });

  function parseEvery(e) {
    const m = /^(\d+)(s|m)$/.exec(e ?? '');
    return m ? (+m[1]) * (m[2] === 'm' ? 60000 : 1000) : 30000;
  }

  // status rows: regex with capture group → first group of first match;
  // no group → match count (multiline).
  function extractRows(output, rowsDef) {
    return Object.entries(rowsDef).map(([label, pattern]) => {
      try {
        const hasGroup = /\((?!\?)/.test(pattern);
        const re = new RegExp(pattern, hasGroup ? 'm' : 'gm');
        if (hasGroup) {
          const m = re.exec(output);
          return { label, value: m ? m[1] : '—' };
        }
        const n = (output.match(re) ?? []).length;
        return { label, value: String(n) };
      } catch { return { label, value: '!' }; }
    });
  }

  // Errors come back as pipeline reports — keep the real text (honest
  // failures), but surface only its LAST line in the compact row; the full
  // report lives in the log stream.
  function compactError(text) {
    const lines = (text ?? '').trim().split('\n').filter(Boolean);
    return lines[lines.length - 1] ?? 'failed';
  }

  async function runStatus() {
    if (!statusW || document.hidden) return;
    try {
      const res = await jpost('/api/rfx/run', { name: statusW.run, args: {} });
      if (res.ok) {
        status = { rows: extractRows(res.output ?? '', statusW.rows), age: 0, error: '' };
      } else {
        status = { ...status, error: compactError(res.output || res.error) };
      }
    } catch { status = { ...status, error: 'core offline' }; }
  }

  $effect(() => {
    if (!statusW || !open) return;
    runStatus();
    const t = setInterval(runStatus, parseEvery(statusW.every));
    const age = setInterval(() => { if (status.age !== null) status.age += 1; }, 1000);
    return () => { clearInterval(t); clearInterval(age); };
  });

  $effect(() => {
    if (!running) return;
    elapsed = 0;
    const t = setInterval(() => (elapsed += 0.1), 100);
    return () => clearInterval(t);
  });

  function fmtAge(s) {
    if (s === null) return '';
    return s < 60 ? `${s}s ago` : `${Math.floor(s / 60)}m ago`;
  }

  function targetOf(w) { return members.find((m) => m.name === w.run); }

  async function fire(w) {
    if (running) return;
    const target = targetOf(w);
    if (!target || !enabledSet.has(target.name)) return;
    if (target.risk === 'dangerous' && armed !== w.label) {
      armed = w.label;
      setTimeout(() => { if (armed === w.label) armed = ''; }, 3000);
      return;
    }
    armed = '';
    const args = { ...(w.args ?? {}) };
    // field binding: a field named after a param fills it (docs/RFX-UI.md §2)
    for (const [k, v] of Object.entries(fields)) {
      if (target.params?.properties?.[k] !== undefined && v !== '') args[k] = v;
    }
    running = w.label;
    try {
      const res = await jpost('/api/rfx/run', { name: target.name, args });
      lastRun = {
        label: w.label,
        output: (res.output ?? '') + (!res.ok && res.error ? (res.output ? '\n' : '') + res.error : ''),
        err: !res.ok
      };
      if (statusW && (target.name === statusW.run || !res.err)) runStatus();
      if (res.ok) fields = {}; // a successful run consumes its field input
    } catch (e) {
      lastRun = { label: w.label, output: String(e), err: true };
    } finally {
      running = '';
    }
  }

  function missingField(w) {
    const req = targetOf(w)?.params?.required ?? [];
    return req.some((p) => fieldWs.some((f) => f.name === p) && !(fields[p] ?? '').trim() && !(w.args?.[p]));
  }

  // Enter in a field fires the first button that requires that param.
  function fieldEnter(name) {
    for (const g of groups) {
      if (g.kind !== 'buttons') continue;
      const btn = g.items.find((w) => (targetOf(w)?.params?.required ?? []).includes(name));
      if (btn && !missingField(btn)) { fire(btn); return; }
    }
  }

  async function toggleReflex(name, on) {
    try {
      await jpost('/api/rfx/toggle', { name, enabled: on });
      const m = members.find((x) => x.name === name);
      if (m) m.enabled = on;
    } catch { /* core offline — leave as-is */ }
  }

  function tailLines(text, n) {
    if (!text) return '';
    return text.split('\n').slice(-(n || 8)).join('\n');
  }
</script>

<div class="pcard">
  <button class="phead" onclick={() => (open = !open)}>
    <Zap size={13} />
    <span class="pname">{pack.name}</span>
    <span class="pver">v{pack.version} · {members.length} talents</span>
    <span class="chip" class:chip-dangerous={maxRisk === 'dangerous'} class:chip-sensitive={maxRisk === 'sensitive'}>{maxRisk}</span>
    {#if open}<ChevronUp size={13} />{:else}<ChevronDown size={13} />{/if}
  </button>

  {#if open}
    <div class="pbody">
      {#each groups as g, gi (gi)}
        {#if g.kind === 'status'}
          {#if status.error}
            <div class="status-err">
              <CircleAlert size={12} />
              <span class="err-text">{status.error}</span>
              <span class="err-age mk">{fmtAge(status.age)}</span>
            </div>
          {:else}
            <div class="metrics">
              {#each status.rows as row (row.label)}
                <div class="metric" class:zero={row.value === '0' || row.value === '—'}>
                  <span class="mk">{row.label}</span><span class="mv">{row.value}</span>
                </div>
              {/each}
              <div class="metric age">
                <span class="mk">checked</span>
                <span class="mv dim">{fmtAge(status.age)}</span>
              </div>
            </div>
          {/if}

        {:else if g.kind === 'progress'}
          {#if running}
            <div class="progress">
              <Loader size={11} class="spin" />
              <span class="mk">{running}</span>
              <div class="pbar"><div class="pfill"></div></div>
              <span class="mv dim">{elapsed.toFixed(1)}s</span>
            </div>
          {/if}

        {:else if g.kind === 'buttons'}
          <div class="actions">
            {#each g.items as w (w.label)}
              {@const off = !enabledSet.has(w.run)}
              <button class="act" class:primary={g.items[0] === w} class:armed={armed === w.label}
                disabled={!!running || off || missingField(w)}
                title={off ? `${w.run} is disabled in Settings` : w.run}
                onclick={() => fire(w)}>
                {#if running === w.label}<Loader size={10} class="spin" />{:else}<Play size={10} strokeWidth={2.5} />{/if}
                {armed === w.label ? 'confirm?' : w.label}
              </button>
            {/each}
          </div>

        {:else if g.kind === 'field'}
          <label class="field">
            <span class="mk">{g.w.name}</span>
            <input type="text" value={fields[g.w.name] ?? ''}
              oninput={(e) => (fields[g.w.name] = e.target.value)}
              onkeydown={(e) => e.key === 'Enter' && fieldEnter(g.w.name)}
              placeholder={g.w.name + '…'} />
          </label>

        {:else if g.kind === 'toggle'}
          {@const m = members.find((x) => x.name === g.w.name)}
          {#if m}
            <label class="trow">
              <span class="mk">{m.name}</span>
              <input type="checkbox" checked={enabledSet.has(m.name)}
                onchange={(e) => toggleReflex(m.name, e.target.checked)} />
            </label>
          {/if}

        {:else if g.kind === 'log'}
          {#if lastRun.output}
            <div class="logbox" class:err={lastRun.err}>
              <div class="loghead">
                <span class="dot" class:ok={!lastRun.err} class:bad={lastRun.err}></span>
                <span class="mk">{lastRun.label}</span>
              </div>
              <pre>{tailLines(lastRun.output, g.w.lines)}</pre>
            </div>
          {/if}
        {/if}
      {/each}
    </div>
  {/if}
</div>

<style>
  .pcard {
    border-radius: 10px; overflow: hidden;
    background: color-mix(in srgb, #fff 2.5%, transparent);
    box-shadow: inset 0 0 0 1px var(--ring, var(--line));
  }
  .phead {
    display: flex; align-items: center; gap: 8px; width: 100%;
    padding: 10px 12px; border: none; cursor: pointer; background: transparent;
    color: var(--accent); text-align: left;
  }
  .pname { font-family: var(--font-mono, monospace); font-size: 12px; font-weight: 650; letter-spacing: .08em; color: var(--text); }
  .pver { flex: 1; font-size: 9.5px; color: var(--faint); }
  .chip {
    font-size: 9px; font-weight: 600; letter-spacing: .04em; padding: 2px 7px; border-radius: 5px;
    color: var(--dim); background: var(--s3); box-shadow: inset 0 0 0 1px var(--line);
  }
  .chip-sensitive { color: var(--warn, #b87a00); }
  .chip-dangerous { color: var(--err); }
  .pbody { padding: 2px 12px 12px; display: flex; flex-direction: column; gap: 10px; }

  .mk { font-family: var(--font-mono, monospace); font-size: 8px; letter-spacing: .12em; text-transform: uppercase; color: var(--faint); }
  .mv { font-family: var(--font-mono, monospace); font-size: 12px; color: var(--text); }
  .mv.dim { color: var(--dim); font-size: 10px; }

  /* metrics — the hardware-panel identity */
  .metrics { display: flex; flex-wrap: wrap; gap: 6px; }
  .metric {
    display: flex; flex-direction: column; gap: 2px;
    padding: 6px 9px; border-radius: 6px; min-width: 54px;
    background: color-mix(in srgb, #fff 2.5%, transparent);
    box-shadow: inset 0 0 0 1px var(--ring, var(--line));
  }
  .metric.zero .mv { color: var(--dim); }
  .metric.age { margin-left: auto; min-width: 0; box-shadow: none; background: transparent; }

  /* compact honest-failure row: last line of the real error, never a wall */
  .status-err {
    display: flex; align-items: center; gap: 7px;
    padding: 7px 9px; border-radius: 6px;
    color: var(--warn, #b87a00);
    background: color-mix(in srgb, #fff 2.5%, transparent);
    box-shadow: inset 0 0 0 1px var(--ring, var(--line));
  }
  .status-err :global(svg) { flex-shrink: 0; }
  .err-text {
    flex: 1; min-width: 0; font-family: var(--font-mono, monospace); font-size: 10px;
    line-height: 1.4; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .err-age { flex-shrink: 0; }

  /* progress — visible only while a run is in flight */
  .progress { display: flex; align-items: center; gap: 8px; color: var(--accent); }
  .pbar { flex: 1; height: 2px; border-radius: 1px; overflow: hidden; background: var(--s3); }
  .pfill { height: 100%; width: 40%; border-radius: 1px; background: var(--accent); animation: slide 1.1s ease-in-out infinite; }
  @keyframes slide { 0% { margin-left: -40%; } 100% { margin-left: 100%; } }
  :global(.spin) { animation: spin 1s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg); } }

  .actions { display: flex; flex-wrap: wrap; gap: 6px; }
  .act {
    display: inline-flex; align-items: center; gap: 6px;
    font-size: 11px; font-weight: 600; padding: 6px 12px; border: none; border-radius: 7px;
    cursor: pointer; background: var(--s3); color: var(--text);
    box-shadow: inset 0 0 0 1px var(--line);
    transition: filter .1s;
  }
  .act.primary { background: var(--accent); color: var(--accent-ink); box-shadow: none; }
  .act.armed { background: var(--err); color: #fff; box-shadow: none; }
  .act:hover:not(:disabled) { filter: brightness(1.12); }
  .act:disabled { opacity: .45; cursor: default; }

  .field { display: flex; align-items: center; gap: 8px; }
  .field input {
    flex: 1; min-width: 0; font-size: 11.5px; padding: 6px 9px; border-radius: 6px;
    border: 1px solid var(--line); background: var(--bg); color: var(--text); outline: none;
    font-family: var(--font-mono, monospace);
  }
  .field input:focus { border-color: var(--accent); }

  .trow { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
  .trow input { accent-color: var(--accent); }

  .logbox { border-radius: 6px; border: 1px solid var(--line); background: var(--bg); overflow: hidden; }
  .logbox.err { border-color: color-mix(in srgb, var(--err) 45%, transparent); }
  .loghead { display: flex; align-items: center; gap: 6px; padding: 6px 8px 0; }
  .dot { width: 5px; height: 5px; border-radius: 50%; }
  .dot.ok { background: var(--ok, #4bb894); }
  .dot.bad { background: var(--err); }
  .logbox pre {
    margin: 0; padding: 5px 8px 8px; max-height: 180px; overflow: auto;
    font-family: var(--font-mono, monospace); font-size: 10px; line-height: 1.5;
    white-space: pre-wrap; word-break: break-word; color: var(--text);
  }
  .logbox.err pre { color: color-mix(in srgb, var(--err) 75%, var(--text)); }
</style>
