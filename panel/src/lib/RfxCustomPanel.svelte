<script>
  import { Zap, ChevronDown, ChevronUp, ShieldAlert } from 'lucide-svelte';
  import { rfxIcon } from './rfxIcons.js';
  import { j, jpost, fetchEvents } from './api.js';

  // RfxCustomPanel — RFX-UI tier 2: the pack ships its own ui/panel.html
  // (any HTML/CSS/JS) rendered in a SANDBOXED iframe. Full presentation
  // freedom; zero capability beyond the bridge:
  //   - sandbox="allow-scripts" → opaque origin, no parent DOM, no cookies
  //   - CSP connect-src 'none'  → no fetch/XHR/WebSocket from panel code
  //   - every rfx.run() lands HERE, is checked against the pack's members,
  //     and dangerous targets need the host confirm strip below — chrome
  //     the panel cannot draw over.
  // Presentation is free. Capability still belongs to RFX.
  let { pack, members, sessionId = null, onTurn = null } = $props();

  let open = $state(true);
  let frameH = $state(260);
  let pendingDanger = $state(null); // {id, name, args, source}
  let iframeEl = $state(null);

  const PackIcon = $derived(rfxIcon(pack.icon, Zap));
  const enabledSet = $derived(new Set(members.filter((m) => m.enabled !== false).map((m) => m.name)));
  const maxRisk = $derived(members.some((m) => m.risk === 'dangerous') ? 'dangerous'
    : members.some((m) => m.risk === 'sensitive') ? 'sensitive' : 'safe');

  async function execute(id, name, args, source, confirmed = false) {
    try {
      const res = await jpost('/api/rfx/run', { name, args: args ?? {}, confirmed });
      source.postMessage({ rfx: 'result', id, ok: !!res.ok, output: res.output ?? '', error: res.error ?? '' }, '*');
    } catch (e) {
      source.postMessage({ rfx: 'result', id, ok: false, output: '', error: String(e) }, '*');
    }
  }

  const reply = (src, id, body) => src.postMessage({ rfx: 'result', id, ...body }, '*');

  // session(): read-only projection of the CURRENT session — plan +
  // checkpoints + running state. Capability-gated by ui.session; never any
  // session but the active one.
  async function readSession(id, source) {
    if (!pack.ui?.session) return reply(source, id, { ok: false, error: 'pack does not declare ui.session' });
    if (!sessionId) return reply(source, id, { ok: false, error: 'no active session' });
    try {
      // /events is JSONL, not JSON — fetchEvents parses it line by line
      const [state, list] = await Promise.all([
        j(`/api/sessions/${sessionId}/state`),
        fetchEvents(`/api/sessions/${sessionId}/events`)
      ]);
      const checkpoints = list.filter((e) => e.type === 'checkpoint')
        .map((e) => ({ ...(e.payload ?? {}), ts: e.ts }));
      const closes = list.filter((e) => e.type === 'turn.close');
      // the stop reason lives in the error event, not turn.close
      const errs = list.filter((e) => e.type === 'error');
      const lastErr = errs.length ? (errs[errs.length - 1].payload ?? {}) : null;
      reply(source, id, {
        ok: true,
        session: sessionId,
        plan: state?.plan ?? null,
        checkpoints,
        running: !!state?.running,
        lastClose: closes.length ? (closes[closes.length - 1].payload ?? {}) : null,
        lastError: lastErr,
        // index of the last event of each kind, so a panel can tell whether
        // the error came BEFORE or AFTER the most recent completed turn
        lastErrorAt: errs.length ? list.lastIndexOf(errs[errs.length - 1]) : -1,
        lastCloseAt: closes.length ? list.lastIndexOf(closes[closes.length - 1]) : -1
      });
    } catch (e) {
      reply(source, id, { ok: false, error: String(e) });
    }
  }

  // turn(): post a turn into the current session — the same thing the user
  // could type. Capability-gated by ui.turn; the host owns the call and it
  // lands in the chat stream visibly.
  async function postTurn(id, text, mode, source) {
    if (!pack.ui?.turn) return reply(source, id, { ok: false, error: 'pack does not declare ui.turn' });
    if (!sessionId) return reply(source, id, { ok: false, error: 'no active session' });
    const t = (text ?? '').trim();
    if (!t) return reply(source, id, { ok: false, error: 'empty turn' });
    try {
      const res = await onTurn?.(t, mode);
      reply(source, id, { ok: res !== false });
    } catch (e) {
      reply(source, id, { ok: false, error: String(e) });
    }
  }

  function onMessage(e) {
    if (!iframeEl || e.source !== iframeEl.contentWindow) return;
    const m = e.data ?? {};
    if (m.rfx === 'resize') {
      frameH = Math.max(120, Math.min(720, +m.h || 260));
      return;
    }
    if (m.rfx === 'session') { readSession(m.id, e.source); return; }
    if (m.rfx === 'turn') { postTurn(m.id, m.text, m.mode, e.source); return; }
    if (m.rfx !== 'run') return;
    const target = members.find((x) => x.name === m.name);
    if (!target) {
      e.source.postMessage({ rfx: 'result', id: m.id, ok: false, output: '', error: `"${m.name}" is not a reflex of this pack` }, '*');
      return;
    }
    if (!enabledSet.has(m.name)) {
      e.source.postMessage({ rfx: 'result', id: m.id, ok: false, output: '', error: `${m.name} is disabled in Settings` }, '*');
      return;
    }
    if (target.risk === 'dangerous') {
      // host-owned confirm: the panel cannot draw over this strip
      pendingDanger = { id: m.id, name: m.name, args: m.args, source: e.source };
      return;
    }
    execute(m.id, m.name, m.args, e.source);
  }

  function approveDanger() {
    const p = pendingDanger;
    pendingDanger = null;
    if (p) execute(p.id, p.name, p.args, p.source, true);
  }
  function denyDanger() {
    const p = pendingDanger;
    pendingDanger = null;
    if (p) p.source.postMessage({ rfx: 'result', id: p.id, ok: false, output: '', error: 'denied by the user' }, '*');
  }

  $effect(() => {
    addEventListener('message', onMessage);
    return () => removeEventListener('message', onMessage);
  });
