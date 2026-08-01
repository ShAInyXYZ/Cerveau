<script>
  import { Zap, Play, ChevronDown, ChevronUp } from 'lucide-svelte';
  import { jpost } from './api.js';

  // RfxPackCard — one talent pack's cockpit in the RFX dock (docs/RFX-UI.md).
  // Chrome is shared and fixed; content is the pack's ui: widget list.
  // Visual identity: the header's hardware panels (mono metrics, inset rings,
  // segmented accents) applied to reflexes.
  let { pack, members } = $props(); // members: this pack's enabled reflexes

  let open = $state(true);
  let status = $state({ rows: [], age: null, error: '' });
  let fields = $state({});
  let lastRun = $state({ label: '', output: '', err: false, running: false });

  const widgets = $derived(pack.ui?.widgets ?? []);
  const statusW = $derived(widgets.find((w) => w.type === 'status'));
  const buttons = $derived(widgets.filter((w) => w.type === 'button'));
  const fieldWs = $derived(widgets.filter((w) => w.type === 'field'));
  const logW = $derived(widgets.find((w) => w.type === 'log'));
  const maxRisk = $derived(members.some((m) => m.risk === 'dangerous') ? 'dangerous'
    : members.some((m) => m.risk === 'sensitive') ? 'sensitive' : 'safe');

  function parseEvery(e) {
    const m = /^(\d+)(s|m)$/.exec(e ?? '');
    return m ? (+m[1]) * (m[2] === 'm' ? 60000 : 1000) : 30000;
  }

  // status rows: regex with capture group → first group of first match;
  // no group → match count. Rows render as mono metric chips.
  function extractRows(output, rowsDef) {
    return Object.entries(rowsDef).map(([label, pattern]) => {
      try {
        const hasGroup = pattern.includes('(');
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

  async function runStatus() {
    if (!statusW || document.hidden) return;
    try {
      const res = await jpost('/api/rfx/run', { name: statusW.run, args: {} });
      if (res.ok) {
        status = { rows: extractRows(res.output ?? '', statusW.rows), age: 0, error: '' };
      } else {
        status = { ...status, error: res.error ?? 'failed' };
      }
    } catch { status = { ...status, error: 'offline' }; }
  }

  $effect(() => {
    if (!statusW) return;
    runStatus();
    const every = parseEvery(statusW.every);
    const t = setInterval(runStatus, every);
    const age = setInterval(() => { if (status.age !== null) status.age += 1; }, 1000);
    return () => { clearInterval(t); clearInterval(age); };
  });

  function fmtAge(s) {
    if (s === null) return '';
    if (s < 60) return `${s}s ago`;
    return `${Math.floor(s / 60)}m ago`;
  }

  // dangerous tier: two-click arm/confirm — a misclick never fires it
  let armed = $state('');

  async function fire(w) {
    const target = w.run ?? members[0]?.name;
    const targetDef = members.find((m) => m.name === target);
    if (targetDef?.risk === 'dangerous' && armed !== w.label) {
      armed = w.label;
      setTimeout(() => { if (armed === w.label) armed = ''; }, 3000);
      return;
    }
    armed = '';
    const args = { ...(w.args ?? {}) };
    // field binding: a field named after a param fills it (docs/RFX-UI.md §2)
    for (const [k, v] of Object.entries(fields)) {
      if (targetDef?.params?.properties?.[k] !== undefined && v !== '') args[k] = v;
    }
    lastRun = { label: w.label, output: '', err: false, running: true };
    try {
      const res = await jpost('/api/rfx/run', { name: target, args });
      lastRun = { label: w.label, output: res.output ?? '', err: !res.ok, running: false };
      if (!res.ok && res.error) lastRun.output += (lastRun.output ? '\n' : '') + res.error;
      if (statusW && target === statusW.run) runStatus();
    } catch (e) {
      lastRun = { label: w.label, output: String(e), err: true, running: false };
    }
  }

  function needsField(w) {
    const target = members.find((m) => m.name === (w.run ?? ''));
    return Object.keys(target?.params?.required ?? []).length > 0
      && (target?.params?.required ?? []).some((p) => fieldWs.some((f) => f.name === p) && !fields[p]);
  }

  function tailLines(text, n) {
    if (!text) return '';
    const lines = text.split('\n');
    return lines.slice(-(n || 8)).join('\n');
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
      {#if statusW}
        {#if status.error}
          <div class="status-err"><span class="mk">status</span><span>{status.error}</span></div>
        {:else}
          <div class="metrics">
            {#each status.rows as row (row.label)}
              <div class="metric"><span class="mk">{row.label}</span><span class="mv">{row.value}</span></div>
            {/each}
            <div class="metric age">
              <span class="mk">checked</span>
              <span class="mv dim">{fmtAge(status.age)}</span>
            </div>
          </div>
        {/if}
      {/if}

      {#if buttons.length > 0}
        <div class="actions">
          {#each buttons as w (w.label)}
            <button class="act" class:armed={armed === w.label}
              disabled={lastRun.running || needsField(w)} onclick={() => fire(w)}>
              <Play size={10} strokeWidth={2.5} />{armed === w.label ? 'confirm?' : w.label}
            </button>
          {/each}
        </div>
      {/if}

      {#each fieldWs as w (w.name)}
        <label class="field">
          <span class="mk">{w.name}</span>
          <input type="text" value={fields[w.name] ?? ''}
            oninput={(e) => (fields[w.name] = e.target.value)}
            placeholder={w.name + '…'} />
        </label>
      {/each}

      {#if logW && lastRun.output}
        <div class="logbox" class:err={lastRun.err}>
          <div class="loghead mk">{lastRun.label} · last run</div>
          <pre>{tailLines(lastRun.output, logW.lines)}</pre>
        </div>
      {/if}
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

  /* metrics — the hardware-panel identity */
  .metrics { display: flex; flex-wrap: wrap; gap: 6px; }
  .metric {
    display: flex; flex-direction: column; gap: 2px;
    padding: 6px 9px; border-radius: 6px; min-width: 58px;
    background: color-mix(in srgb, #fff 2.5%, transparent);
    box-shadow: inset 0 0 0 1px var(--ring, var(--line));
  }
  .metric.age { margin-left: auto; min-width: 0; }
  .mk { font-family: var(--font-mono, monospace); font-size: 8px; letter-spacing: .12em; text-transform: uppercase; color: var(--faint); }
  .mv { font-family: var(--font-mono, monospace); font-size: 12px; color: var(--text); }
  .mv.dim { color: var(--dim); font-size: 10px; }

  .status-err {
    display: flex; align-items: baseline; gap: 8px;
  }
  .status-err .mk { flex-shrink: 0; }
  .status-err {
    padding: 7px 9px; border-radius: 6px; font-size: 10.5px; color: var(--warn, #b87a00);
    background: color-mix(in srgb, #fff 2.5%, transparent);
    box-shadow: inset 0 0 0 1px var(--ring, var(--line));
    font-family: var(--font-mono, monospace); overflow-wrap: anywhere;
  }

  .actions { display: flex; flex-wrap: wrap; gap: 6px; }
  .act {
    display: inline-flex; align-items: center; gap: 6px;
    font-size: 11px; font-weight: 600; padding: 6px 12px; border: none; border-radius: 7px;
    cursor: pointer; background: var(--accent); color: var(--accent-ink);
    transition: filter .1s;
  }
  .act + .act { background: var(--s3); color: var(--text); box-shadow: inset 0 0 0 1px var(--line); }
  .act.armed { background: var(--err); color: #fff; }
  .act:hover:not(:disabled) { filter: brightness(1.12); }
  .act:disabled { opacity: .45; cursor: default; }

  .field { display: flex; align-items: center; gap: 8px; }
  .field input {
    flex: 1; min-width: 0; font-size: 11.5px; padding: 6px 9px; border-radius: 6px;
    border: 1px solid var(--line); background: var(--bg); color: var(--text); outline: none;
    font-family: var(--font-mono, monospace);
  }
  .field input:focus { border-color: var(--accent); }

  .logbox { border-radius: 6px; border: 1px solid var(--line); background: var(--bg); overflow: hidden; }
  .logbox.err { border-color: var(--err); }
  .loghead { padding: 5px 8px 0; }
  .logbox pre {
    margin: 0; padding: 6px 8px 8px; max-height: 180px; overflow: auto;
    font-family: var(--font-mono, monospace); font-size: 10px; line-height: 1.5;
    white-space: pre-wrap; word-break: break-word; color: var(--text);
  }
  .logbox.err pre { color: var(--err); }
</style>
