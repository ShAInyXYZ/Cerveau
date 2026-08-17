// UI store — which surfaces are open. One truth for view switching, the
// activity drawer, and the mobile rail drawer.
type View = 'chat' | 'memory' | 'settings';

let view = $state<View>('chat');
let activityOpen = $state(false);
let railOpen = $state(false); // mobile drawer (< 900px); rail is static on desktop

export const uiStore = {
  get view() { return view; },
  get activityOpen() { return activityOpen; },
  set activityOpen(v: boolean) { activityOpen = v; },
  get railOpen() { return railOpen; },
  set railOpen(v: boolean) { railOpen = v; },

  toggleMemory(): void { view = view === 'memory' ? 'chat' : 'memory'; },
  toggleSettings(): void { view = view === 'settings' ? 'chat' : 'settings'; },
  showChat(): void { view = 'chat'; },
  toggleRail(): void { railOpen = !railOpen; },
  closeRail(): void { railOpen = false; },
};
