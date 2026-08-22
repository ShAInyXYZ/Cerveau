// UI store — which surfaces are open. One truth for view switching, the
// activity drawer, and the mobile rail drawer.
import { storage } from '../storage';

type View = 'chat' | 'memory' | 'settings';

let view = $state<View>('chat');
let activityOpen = $state(false);
// Two different questions with two different defaults:
//
//   railOpen      is the MOBILE drawer slid over the content — closed by
//                 default, because a drawer covering the chat on load would be
//                 in the way.
//   railCollapsed is the DESKTOP rail folded away to reclaim its width —
//                 expanded by default, because the session tree is the primary
//                 navigation and hiding it on first run hides the app.
//
// One flag could not serve both: false means "hidden" for one and "shown" for
// the other.
let railOpen = $state(false);
let railCollapsed = $state(storage.get<boolean>('rail.collapsed', false));

export const uiStore = {
  get view() { return view; },
  get activityOpen() { return activityOpen; },
  set activityOpen(v: boolean) { activityOpen = v; },
  get railOpen() { return railOpen; },
  set railOpen(v: boolean) { railOpen = v; },
  get railCollapsed() { return railCollapsed; },

  toggleMemory(): void { view = view === 'memory' ? 'chat' : 'memory'; },
  toggleSettings(): void { view = view === 'settings' ? 'chat' : 'settings'; },
  showChat(): void { view = 'chat'; },
  toggleRail(): void { railOpen = !railOpen; },
  closeRail(): void { railOpen = false; },
  // Persisted: a collapsed rail is a working preference, and restoring the
  // tree on every reload would undo the choice each time.
  toggleRailCollapsed(): void {
    railCollapsed = !railCollapsed;
    storage.set('rail.collapsed', railCollapsed);
  },
};
