// API contract types — mirror internal/api + internal/loop payloads.
// One place to update when the Go side changes shape.

export interface HealthComponent {
  name: string;
  url?: string;
  ok: boolean;
  info?: string;
}

export interface Health {
  components: HealthComponent[];
  model?: { name?: string; modalities?: Modalities };
  modes?: string[];
  system?: { version?: string; model_ctx?: number; uptime?: number };
  workspace?: string;
}

// ── Rig (GET /api/rig) — mirrors internal/rig ──
/** What a card IS. Live temperature and load are in SysStats, not here. */
export interface RigGPU {
  index: number; uuid: string; name: string;
  mem_total: number; // MiB
  pcie_gen?: number; pcie_width?: number; power_limit?: number; compute_cap?: string;
}
export interface RigInventory {
  gpus: RigGPU[] | null; // null on a machine without nvidia-smi
  links?: string[][];
  nvlink: boolean;
}
export type RigRole = 'core' | 'embedder' | 'other';
export interface RigConsumer { pid: number; mem: number; name: string; unit?: string; role: RigRole; core?: string }
/** One card's memory right now. `used` is everything the driver counts; the
 *  consumers are only compute processes, so the two need not add up. */
export interface RigCardUse { gpu: number; used: number; consumers: RigConsumer[] }
/** Where the active Core sits on its NEXT start: profile defaults + overrides. */
export interface RigPlacement {
  core: string; name: string; engine: string; model?: string;
  gpus: number[] | null; // null: the profile does not say
  tp?: number; gpu_util?: number; kv?: string; max_len?: number;
  embed: { device: string; gpus?: number[] };
  editable: boolean; overridden: boolean; order_pinned: boolean;
  /** everything the profile runs with: defaults + the user's overrides */
  params: Record<string, string> | null;
  /** the Core the harness is talking to; any other profile is a preview */
  live: boolean;
}
export interface RigProfile { id: string; name: string; engine: string; model?: string; live: boolean; editable: boolean }
export interface Rig {
  inventory: RigInventory; observed: RigCardUse[] | null;
  next: RigPlacement | null;      // the profile asked for, or the live one
  profiles?: RigProfile[] | null; // every profile this machine knows
}
/** One card of a fit estimate, MiB. */
export interface RigCardFit { gpu: number; total: number; busy: number; budget: number; weights: number; overhead: number; kv_room: number; kv_needed: number }
export interface RigFit {
  verdict: 'fits' | 'tight' | 'no' | 'unknown';
  reasons: string[];
  cards: RigCardFit[];
  window: number; max_window: number;
  group_sizes: number[]; // how many cards this model can be split across
  remote: boolean;
}
export interface RigSavedLayout { name: string; gpus: number[]; embed: { device: string; gpus?: number[] }; gpu_util?: number }
/** POST /api/rig/plan — what saving a drawn layout would write. The override
 *  sets are COMPLETE: PUT /params replaces the file, so they are sent as given. */
export interface RigPlan {
  /** the profile this plan was made for — it is only ever saved to that one */
  core: string;
  overrides: Record<string, string>;
  embed_overrides: Record<string, string>;
  changed: string[];
  problems: string[];  // block a save
  warnings: string[];
  fit?: RigFit | null;
  gpus: number[]; gpu_util: number; kv: string; window: number;
}

// ── live hardware (GET /api/system/stats) ──
export interface SysGPU {
  index: number; name: string; temp: number; util: number;
  mem_used: number; mem_total: number; power: number; power_max: number; fan: number;
}
export interface SysStats {
  gpus?: SysGPU[] | null;
  gpu?: SysGPU | null;
  cpu?: { name: string; cores: number; temp: number; util: number };
  ram?: { used: number; total: number; type?: string; speed?: string; vendor?: string; sticks?: number };
}

export interface Modalities {
  text: boolean;
  vision?: boolean;
  audio?: boolean;
  video?: boolean;
}

export interface SessionMeta {
  id: string;
  name?: string;
  workspace?: string;
  instant?: boolean;
}

export interface ChatMessage {
  id?: string;
  type: 'msg.user' | 'msg.assistant';
  ts: string;
  payload?: { text?: string; tool_calls?: unknown[]; images?: { data_url: string; width?: number; height?: number; sha256?: string }[] };
  _optimistic?: boolean;
}

export interface EpisodicEvent {
  id: string;
  type: string;
  ts: string;
  payload?: Record<string, unknown>;
}

export interface SessionError {
 id?: string; run_id?: string;
  class?: string;
  what?: string;
  why?: string;
  detail?: string;
  stop?: string;
  tried?: string;
}

export interface StepReport {
  title: string;
  status: string;
  summary?: string;
  ts?: string;
}

export interface PlanReport {
  title: string;
  plan_event_id?: string;
  steps: StepReport[];
  done: number;
  failed: number;
  skipped: number;
  needs_reverify?: number;
  pending?: number;
  unverified?: number;
  running?: number;
  handback?: boolean;
  finished_at?: string;
}

export interface Question {
 id?: string;
 run_id?: string;
  question: string;
  options?: string[];
}

export interface WindowReport {
  tokens: number;
  budget: number;
  zone?: string;
  demoted?: number;
  trimmed?: number;
}

export interface ChatResult {
  reply?: string;
  stop_reason?: string;
  window?: WindowReport;
}

/** A live step card in the working block, derived from episodic events. */
export interface LiveStep {
  id: string;
  kind: 'tool' | 'result' | 'note' | 'error' | 'abort';
  name?: string;
  arg?: string;
  status?: 'run' | 'ok' | 'fail';
  output?: string;
  text?: string;
  /** the note's own kind, e.g. 'context_compacted' */
  noteKind?: string;
}

export type Mode = 'discussion' | 'autopilot' | 'brainstorming';

export interface RunState {
 kind?: string; reflex?: string;
 control_version: number;
 result?: ChatResult;
 id: string; status: string; phase?: string; tool?: string; step: number;
 recovery_phase?: string;
 started: string; updated: string; reason?: string; calls: number;
 thinking_mode: string; thinking_effort: string; sampling: string;
}
export interface SessionSnapshot {
 messages?: ChatMessage[]; logs?: Record<string, EpisodicEvent[]>;
 run?: RunState | null; plan_state?: PlanState | null; running?: boolean;
 cursor?: string; events?: EpisodicEvent[];
 question?: Question | null; errors?: SessionError[]; report?: PlanReport | null;
}
export interface PlanState {
 plan_event_id: string; title: string; next: number; blocked: number; done: boolean;
 steps: {id: string; title: string; status: string; rev: number; attempts: number; reason?: string; verdict?: {pass: boolean; evidence?: string; check?: string; workspace_version?: string; evidence_event_id?: string; execution_stop?: string}}[];
}
