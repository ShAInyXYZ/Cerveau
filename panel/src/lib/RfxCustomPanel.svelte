<script>
 import { sessionStore } from './stores/session.svelte.ts';
  import { Zap, ChevronDown, ChevronUp, ShieldAlert } from 'lucide-svelte';
  import { rfxIcon } from './rfxIcons.js';
  import { j, jpost, ApiError } from './api';
  import PackSource from './PackSource.svelte';

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
  // Retain identity when a POST's acknowledgement is lost. A retry of the
  // same uncertain intent must discover the accepted run, not repeat it.
  const uncertainPlanCommands = new Map();
  function uncertainCommand(key) {
    try { const saved = sessionStorage.getItem('crv.rfx.pending.' + key); if (saved) return saved; } catch {}
    return uncertainPlanCommands.get(key);
  }
  function rememberCommand(key, value) {
    if (value) uncertainPlanCommands.set(key, value); else uncertainPlanCommands.delete(key);
    try { if (value) sessionStorage.setItem('crv.rfx.pending.' + key, value); else sessionStorage.removeItem('crv.rfx.pending.' + key); } catch {}
  }

  const PackIcon = $derived(rfxIcon(pack.icon, Zap));
  const enabledSet = $derived(new Set(members.filter((m) => m.enabled !== false).map((m) => m.name)));
  const maxRisk = $derived(members.some((m) => m.risk === 'dangerous') ? 'dangerous'
    : members.some((m) => m.risk === 'sensitive') ? 'sensitive' : 'safe');

  async function execute(id, name, args, source, confirmed = false, sid = sessionId) {
    try {
      if (!sid || sid !== sessionId || sid !== sessionStore.activeId) throw new Error('Session changed; request the reflex again in the intended session.');
      const res = await jpost('/api/rfx/run', { session_id: sid, name, args: args ?? {}, confirmed });
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
    const sid = sessionId;
    try {
      const state = await j(`/api/sessions/${sid}/state`);
      if (sessionId !== sid) throw new Error('Session changed; refresh the panel.');
      if (!state) throw new Error('Could not load session state; refresh before starting work.');
      const list = state.events ?? [];
      const checkpoints = list.filter((e) => e.type === 'checkpoint')
        .map((e) => ({ ...(e.payload ?? {}), ts: e.ts }));
      const closes = list.filter((e) => e.type === 'turn.close');
      // the stop reason lives in the error event, not turn.close
      const errs = list.filter((e) => e.type === 'error');
      const lastErr = errs.length ? (errs[errs.length - 1].payload ?? {}) : null;
      reply(source, id, {
        ok: true,
        session: sid,
        plan: state?.plan ?? null,
        checkpoints,
        planState:state?.plan_state??null,
        run:state?.run??null,
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

  // files(): ask the core which declared paths exist in the ACTIVE
  // workspace. Same capability gate as session (both are reads).
  async function probeFiles(id, paths, source) {
    if (!pack.ui?.session) return reply(source, id, { ok: false, error: 'pack does not declare ui.session' });
    try {
      const res = await jpost('/api/files/probe', { session_id: sessionId, paths: paths ?? [] });
      reply(source, id, { ok: true, workspace: res?.workspace ?? '', files: res?.files ?? [] });
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

  // plan(): read the committed plan WITH its cursor — which step is next,
  // which is blocked, which revision each is on. Same gate as session: a read.
  async function readPlan(id, source) {
    if (!pack.ui?.session) return reply(source, id, { ok: false, error: 'pack does not declare ui.session' });
    if (!sessionId) return reply(source, id, { ok: false, error: 'no active session' });
    const sid = sessionId;
    try {
      const state = await j(`/api/sessions/${sid}/plan`);
      if (sessionId !== sid) throw new Error('Session changed; refresh the panel.');
      if (!state) throw new Error('Could not load the committed plan; refresh before starting work.');
      reply(source, id, { ok: true, ...state });
    } catch (e) {
      reply(source, id, { ok: false, error: String(e) });
    }
  }

  // runStep(): run ONE step of the committed plan and verify it.
  //
  // This replaces composing an English prompt and posting it as an ordinary
  // turn ("do step 3 only, then stop and report"), which left the core with no
  // idea a step was requested: nothing bound the run to step 3, nothing
  // verified it, and no checkpoint was written. Same ui.turn gate — it starts
  // work in the user's session either way.
  async function runStep(id, step, revision, source, steps = undefined, displayedPlanID = undefined) {
    if (!pack.ui?.turn) return reply(source, id, { ok: false, error: 'pack does not declare ui.turn' });
    if (!sessionId) return reply(source, id, { ok: false, error: 'no active session' });
    try {
      const sid=sessionId;
      const ps=await j(`/api/sessions/${sid}/plan`);
      if(!ps)throw new Error('No committed plan');
      if(sessionId!==sid)throw new Error('Session changed before acceptance; no run was started.');
      if(displayedPlanID && displayedPlanID!==ps.plan_event_id)throw new Error('Plan changed; refresh the panel before running.');
      const intent={
        kind:step==='selected'?'selected':step==='all'?'continue':'step',
        step:Number.isInteger(step)?step:-1,steps,revision:!!revision,plan_event_id:ps.plan_event_id
      };
      const key=JSON.stringify([sid,intent]);
      const commandID=uncertainCommand(key)??crypto.randomUUID();
      rememberCommand(key,commandID);
      let accepted;
      try { accepted=await jpost(`/api/sessions/${sid}/commands`,{command_id:commandID,...intent}); }
      catch(error) { if(error instanceof ApiError && error.status>=400 && error.status<500)rememberCommand(key); throw error; }
      rememberCommand(key);
      // Await terminal projection, not a fragile long-running HTTP response.
      const until=Date.now()+2*60*60*1000;
      while(Date.now()<until){
        if(sessionId!==sid)throw new Error('Session changed; the original run continues in its own session.');
        const state=await j(`/api/sessions/${sid}/state`);
        if(state?.run?.id===accepted.run.id && !state.running){
          reply(source,id,{ok:state.run.status==='completed',run:state.run,error:state.run.reason??''});return;
        }
        if(state?.run?.id && state.run.id!==accepted.run.id)throw new Error('A newer run is now displayed; inspect the original run in session history.');
        await new Promise(resolve=>setTimeout(resolve,1000));
      }
      throw new Error('Observation timed out; inspect the run before retrying.');
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
    if (m.rfx === 'plan') { readPlan(m.id, e.source); return; }
    if (m.rfx === 'runPlan') { runStep(m.id, 'all', false, e.source, undefined, m.plan_event_id); return; }
    if (m.rfx === 'runSelected') { runStep(m.id, 'selected', false, e.source, m.steps, m.plan_event_id); return; }
    if (m.rfx === 'runStep') { runStep(m.id, m.step, m.revision, e.source, undefined, m.plan_event_id); return; }
    if (m.rfx === 'files') { probeFiles(m.id, m.paths, e.source); return; }
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
      pendingDanger = { id: m.id, name: m.name, args: m.args, source: e.source, sid: sessionId };
      return;
    }
    execute(m.id, m.name, m.args, e.source);
  }

  function approveDanger() {
    const p = pendingDanger;
    pendingDanger = null;
    if (p) execute(p.id, p.name, p.args, p.source, true, p.sid);
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
  <button class="phead" aria-expanded={open} onclick={() => (open = !open)}>
    <PackIcon size={13} />
    <span class="pname">{pack.name}</span>
    <span class="pver">v{pack.version} · {members.length ? 'custom panel' : 'supervisor'}</span>
    <span class="chip" class:chip-dangerous={maxRisk === 'dangerous'} class:chip-sensitive={maxRisk === 'sensitive'}>{maxRisk}</span>
    {#if open}<ChevronUp size={13} />{:else}<ChevronDown size={13} />{/if}
  </button>

  <PackSource {pack} />
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
    border-radius: var(--r-panel); overflow: hidden;
    background: transparent;
  }
  .phead {
    display: flex; align-items: center; gap: 8px; width: 100%;
    padding: 10px 12px; border: none; cursor: pointer; background: transparent;
    color: var(--accent); text-align: left;
  }
  .pname { font-size: 13px; font-weight: 650; color: var(--text); }
  .pver { flex: 1; font-size: 9.5px; color: var(--muted); }
  .chip {
    font-size: 9px; font-weight: 600; padding: 2px 7px; border-radius: var(--r-control);
    color: var(--muted); background: transparent;
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
    font-size: 10.5px; font-weight: 600; padding: 4px 12px; border: none; border-radius: var(--r-control);
    cursor: pointer; background: var(--s3); color: var(--text);
    box-shadow: inset 0 0 0 1px var(--line);
  }
  .ds-btn.ok { background: var(--err); color: #fff; box-shadow: none; }

  .frame { display: block; width: 100%; border: none; background: transparent; }
</style>
