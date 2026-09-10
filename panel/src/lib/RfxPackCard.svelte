<script>
 import { sessionStore } from './stores/session.svelte.ts';
  import { onDestroy, untrack } from 'svelte';
  import { Zap, Play, ChevronDown, ChevronUp, Loader, CircleAlert, Sparkles } from 'lucide-svelte';
  import { rfxIcon } from './rfxIcons.js';
  import { jpost } from './api';
  import PackSource from './PackSource.svelte';
  import { rfxParams, parseRfxArgs, canAutoRun, RfxRequestScope, contextImages, attachContextImage } from './rfxArgs';

  // RfxPackCard — one talent pack's cockpit in the RFX dock (docs-private/RFX-UI.md).
  // Chrome is shared and fixed; content is the pack's ui: widget list,
  // rendered IN AUTHOR ORDER (the manifest is the layout). Visual identity:
  // hardware panels — mono metrics, inset rings, one accent per card.
  let { pack, members, sessionId = null } = $props(); // members: ALL of this pack's reflexes (enabled or not)
  const sid = $derived(sessionId ?? sessionStore.activeId);
  const scope = new RfxRequestScope();
  let armTimer;

  let open = $state(true);
  let status = $state({ rows: [], age: null, error: '', output: '' });
  let fields = $state({});
  let lastRun = $state({ label: '', output: '', err: false });
  let running = $state('');          // label of the in-flight button ('' = idle)
  let elapsed = $state(0);
  let armed = $state('');            // dangerous tier: two-click arm/confirm
  let formError = $state('');

  const widgets = $derived(pack.ui?.widgets ?? []);
  const enabledSet = $derived(new Set(members.filter((m) => m.enabled === true).map((m) => m.name)));
  const statusW = $derived(widgets.find((w) => w.type === 'status'));
  const fieldWs = $derived(widgets.filter((w) => w.type === 'field'));
  const maxRisk = $derived(members.some((m) => m.risk === 'dangerous') ? 'dangerous'
    : members.some((m) => m.risk === 'sensitive') ? 'sensitive' : 'safe');
  const PackIcon = $derived(rfxIcon(pack.icon, Zap));
  onDestroy(() => { scope.dispose(); clearTimeout(armTimer); });
  $effect(() => {
    sid;
    scope.invalidate(); running = ''; armed = ''; fields = {}; formError = '';
    status = { rows: [], age: null, error: '', output: '' };
    lastRun = { label: '', output: '', err: false };
    clearTimeout(armTimer);
  });

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

  // status rows: {re, tone} — capture group → first group of first match;
  // no group → match count (multiline). Tone is the author's semantic hint;
  // the theme owns the actual color.
  function extractRows(output, rowsDef) {
    return Object.entries(rowsDef).map(([label, row]) => {
      const pattern = row.re ?? row; // tolerate pre-v1.3 scalar form
      const tone = row.tone ?? '';
      try {
        const hasGroup = /\((?!\?)/.test(pattern);
        const re = new RegExp(pattern, hasGroup ? 'm' : 'gm');
        if (hasGroup) {
          const m = re.exec(output);
          return { label, tone, value: m ? m[1].trim() : '—' };
        }
        const n = (output.match(re) ?? []).length;
        return { label, tone, value: String(n) };
      } catch { return { label, tone, value: '!' }; }
    });
  }

  // list widget: matched lines from the status run's output
  function listLines(w) {
    try {
      const re = new RegExp(w.match, 'gm');
      return (status.output.match(re) ?? []).slice(0, w.limit || 6);
    } catch { return []; }
  }

  // Errors come back as pipeline reports (== step headers + output). Strip
  // the report scaffolding, keep the real message lines — shown IN FULL
  // (a truncated error is no error at all).
  function cleanError(text) {
    const lines = (text ?? '').trim().split('\n')
      .filter((l) => l.trim() && !l.startsWith('==') && !l.startsWith('reflex '));
    return lines.join('\n') || 'failed';
  }

  async function runStatus() {
    const widget = statusW;
    const target = widget && targetOf(widget);
    if (!canAutoRun(target, { sessionId: sid, busy: sessionStore.running || sessionStore.connectionLost, pending: !!running, hidden: document.hidden, open })) return;
    const token = scope.begin(sid);
    if (!token) return;
    running = 'Refreshing status';
    try {
      const res = await jpost('/api/rfx/run', { session_id: token.sessionId, name: widget.run, args: {}, confirmed: false });
      if (!scope.current(token, sid)) return;
      if (res.ok) {
        status = { rows: extractRows(res.output ?? '', widget.rows), age: 0, error: '', output: res.output ?? '' };
      } else {
        status = { ...status, output: '', error: cleanError(res.output || res.error) };
      }
    } catch (error) { if (scope.current(token, sid)) status = { ...status, error: String(error) }; }
    finally { scope.finish(token); if (scope.current(token, sid)) running = ''; }
  }

  $effect(() => {
    if (!statusW || !open || !sid) return;
    enabledSet; // disabling a member cancels its polling schedule
    untrack(() => { void runStatus(); });
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
  function fieldSchema(name) {
    for (const member of members) {
      const param = rfxParams(member.params).find((item) => item.name === name);
      if (param) return param;
    }
    return { name, type: 'string' };
  }
  function changeField(name, value) { fields[name] = value; armed = ''; formError = ''; }

  async function fire(w) {
    if (running || !sid || sessionStore.running || sessionStore.connectionLost) return;
    const target = targetOf(w);
    if (!target || !enabledSet.has(target.name)) return;
    let args;
    try { args = parseRfxArgs(target.params, fields, w.args); }
    catch (error) { formError = String(error); return; }
    formError = '';
    if (target.risk === 'dangerous' && armed !== w.label) {
      armed = w.label;
      clearTimeout(armTimer);
      armTimer = setTimeout(() => { if (armed === w.label) armed = ''; }, 5000);
      return;
    }
    const confirmed = target.risk === 'dangerous'; // second (armed) click
    armed = '';
    const token = scope.begin(sid);
    if (!token) { formError = 'Another RFX request is awaiting acknowledgement in this session.'; return; }
    running = w.label;
    try {
      const res = await jpost('/api/rfx/run', { session_id: token.sessionId, name: target.name, args, confirmed });
      if (!scope.current(token, sid)) return;
      lastRun = {
        label: w.label,
        output: (res.output ?? '') + (!res.ok && res.error ? (res.output ? '\n' : '') + res.error : ''),
        err: !res.ok
      };
      if (statusW && target.name === statusW.run && res.ok) status = { rows: extractRows(res.output ?? '', statusW.rows), age: 0, error: '', output: res.output ?? '' };
      // Keep useful fields after reads and writes. A successful inspection does
      // not consume the URL, workspace path, or the user's next check arguments.
    } catch (e) {
      if (scope.current(token, sid)) lastRun = { label: w.label, output: String(e), err: true };
    } finally {
      scope.finish(token);
      if (scope.current(token, sid)) running = '';
    }
  }

  function missingField(w) {
    const req = targetOf(w)?.params?.required ?? [];
    return req.some((p) => fieldWs.some((f) => f.name === p) && [undefined, null, ''].includes(fields[p]) && [undefined, null, ''].includes(w.args?.[p]));
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
    if (running || sessionStore.running) return;
    const token = scope.begin(sid);
    if (!token) return;
    running = 'Updating talent';
    try {
      await jpost('/api/rfx/toggle', { name, enabled: on });
      if (!scope.current(token, sid)) return;
      const m = members.find((x) => x.name === name);
      if (m) m.enabled = on;
    } catch (error) { if (scope.current(token, sid)) formError = `Could not update ${name}: ${String(error)}`; }
    finally { scope.finish(token); if (scope.current(token, sid)) running = ''; }
  }

  function tailLines(text, n) {
    if (!text) return '';
    return text.split('\n').slice(-(n || 8)).join('\n');
  }
</script>

<div class="pcard">
  <button class="phead" aria-expanded={open} onclick={() => (open = !open)}>
    <PackIcon size={13} />
    <span class="pname">{pack.name}</span>
    <span class="pver">v{pack.version} · {members.length} talents</span>
    <span class="chip" class:chip-dangerous={maxRisk === 'dangerous'} class:chip-sensitive={maxRisk === 'sensitive'}>{maxRisk}</span>
    {#if open}<ChevronUp size={13} />{:else}<ChevronDown size={13} />{/if}
  </button>

  <PackSource {pack} />
  {#if open}
    <div class="pbody">
      {#if formError}<p class="form-error" role="alert">{formError}</p>{/if}
      {#each groups as g, gi (gi)}
        {#if g.kind === 'status'}
          {#if targetOf(g.w)?.risk !== 'safe' || !enabledSet.has(g.w.run) || targetOf(g.w)?.params?.required?.length}
            <p class="status-note">Automatic status is off. Only enabled, parameter-free safe talents may poll.</p>
          {/if}
          {#if status.error}
            <div class="status-fail">
              <div class="sf-head">
                <CircleAlert size={12} />
                <span class="mk">unavailable</span>
                <span class="mk sf-age">{fmtAge(status.age)}</span>
              </div>
              <div class="sf-text">{status.error}</div>
              {#if g.w.on_fail}
                {@const RemedyIcon = rfxIcon(g.w.on_fail.icon, Sparkles)}
                <button class="act primary sf-remedy" disabled={!!running || !sid || sessionStore.running || sessionStore.connectionLost || !enabledSet.has(g.w.on_fail.run)}
                  onclick={() => fire({ label: g.w.on_fail.label, run: g.w.on_fail.run })}>
                  {#if running === g.w.on_fail.label}<Loader size={10} class="spin" />{:else}<RemedyIcon size={11} strokeWidth={2.4} />{/if}
                  {armed === g.w.on_fail.label ? 'Confirm run' : g.w.on_fail.label}
                </button>
              {/if}
            </div>
          {:else}
            <div class="metrics">
              {#each status.rows as row (row.label)}
                <div class="metric" class:zero={row.value === '0' || row.value === '—'}>
                  <span class="mk">{row.label}</span>
                  <span class="mv" class:t-ok={row.tone === 'ok'} class:t-err={row.tone === 'err'}
                    class:t-warn={row.tone === 'warn'} class:t-accent={row.tone === 'accent'}>{row.value}</span>
                </div>
              {/each}
              <div class="metric age">
                <span class="mk">checked</span>
                <span class="mv dim">{fmtAge(status.age)}</span>
              </div>
            </div>
          {/if}

        {:else if g.kind === 'list'}
          {@const lines = listLines(g.w)}
          {#if lines.length}
            <div class="filelist">
              {#each lines as line (line)}
                <div class="fl-row">{line}</div>
              {/each}
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
              {@const BtnIcon = rfxIcon(w.icon, Play)}
              <button class="act" class:primary={g.items[0] === w} class:armed={armed === w.label}
                disabled={!!running || off || missingField(w) || !sid || sessionStore.running || sessionStore.connectionLost}
                title={off ? `${w.run} is disabled in Settings` : w.run}
                onclick={() => fire(w)}>
                {#if running === w.label}<Loader size={10} class="spin" />{:else}<BtnIcon size={10} strokeWidth={2.5} />{/if}
                {armed === w.label ? 'Confirm run' : w.label}
              </button>
            {/each}
          </div>

        {:else if g.kind === 'field'}
          {@const param = fieldSchema(g.w.name)}
          <label class="field" class:structured={param.type === 'array' || param.type === 'object'}>
            <span class="mk">{g.w.label || g.w.name}</span>
            {#if param.enum || param.type === 'boolean'}
              <select value={String(fields[g.w.name] ?? '')} onchange={(e) => changeField(g.w.name, e.target.value)}>
                <option value="">Not set</option>{#each param.enum ?? [true, false] as value}<option value={String(value)}>{String(value)}</option>{/each}
              </select>
            {:else if param.type === 'array' || param.type === 'object'}
              <textarea rows="3" value={fields[g.w.name] ?? ''} oninput={(e) => changeField(g.w.name, e.target.value)} placeholder={param.type === 'array' ? '["path/to/file"]' : '{"key":"value"}'} spellcheck="false"></textarea>
            {:else}
              <input type={param.type === 'integer' || param.type === 'number' ? 'number' : 'text'} value={fields[g.w.name] ?? ''}
                oninput={(e) => changeField(g.w.name, e.target.value)}
                onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); fieldEnter(g.w.name); } }}
                placeholder={g.w.name + '…'} />
            {/if}
          </label>

        {:else if g.kind === 'toggle'}
          {@const m = members.find((x) => x.name === g.w.name)}
          {#if m}
            <label class="trow">
              <span class="mk">{m.name}</span>
              <input type="checkbox" checked={enabledSet.has(m.name)} disabled={!!running || !sid || sessionStore.running}
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
      {#if lastRun.output && !lastRun.err}
        {#each contextImages(lastRun.output) as image (image.path)}
          <button class="act" disabled={!!running || sessionStore.running || sessionStore.connectionLost} onclick={() => attachContextImage(image, sid)}>Attach to chat · {image.width}×{image.height}</button>
        {/each}
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
  .form-error { color: var(--err); font-size: 12px; line-height: 1.45; overflow-wrap: anywhere; }
  .status-note { color: var(--muted); font-size: 11px; line-height: 1.4; }
  button:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }

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
  /* author-declared semantic tones — theme owns the actual colors */
  .metric:not(.zero) .mv.t-ok { color: var(--ok, #4bb894); }
  .metric:not(.zero) .mv.t-err { color: var(--err); }
  .metric:not(.zero) .mv.t-warn { color: var(--warn, #b87a00); }
  .metric:not(.zero) .mv.t-accent { color: var(--accent); }

  /* list — matched lines from the status output (e.g. changed files) */
  .filelist {
    border-radius: 6px; padding: 4px 0;
    background: color-mix(in srgb, #fff 2%, transparent);
    box-shadow: inset 0 0 0 1px var(--ring, var(--line));
  }
  .fl-row {
    padding: 2px 9px; font-family: var(--font-mono, monospace); font-size: 10px;
    line-height: 1.5; color: var(--muted);
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }

  /* Keep the real failure readable without widening the dock. */
  .status-fail {
    display: flex; flex-direction: column; gap: 7px;
    padding: 7px 9px; border-radius: 6px;
    color: var(--warn, #b87a00);
    background: color-mix(in srgb, #fff 2.5%, transparent);
    box-shadow: inset 0 0 0 1px var(--ring, var(--line));
  }
  .sf-head { display: flex; align-items: center; gap: 6px; }
  .sf-head :global(svg) { flex-shrink: 0; }
  .sf-text {
    min-width: 0; font-family: var(--font-mono, monospace); font-size: 11px;
    line-height: 1.45; max-height: 180px; overflow: auto; overflow-wrap: anywhere; white-space: pre-wrap;
  }
  .sf-age { margin-left: auto; }

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
  .act.primary { background: var(--accent); color: var(--on-accent); box-shadow: none; }
  .act.armed { background: var(--err); color: #fff; box-shadow: none; }
  .act:hover:not(:disabled) { filter: brightness(1.12); }
  .act:disabled { opacity: .45; cursor: default; }

  .field { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
  .field input, .field select, .field textarea {
    flex: 1; min-width: 0; font-size: 11.5px; padding: 6px 9px; border-radius: 6px;
    border: 1px solid var(--line); background: var(--bg); color: var(--text); outline: none;
    font-family: var(--font-mono, monospace);
  }
  .field textarea { flex-basis: 100%; resize: vertical; }
  .field input:focus, .field select:focus, .field textarea:focus { border-color: var(--accent); }

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
  @media (max-width: 900px) {
    .field input, .field select, .field textarea { font-size: 16px; }
    .act { min-height: 40px; }
    .pname { overflow-wrap: anywhere; min-width: 0; }
    .phead { flex-wrap: wrap; }
  }
  @media (prefers-reduced-motion: reduce) { :global(.spin), .pfill { animation: none; } }
</style>
