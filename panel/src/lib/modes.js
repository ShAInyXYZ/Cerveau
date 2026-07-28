import { MessageCircle, FlaskConical, Gauge } from 'lucide-svelte';

// The three operating modes, with the presentation metadata the UI needs.
export const MODES = [
  {
    value: 'discussion',
    title: 'Discussion',
    desc: 'Quick, concise planning dialogue',
    icon: MessageCircle,
    color: 'var(--info)'      // blue — talk / plan
  },
  {
    value: 'brainstorming',
    title: 'Brainstorming',
    desc: 'Deep research, long-form thinking',
    icon: FlaskConical,
    color: 'var(--warn)'      // gold — research / heat
  },
  {
    value: 'autopilot',
    title: 'Autopilot',
    desc: 'Execute a committed plan, step by step',
    icon: Gauge,
    color: 'var(--ok)'        // green — go / execute
  }
];

export function modeMeta(value) {
  return MODES.find((m) => m.value === value) ?? MODES[0];
}
