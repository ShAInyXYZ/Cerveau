<script>
  import './tokens.css';
  import { j, jpost, fetchEvents, streamEvents } from './lib/api.js';
  import { play } from './lib/sound.js';
  import StatusBar from './lib/StatusBar.svelte';
  import WorkspaceRail from './lib/WorkspaceRail.svelte';
  import Chat from './lib/Chat.svelte';
  import Activity from './lib/Activity.svelte';
  import MemoryView from './lib/MemoryView.svelte';
  import RfxDock from './lib/RfxDock.svelte';
  import Settings from './lib/Settings.svelte';
  import DeleteSessionDialog from './lib/DeleteSessionDialog.svelte';

  let health = $state(null);
  let sessions = $state([]);
  let activeId = $state(null);
  const activeInstant = $derived(sessions.find((s) => s.id === activeId)?.instant === true);
  let messages = $state([]);
  let ticks = $state([]);
  let lastEvents = $state({});
  let running = $state(false);
  let runStarted = $state(null);
  let windowReport = $state(null);
  let question = $state(null);
  let errors = $state([]);
  let report = $state(null);
  let skills = $state([]);
  let mode = $state('discussion');
  let memView = $state(false);
  let settingsView = $state(false);
  let activityOpen = $state(false);   // hidden by default
  let liveSteps = $state([]);         // in-flight tool/thinking steps for the current turn
  let closeStream = null;

  async function loadHealth() { health = await j('/api/health'); }
  async function loadSessions() {
    const d = await j('/api/sessions');
    sessions = d?.sessions ?? [];
    if (!activeId && sessions.length) select(sessions[0].id);
  }
  async function loadMessages() {
    if (!activeId) return;
    const d = await j(`/api/sessions/${activeId}/state`);
    // don't blank the chat on a transient failed fetch — keep what we have
    if (d && Array.isArray(d.messages)) messages = d.messages;
  }
  async function loadTicks() {
    if (!activeId) return;
    const evts = await fetchEvents(`/api/sessions/${activeId}/events`);
    ticks = evts.slice(-200);
    if (evts.length) lastEvents = { ...lastEvents, [activeId]: evts[evts.length - 1] };
  }
  async function loadQuestion() {
    // Don't gate on `running`: a question can be pending server-side even when
    // THIS tab didn't start the turn (page reload, second tab, turn parked on
    // ask_user while the chat POST is still blocked). Poll it unconditionally so
    // the card always shows when the loop is waiting on the user.
    if (!activeId) return;
    const d = await j(`/api/sessions/${activeId}/question`);
    const next = d?.question ? d : null;
    if (next && !question) play('ask'); // chime once, when a question first appears
    question = next;
  }
  // Errors the user has dismissed, tracked by a STABLE signature (not a count) —
  // PER SESSION and PERSISTED to localStorage, so a dismissal survives switching
  // sessions and page reloads (it used to reset on every session switch, so the
  // error came back). Keyed by session id.
  let dismissedErrorKeys = $state(new Set());
  function dismissStoreKey(id) { return `crv:dismissed:${id}`; }
  function loadDismissed(id) {
    try {
      const raw = localStorage.getItem(dismissStoreKey(id));
      return new Set(raw ? JSON.parse(raw) : []);
    } catch { return new Set(); }
  }
  function saveDismissed(id, set) {
    try { localStorage.setItem(dismissStoreKey(id), JSON.stringify([...set])); } catch {}
  }
  function errKey(e, i) {
    // content signature: what happened + why + class. Falls back to index so two
    // identical-looking errors are still distinguishable.
    return `${e.class ?? ''}|${e.what ?? e.stop ?? ''}|${e.why ?? e.detail ?? ''}|${i}`;
  }
  async function loadErrors() {
    if (!activeId) return;
    const d = await j(`/api/sessions/${activeId}/errors`);
    const all = d?.errors ?? [];
    // 'transient' cards are auto-retry chatter ("model call failed — retrying") the
    // user can't act on — they stay in Activity but don't belong in the chat. If the
    // endpoint stays down, the run ends on a non-transient stop that DOES surface.
    const prevCount = errors.length;
    errors = all
      .map((e, i) => ({ e, key: errKey(e, i) }))
      .filter(({ e, key }) => e.class !== 'transient' && !dismissedErrorKeys.has(key))
      .slice(-3)
      .map(({ e }) => e);
    if (errors.length > prevCount) play('error'); // a new error card just surfaced
  }
  async function dismissAllErrors() {
    if (!activeId) return;
    const d = await j(`/api/sessions/${activeId}/errors`);
    const all = d?.errors ?? [];
    dismissedErrorKeys = new Set(all.map((e, i) => errKey(e, i)));
    saveDismissed(activeId, dismissedErrorKeys); // persist so it sticks across switches/reloads
    errors = [];
  }
  async function loadReport() {
    if (!activeId) return;
    const r = await fetch(`/api/sessions/${activeId}/report`);
    report = r.ok ? await r.json() : null;
  }
  async function loadSkills() {
    const d = await j('/api/skills');
    skills = d?.skills ?? [];
  }
  // rename a session's display title (id stays fixed — pure label change)
  async function renameSession(id, name) {
    const r = await fetch(`/api/sessions/${id}`, {
      method: 'PATCH',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ name })
    });
    if (r.ok) { play('confirm'); await loadSessions(); }
  }

  // --- delete session flow (dialog with type-to-confirm + keep-memory tiers) ---
  let deleteTarget = $state(null);   // the session being deleted
  let deletePreview = $state(null);  // { name, events, episodic, semantic, code }
  async function openDelete(s) {
    deleteTarget = s;
    deletePreview = await j(`/api/sessions/${s.id}/delete-preview`);
  }
  function closeDelete() { deleteTarget = null; deletePreview = null; }
  async function confirmDelete({ mode, confirm }) {
    const id = deleteTarget.id;
    const r = await fetch(`/api/sessions/${id}`, {
      method: 'DELETE',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ mode, confirm })
    });
    if (r.ok) {
      play('confirm');
      closeDelete();
      if (activeId === id) { activeId = null; messages = []; }
      await loadSessions();
    }
  }

  function select(id) {
    activeId = id;
    messages = []; errors = []; question = null; report = null;
    dismissedErrorKeys = loadDismissed(id); // this session's own remembered dismissals
    loadMessages(); loadTicks(); loadErrors(); loadReport();
    followSessionWorkspace(id);
  }

  // The workspace follows the SESSION, not the other way around: selecting a
  // session in another project re-points the core (registry, guard, code
  // graph, RFX runs) at that session's folder, and the WS chip + RFX dock
  // update with it. Without this, the dock kept showing the previous
  // project's git state after a session switch.
  async function followSessionWorkspace(id) {
    const s = sessions.find((x) => x.id === id);
    const ws = s?.workspace;
    if (!ws || ws === health?.workspace) return;
    const r = await jpost('/api/config/workspace', { path: ws });
    if (r?.ok) await loadHealth();
  }

  async function createSession(name, workspace) {
    const m = await jpost('/api/sessions', { name, workspace });
    await loadSessions();
    if (m?.id) { play('notify'); select(m.id); }
  }
  // start an ephemeral Instant session (no memory, auto-deletes in 24h)
  async function createInstant() {
    const m = await jpost('/api/sessions/instant', {});
    await loadSessions();
    if (m?.id) { play('notify'); select(m.id); }
  }

  // Picking a workspace via the WS chip: the server has already re-pointed the
  // default workspace (code graph / guard / registry rebuilt). But a project pill
  // only exists once the workspace HAS a session, so create + switch to one now —
  // otherwise the pick looks like it did nothing.
  async function onWorkspaceChanged(ws) {
    const name = folderName(ws) || 'session';
    await createSession(name, ws);   // stamps the session with ws, selects it
    await loadHealth();              // refresh the WS chip
  }
  function folderName(p) {
    if (!p) return '';
    return p.replace(/[/\\]+$/, '').split(/[/\\]/).pop() || '';
  }

  // Pull the human-meaningful argument out of a tool call so the chat can show
  // WHAT the agent is doing (the command, the file), not just the tool name.
  function callSummary(name, args) {
    const a = args ?? {};
    switch (name) {
      case 'bash':            return a.command ?? '';
      case 'read':
      case 'write':
      case 'edit':
      case 'outline_file':    return a.path ?? '';
      case 'grep':            return a.pattern ?? a.query ?? '';
      case 'find_symbol':
      case 'find_references': return a.name ?? '';
      case 'web_fetch':       return a.url ?? '';
      case 'remember':        return a.content ?? '';
      case 'ask_user':        return a.question ?? '';
      default: {
        // fall back to the first string arg
        const v = Object.values(a).find((x) => typeof x === 'string');
        return v ?? '';
      }
    }
  }

  // turn a raw episodic event into a live tool step (a card in the working block)
  function toStep(ev) {
    const p = ev.payload ?? {};
    switch (ev.type) {
      case 'tool.call':
        return { id: ev.id, kind: 'tool', name: p.name, arg: callSummary(p.name, p.args), status: 'run' };
      case 'tool.result':
        return { id: ev.id, kind: 'result', name: p.name, ok: p.ok !== false, output: typeof p.output === 'string' ? p.output : '' };
      case 'note':    return p.text ? { id: ev.id, kind: 'note', text: p.text } : null;
      case 'error':   return { id: ev.id, kind: 'error', text: p.what || p.detail || 'error' };
      case 'aborted': return { id: ev.id, kind: 'abort', text: p.reason || 'aborted' };
      default:        return null;
    }
  }


  async function send(text) {
    if (!activeId || running) return;
    const sid = activeId;
    running = true; runStarted = Date.now();
    liveSteps = [];
    errors = [];
    // 1) show the user's message immediately (optimistic)
    messages = [...messages, { type: 'msg.user', ts: new Date().toISOString(), payload: { text }, _optimistic: true }];

    // 2) SSE drives LIVE STEPS only (tool calls / notes / results as they happen).
    //    The messages themselves come from the authoritative /state reload below,
    //    so a fast turn or replay timing can never drop the answer.
    closeStream?.();
    const seen = new Set(messages.map((m) => m.id).filter(Boolean));
    let sawNew = false;
    closeStream = streamEvents(sid, (ev) => {
      if (sid !== activeId) return;
      if (seen.has(ev.id)) return; seen.add(ev.id);
      // once we see our just-sent user message in the stream, live steps are "current"
      if (ev.type === 'msg.user' && ev.payload?.text === text) { sawNew = true; return; }
      if (!sawNew) return;
      if (ev.type === 'msg.assistant') {
        // answer landed — reflect it right away, clear the working block
        loadMessages();
        liveSteps = [];
      } else {
        const st = toStep(ev);
        if (st) liveSteps = [...liveSteps, st];
      }
    });

    // 3) fire the turn
    const res = await jpost(`/api/sessions/${sid}/chat`, { text, mode });
    running = false; runStarted = null;
    closeStream?.(); closeStream = null;
    liveSteps = [];
    if (res?.window) windowReport = res.window;
    // authoritative refresh — the real messages, replacing the optimistic one
    await Promise.all([loadMessages(), loadErrors(), loadReport()]);
    // audio cue: a clean finish chimes 'done'; loadErrors() above already plays
    // 'error' if the turn surfaced one, so only chime on a real answer.
    const stop = res?.StopReason ?? res?.stop_reason;
    if (!errors.length && (!stop || stop === 'final_answer')) play('done');
  }

  async function steer(text) { await jpost(`/api/sessions/${activeId}/steer`, { text }); loadTicks(); }
  async function pause() { await jpost(`/api/sessions/${activeId}/pause`, {}); }
  async function kill() { await jpost(`/api/sessions/${activeId}/kill`, {}); running = false; runStarted = null; }
  async function answer(ans) { await jpost(`/api/sessions/${activeId}/answer`, { answer: ans }); question = null; }
  function runAutopilot() {
    if (!activeId || running) return;
    running = true; runStarted = Date.now();
    jpost(`/api/sessions/${activeId}/autopilot`, {}).then(() => {
      running = false; runStarted = null;
      Promise.all([loadMessages(), loadTicks(), loadReport(), loadErrors()]);
    });
  }

  $effect(() => {
    loadHealth(); loadSessions(); loadSkills();
    const h = setInterval(loadHealth, 5000);
    const t = setInterval(() => {
      loadTicks(); loadQuestion();
      if (report) loadReport();
      loadErrors();
    }, 2000);
    return () => { clearInterval(h); clearInterval(t); };
  });
