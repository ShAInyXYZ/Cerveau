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
  payload?: { text?: string };
  _optimistic?: boolean;
}

export interface EpisodicEvent {
  id: string;
  type: string;
  ts: string;
  payload?: Record<string, unknown>;
}

export interface SessionError {
  class?: string;
  what?: string;
  why?: string;
  detail?: string;
  stop?: string;
  tried?: string;
}

export interface StepReport {
  title: string;
  status: 'pending' | 'partial' | 'done' | 'failed' | 'skipped';
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
  handback?: boolean;
  finished_at?: string;
}

export interface Question {
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
}

export type Mode = 'discussion' | 'autopilot' | 'brainstorming';
