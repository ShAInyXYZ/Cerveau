// Session store — everything about the active session and its live turn.
// Replaces the App.svelte god-component state + the 17-prop drill into Chat.
import { api, streamEvents } from '../api';
import { toStep, errorKey } from '../steps';
import { ErrorChime } from '../errorchime';
import { storage, storageKeys } from '../storage';
import { play } from '../sound.js';
import { healthStore } from './health.svelte.ts';
import type {
  ChatMessage, EpisodicEvent, LiveStep, Mode, PlanReport, Question,
  SessionError, SessionMeta, WindowReport,
} from '../types';

const TICK_MS = 2000;
const TICK_KEEP = 200;
const ERROR_CARDS = 3;

let sessions = $state<SessionMeta[]>([]);
// sessions with a turn executing now — CLI runs included, so a build started
// outside the panel is visibly running instead of looking idle.
let runningIds = $state<string[]>([]);
// A ONE-TURN sampling override. It clears after the turn it applies to, so a
// deliberate choice for one message never silently becomes the new normal.
let turnSampling = $state<string>('');
let activeId = $state<string | null>(null);
let messages = $state<ChatMessage[]>([]);
let ticks = $state<EpisodicEvent[]>([]);
let lastEvents = $state<Record<string, EpisodicEvent>>({});
let running = $state(false);
let runStarted = $state<number | null>(null);
let windowReport = $state<WindowReport | null>(null);
let question = $state<Question | null>(null);
let errors = $state<SessionError[]>([]);
let report = $state<PlanReport | null>(null);
let skills = $state<unknown[]>([]);
// Autopilot is the default: Cerveau is a build harness first, a chat second.
// The last choice is remembered per browser, so a reload does not reset it.
const MODE_KEY = 'crv.mode';
function initialMode(): Mode {
  try {
    const m = localStorage.getItem(MODE_KEY);
    if (m === 'discussion' || m === 'autopilot' || m === 'brainstorming') return m;
  } catch { /* no storage */ }
  return 'autopilot';
}
let mode = $state<Mode>(initialMode());
let liveSteps = $state<LiveStep[]>([]);

let dismissed = new Set<string>();
let closeStream: (() => void) | null = null;
let timer: ReturnType<typeof setInterval> | null = null;

async function loadMessages(): Promise<void> {
  if (!activeId) return;
  const d = await api.sessionState(activeId);
  // never blank the chat on a transient failed fetch
  if (d && Array.isArray(d.messages)) messages = d.messages;
}

async function loadTicks(): Promise<void> {
  if (!activeId) return;
  const evts = await api.events(activeId);
  ticks = evts.slice(-TICK_KEEP);
  if (evts.length) lastEvents = { ...lastEvents, [activeId]: evts[evts.length - 1] };
}

async function loadQuestion(): Promise<void> {
  // poll unconditionally: a question can be pending server-side even when this
  // tab didn't start the turn (reload, second tab, parked ask_user)
  if (!activeId) return;
  const d = await api.question(activeId);
  const next = d?.question ? d : null;
  if (next && !question) play('ask');
  question = next;
}

// Play on HAPPENING, not on display. Counting errors meant every session
// switch (which empties the list) re-announced whatever was already there, so
// opening a session that ended on a guard stop replayed its error out loud.
const chime = new ErrorChime();

async function loadErrors(): Promise<void> {
  if (!activeId) return;
  const all = await api.errors(activeId);
  // transient cards are auto-retry chatter the user cannot act on
  const kept = all
    .map((e, i) => ({ e, key: errorKey(e, i) }))
    .filter(({ e, key }) => e.class !== 'transient' && !dismissed.has(key))
    .slice(-ERROR_CARDS);
  errors = kept.map(({ e }) => e);
  if (chime.shouldPlay(kept.map(({ key }) => key))) play('error');
}

async function loadReport(): Promise<void> {
  if (!activeId) return;
  // Keep the last good report. The fetch helper returns null for ANY non-200,
  // and /report 404s until a plan event exists — the first seconds of every
  // turn. Assigning that null erased the plan the strip was waiting for, over
  // and over, so the strip could never appear mid-run. `report` is cleared
  // explicitly on session switch, which is the only place it should go blank.
  const next = await api.report(activeId);
  if (next) report = next;
}