</script>

<div class="shell">
  <StatusBar {health} {windowReport} bind:activityOpen
    memView={memView} onToggleMemory={() => { memView = !memView; settingsView = false; }} />

  <div class="body">
    <WorkspaceRail {sessions} {activeId} {lastEvents} {skills}
      activeWorkspace={health?.workspace ?? ''}
      onSelect={select} onCreate={createSession} onRename={renameSession} onDelete={openDelete}
      onInstant={createInstant}
      memView={memView} onToggleMemory={() => { memView = !memView; settingsView = false; }}
      onSettings={() => { settingsView = !settingsView; memView = false; }} settingsOpen={settingsView} />

    {#if settingsView}
      <Settings />
    {:else if memView}
      <MemoryView />
    {:else}
      <div class="chatwrap">
        <Chat
          {messages} {running} {runStarted} bind:mode {question} {errors} {report} {liveSteps}
          workspace={health?.workspace ?? ''}
          modalities={health?.model?.modalities ?? null}
          isInstant={activeInstant}
          onSend={send} onSteer={steer} onPause={pause} onKill={kill}
          onAnswer={answer} onRunAutopilot={runAutopilot}
          onRetry={async (text) => { await dismissAllErrors(); send(text); }}
          onDismissError={dismissAllErrors}
          onAttach={(kind) => console.log('attach requested:', kind)}
          onWorkspaceChanged={onWorkspaceChanged} />
        {#key health?.workspace}
          <RfxDock />
        {/key}
      </div>
    {/if}

    {#if activityOpen}
      <Activity {ticks} {running} onClose={() => (activityOpen = false)} />
    {/if}
  </div>
</div>

{#if deleteTarget && deletePreview}
  <DeleteSessionDialog session={deleteTarget} preview={deletePreview}
    onConfirm={confirmDelete} onClose={closeDelete} />
{/if}

<style>
  .shell { height: 100vh; display: flex; flex-direction: column; background: var(--bg); }
  .chatwrap { display: flex; flex: 1; min-width: 0; min-height: 0; }
  .chatwrap :global(main.chat) { flex: 1; min-width: 0; }
  .body { flex: 1; display: flex; min-height: 0; gap: 1px; background: var(--line); }
  .body > :global(*) { background: var(--bg); }
</style>