</script>

<div class="cpanel">
  <button class="phead" onclick={() => (open = !open)}>
    <PackIcon size={13} />
    <span class="pname">{pack.name}</span>
    <span class="pver">v{pack.version} · {members.length ? 'custom panel' : 'supervisor'}</span>
    <span class="chip" class:chip-dangerous={maxRisk === 'dangerous'} class:chip-sensitive={maxRisk === 'sensitive'}>{maxRisk}</span>
    {#if open}<ChevronUp size={13} />{:else}<ChevronDown size={13} />{/if}
  </button>

  {#if open}
    {#if pendingDanger}
      <div class="danger-strip">
        <ShieldAlert size={13} />
        <span class="ds-text">run <b>{pendingDanger.name}</b>? (dangerous)</span>
        <button class="ds-btn ok" onclick={approveDanger}>run</button>
        <button class="ds-btn" onclick={denyDanger}>deny</button>
      </div>
    {/if}
    <iframe
      bind:this={iframeEl}
      class="frame"
      style="height: {frameH}px"
      src="/api/rfx/panel/{pack.name}"
      sandbox="allow-scripts"
      title="{pack.name} panel"
    ></iframe>
  {/if}
</div>

<style>
  .cpanel {
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

  .danger-strip {
    display: flex; align-items: center; gap: 8px;
    padding: 8px 12px; color: var(--err);
    background: color-mix(in srgb, var(--err) 9%, transparent);
    border-top: 1px solid color-mix(in srgb, var(--err) 35%, transparent);
    border-bottom: 1px solid color-mix(in srgb, var(--err) 35%, transparent);
  }
  .ds-text { flex: 1; font-size: 11px; }
  .ds-btn {
    font-size: 10.5px; font-weight: 600; padding: 4px 12px; border: none; border-radius: 6px;
    cursor: pointer; background: var(--s3); color: var(--text);
    box-shadow: inset 0 0 0 1px var(--line);
  }
  .ds-btn.ok { background: var(--err); color: #fff; box-shadow: none; }

  .frame { display: block; width: 100%; border: none; background: transparent; }
</style>