/** The workspace follows the SESSION: selecting a session in another project
 *  re-points the core (registry, guard, code graph, RFX) at its folder. */
async function followSessionWorkspace(id: string): Promise<void> {
  const ws = sessions.find((x) => x.id === id)?.workspace;
  if (!ws || ws === healthStore.workspace) return;
  const r = await api.setWorkspace(ws);
  if (r?.ok) await healthStore.refresh();
}

export const sessionStore = {
  get sessions() { return sessions; },
  get activeId() { return activeId; },
  get activeIsInstant() { return sessions.find((s) => s.id === activeId)?.instant === true; },
  get messages() { return messages; },
  get ticks() { return ticks; },
  get lastEvents() { return lastEvents; },
  // running = this tab started a turn, OR the server says one is executing in
  // the active session. Without the second half a CLI build renders as a dead
  // screen: the working log is gated on this flag, and the assistant messages
  // during a build are empty (the model is calling tools, not talking), so
  // there is literally nothing on screen while the machine works.
  get running() { return running || runningIds.includes(activeId ?? ''); },
  get runStarted() { return runStarted; },
  get windowReport() { return windowReport; },
  get question() { return question; },
  get errors() { return errors; },
  get report() { return report; },
  get skills() { return skills; },
  get runningIds() { return runningIds; },
  get turnSampling() { return turnSampling; },
  set turnSampling(v: string) { turnSampling = v; },
  // liveSteps is fed by the SSE stream, which only exists for turns this tab
  // started. For a CLI run, derive the same steps from the episodic ticks we
  // already poll — otherwise the working log is empty for the whole build.
  get liveSteps() {
    if (liveSteps.length) return liveSteps;
    if (!runningIds.includes(activeId ?? '')) return liveSteps;
    const out: LiveStep[] = [];
    for (const ev of ticks) {
      const st = toStep(ev as EpisodicEvent);
      if (st) out.push(st);
    }
    return out;
  },
  get mode() { return mode; },
  set mode(m: Mode) { mode = m; try { localStorage.setItem(MODE_KEY, m); } catch { /* no storage */ } },

  async loadSessions(): Promise<void> {
    sessions = await api.sessions();
    runningIds = await api.runningSessions();
    if (!activeId && sessions.length) this.select(sessions[0].id);
  },

  async loadSkills(): Promise<void> {
    skills = await api.skills();
  },

  select(id: string): void {
    activeId = id;
    messages = []; errors = []; question = null; report = null;
    chime.reset();   // a session opened for the first time must be silent
    dismissed = new Set(storage.get<string[]>(storageKeys.dismissedErrors(id), []));
    void loadMessages(); void loadTicks(); void loadErrors(); void loadReport();
    void followSessionWorkspace(id);
  },

  /**
   * Edit a user message: drop it and everything after it, then send the new
   * text as a fresh turn.
   *
   * The log is append-only, so an edit cannot be a patch — every turn after
   * that message was a response to the ORIGINAL, and keeping both would leave
   * the model reading a question and a correction with no way to know which
   * one counts.
   */
  async editAndResend(eventId: string, text: string): Promise<void> {
    if (!activeId || running || !text.trim()) return;
    try {
      await api.rewind(activeId, eventId);
    } catch {
      return;             // refused (a turn is running) — leave the log alone
    }
    await loadMessages();
    await this.send(text);
  },

  async send(text: string, opts: { step?: boolean } = {}): Promise<void> {
    if (!activeId || running) return;
    const sid = activeId;
    running = true; runStarted = Date.now();
    liveSteps = []; errors = [];
    // optimistic echo of the user's message
    messages = [...messages, {
      type: 'msg.user', ts: new Date().toISOString(), payload: { text }, _optimistic: true,
    }];

    // SSE drives LIVE STEPS only; authoritative messages come from /state below
    closeStream?.();
    const seen = new Set(messages.map((m) => m.id).filter(Boolean));
    let sawNew = false;
    closeStream = streamEvents(sid, (ev) => {
      if (sid !== activeId || seen.has(ev.id)) return;
      seen.add(ev.id);
      if (ev.type === 'msg.user' && (ev.payload as { text?: string })?.text === text) {
        sawNew = true; return;
      }
      if (!sawNew) return;
      if (ev.type === 'msg.assistant') {
        void loadMessages();
        liveSteps = [];
      } else {
        const st = toStep(ev);
        if (st) liveSteps = [...liveSteps, st];
      }
    });

    const used = turnSampling;
    turnSampling = '';   // one turn only
    const res = await api.chat(sid, text, mode, !!opts.step, used || undefined);
    running = false; runStarted = null;
    closeStream?.(); closeStream = null;
    liveSteps = [];
    if (res?.window) windowReport = res.window;
    await Promise.all([loadMessages(), loadErrors(), loadReport()]);
    const stop = res?.stop_reason;
    if (!errors.length && (!stop || stop === 'final_answer')) play('done');
  },

  /** An RFX panel asking for a turn — identical to the user typing it. */
  async panelTurn(text: string, m?: Mode): Promise<boolean> {
    if (!activeId || running) return false;
    if (m && m !== mode) mode = m;
    await this.send(text, { step: true });
    return true;
  },

  async steer(text: string): Promise<void> {
    if (!activeId) return;
    await api.steer(activeId, text);
    void loadTicks();
  },
  async pause(): Promise<void> { if (activeId) await api.pause(activeId); },
  async kill(): Promise<void> {
    if (!activeId) return;
    await api.kill(activeId);
    running = false; runStarted = null;
  },
  async answer(ans: string): Promise<void> {
    if (!activeId) return;
    await api.answer(activeId, ans);
    question = null;
  },
  runAutopilot(): void {
    if (!activeId || running) return;
    const sid = activeId;
    running = true; runStarted = Date.now();
    void api.autopilot(sid).then(() => {
      running = false; runStarted = null;
      void Promise.all([loadMessages(), loadTicks(), loadReport(), loadErrors()]);
    });
  },

  async dismissAllErrors(): Promise<void> {
    if (!activeId) return;
    const all = await api.errors(activeId);
    dismissed = new Set(all.map((e, i) => errorKey(e, i)));
    storage.set(storageKeys.dismissedErrors(activeId), [...dismissed]);
    errors = [];
  },
  async retry(text: string): Promise<void> {
    await this.dismissAllErrors();
    await this.send(text);
  },

  async create(name: string, workspace?: string): Promise<void> {
    const m = await api.createSession(name, workspace);
    await this.loadSessions();
    if (m?.id) { play('notify'); this.select(m.id); }
  },
  async createInstant(): Promise<void> {
    const m = await api.createInstant();
    await this.loadSessions();
    if (m?.id) { play('notify'); this.select(m.id); }
  },
  async rename(id: string, name: string): Promise<void> {
    if (await api.renameSession(id, name)) { play('confirm'); await this.loadSessions(); }
  },
  async remove(id: string, mode: string, confirm: string): Promise<boolean> {
    const ok = await api.deleteSession(id, mode, confirm);
    if (ok) {
      play('confirm');
      if (activeId === id) { activeId = null; messages = []; }
      await this.loadSessions();
    }
    return ok;
  },

  /** WS chip picked a folder: a project pill only exists once the workspace
   *  has a session, so create + select one now. */
  async onWorkspaceChanged(ws: string): Promise<void> {
    const name = ws.replace(/[/\\]+$/, '').split(/[/\\]/).pop() || 'session';
    await this.create(name, ws);
    await healthStore.refresh();
  },

  start(): void {
    if (timer) return;
    void this.loadSessions(); void this.loadSkills();
    timer = setInterval(() => {
      void loadTicks(); void loadQuestion(); void loadErrors();
      // keep the running set fresh: a CLI turn can start or finish at any
      // moment and the panel has no other way to learn about it.
      void api.runningSessions().then((ids) => { runningIds = ids; }).catch(() => {});
      // Poll while a turn is RUNNING too, not only once a report exists.
      // `if (report)` refreshed a report we already had and never fetched the
      // first one, so the plan strip could not appear until the turn ended —
      // and by then the plan was complete, so it archived itself on arrival.
      // The strip is a progress indicator; it has to show up during the work.
      // The RFX planner fetches independently, which is why it filled in at
      // the start while the chat strip stayed empty (2026-09-04, fan).
      if (report || running) void loadReport();
    }, TICK_MS);
  },
  stop(): void {
    if (timer) { clearInterval(timer); timer = null; }
    closeStream?.(); closeStream = null;
  },
};
