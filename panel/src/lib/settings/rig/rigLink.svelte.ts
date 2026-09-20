// Which module the user is dragging a link from, if any. The hardware cards
// read it to light up as somewhere it can go, or fade as somewhere it cannot —
// the answer to "where may I drop this?" before the user has to guess.
import type { Movable } from './rigGraph';

let from = $state<Movable | null>(null);
// The module whose form is open. Its card carries a ring, so the form and the
// diagram visibly talk about the same thing.
let selected = $state('');

export const rigLink = {
  get selected(): string { return selected; },
  select(id: string): void { selected = selected === id ? '' : id; },
  open(id: string): void { selected = id; },
  close(): void { selected = ''; },

  get from(): Movable | null { return from; },
  start(id: string | null): void { from = id === 'core' || id === 'embedder' ? id : null; },
  end(): void { from = null; },
};
