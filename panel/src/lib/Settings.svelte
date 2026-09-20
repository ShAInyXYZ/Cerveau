<script lang="ts">
  // Settings — a shell: a row of sections, one on screen at a time. Each
  // section is its own file under settings/ and owns its own loading; this
  // file knows only which one is open. Ordered by what people come here for.
  import './settings/settings.css';
  import { Server, BrainCircuit, SlidersHorizontal, Zap, Volume2 } from 'lucide-svelte';
  import { storage, storageKeys } from './storage';
  import RigView from './settings/rig/RigView.svelte';
  import EngineSection from './settings/EngineSection.svelte';
  import GenerationSection from './settings/GenerationSection.svelte';
  import TalentsSection from './settings/TalentsSection.svelte';
  import SoundSection from './settings/SoundSection.svelte';

  // `what` is the one line that says what lives in a section — shown beside
  // the open tab, so the row answers "where am I" without a second heading.
  const SECTIONS = [
    { id: 'rig',        label: 'Rig',        icon: Server,            what: 'the machine, and where each thing runs on it',  view: RigView,           wide: true },
    { id: 'engine',     label: 'Engine',     icon: BrainCircuit,      what: 'which Brain Core answers, and its parameters',  view: EngineSection,     wide: false },
    { id: 'generation', label: 'Generation', icon: SlidersHorizontal, what: 'sampling and thinking, applied on the next call', view: GenerationSection, wide: false },
    { id: 'talents',    label: 'Talents',    icon: Zap,               what: 'the reflexes the model may use',                view: TalentsSection,    wide: false },
    { id: 'sound',      label: 'Sound',      icon: Volume2,           what: 'what you hear, and how loud',                   view: SoundSection,      wide: false },
  ] as const;
  type SectionId = (typeof SECTIONS)[number]['id'];

  // Reopen where the user left off; an id from an older build falls back to Rig.
  const saved = storage.get<string>(storageKeys.settingsSection, 'rig');
  let active = $state<SectionId>(SECTIONS.some((s) => s.id === saved) ? (saved as SectionId) : 'rig');
  const current = $derived(SECTIONS.find((s) => s.id === active) ?? SECTIONS[0]);
  const View = $derived(current.view);

  function open(id: SectionId) {
    active = id;
    storage.set(storageKeys.settingsSection, id);
  }

  // A tablist moves with the arrow keys; Tab leaves it for the section.
  let tabs: HTMLElement | undefined = $state();
  function onKey(e: KeyboardEvent) {
    const i = SECTIONS.findIndex((s) => s.id === active);
    const to = e.key === 'ArrowRight' ? i + 1 : e.key === 'ArrowLeft' ? i - 1
      : e.key === 'Home' ? 0 : e.key === 'End' ? SECTIONS.length - 1 : -1;
    if (to < 0 || to >= SECTIONS.length || to === i) return;
    e.preventDefault();
    open(SECTIONS[to].id);
    tabs?.querySelectorAll<HTMLElement>('[role="tab"]')[to]?.focus();
  }
</script>

<main class="settings">
  <header class="shead">
    <div class="tabs" role="tablist" aria-label="Settings" tabindex="-1" bind:this={tabs} onkeydown={onKey}>
      {#each SECTIONS as s (s.id)}
        {@const Icon = s.icon}
        <button role="tab" id="set-tab-{s.id}" class="tab" class:on={s.id === active}
          aria-selected={s.id === active} aria-controls="set-panel" tabindex={s.id === active ? 0 : -1}
          onclick={() => open(s.id)}><Icon size={15} strokeWidth={1.8} /><span>{s.label}</span></button>
      {/each}
    </div>
    <p class="what">{current.what}</p>
  </header>

  <div class="page" class:wide={current.wide} id="set-panel" role="tabpanel" aria-labelledby="set-tab-{active}">
    <View />
  </div>
</main>

<style>
  .settings { flex: 1; min-width: 0; min-height: 0; display: flex; flex-direction: column; }

  /* The sections: an icon and a word each. The open one is selected the way
     everything in this panel is — raised, with an accent hairline, its icon in
     the accent: the same cue as the active project in the rail and the chosen
     profile below. One way to say "this one", everywhere. */
  .shead {
    flex-shrink: 0; display: flex; align-items: center; gap: 18px;
    padding: 10px 26px; border-bottom: 1px solid var(--line); background: var(--s1);
  }
  .tabs { display: flex; gap: 4px; min-width: 0; overflow-x: auto; scrollbar-width: none; }
  .tabs::-webkit-scrollbar { display: none; }
  .tab {
    flex-shrink: 0; display: inline-flex; align-items: center; gap: 8px; cursor: pointer;
    padding: 8px 13px 8px 11px; border: none; border-radius: 8px; background: transparent;
    font: inherit; font-size: var(--fs-body); font-weight: 560; color: var(--dim);
    transition: color var(--t-fast), background var(--t-fast), box-shadow var(--t-fast);
  }
  .tab:hover { color: var(--text); background: var(--s2); }
  .tab.on { color: var(--text); background: var(--surface-raised); box-shadow: inset 0 0 0 1px var(--accent-line); }
  .tab.on :global(svg) { color: var(--accent); }
  .tab:focus-visible { outline: 1px solid var(--accent); outline-offset: 2px; }
  .what {
    flex: 1; min-width: 0; margin: 0; text-align: right;
    font-size: var(--fs-small); color: var(--dim); white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }

  /* A form reads best in a narrow column; the Rig is a canvas and takes the
     room there is. */
  .page {
    flex: 1; min-height: 0; overflow-y: auto;
    width: 100%; max-width: 640px; margin: 0 auto; box-sizing: border-box;
    padding: 26px 26px 60px;
  }
  .page.wide { max-width: none; display: flex; flex-direction: column; padding: 18px 22px 22px; }

  @media (max-width: 900px) { .what { display: none; } }
  @media (max-width: 640px) {
    .shead { padding: 8px 12px; }
    .page { padding: 20px 16px 48px; }
  }
</style>
