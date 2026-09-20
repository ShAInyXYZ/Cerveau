<script lang="ts">
  // The head of the Rig page, as ONE block: which profile, and what it is.
  //
  //   profiles      every profile this machine knows — name, engine, what it
  //                 loads — with the loaded one marked live
  //   spec plate    the chosen profile read like a device's label: a name, a
  //                 mode (real time | preview), and its parameters as
  //                 label-over-value pairs, the ones that matter first
  //
  // It replaced six stacked strips (a title, a sentence, pills, a status line,
  // two rows of identical chips, a legend, a note) in which nothing outranked
  // anything. Everything here is read from the profile — cores.json and the
  // user's overrides; nothing about a profile is written in this file.
  import BrandMark from './BrandMark.svelte';
  import { brandFor } from '../../brands';
  import { tooltip } from '../../../kit/tooltip.js';
  import { ChevronDown } from 'lucide-svelte';
  import { PARAMS, HIDDEN_PARAMS, profileLabel, modelName } from '../vocabulary';
  import type { RigPlacement, RigProfile } from '../../types';

  let { profiles, next, machine, notes, onpick }: {
    profiles: RigProfile[]; next: RigPlacement | null;
    machine: string; notes: string[]; onpick: (id: string) => void;
  } = $props();

  // The plate shows the parameters vocabulary.ts has a label for, in its order;
  // anything else the profile declares is kept, under "more" — a key nobody
  // has words for yet must still be visible.
  type Spec = { key: string; label: string; value: string; raw: string };
  const specs = $derived.by(() => {
    const p = next?.params ?? {};
    const plate: Spec[] = [], more: Spec[] = [];
    for (const [key, word] of Object.entries(PARAMS)) {
      if (word.label && p[key] !== undefined && p[key] !== '') plate.push({ key, label: word.label, value: word.say ? word.say(p[key]) : p[key], raw: p[key] });
    }
    // a profile that publishes nothing still has a window in cores.json
    if (!plate.length && next?.max_len) plate.push({ key: 'ctx', label: PARAMS.MAX_LEN.label!, value: PARAMS.MAX_LEN.say!(String(next.max_len)), raw: String(next.max_len) });
    for (const key of Object.keys(p).sort()) {
      if (!PARAMS[key]?.label && !HIDDEN_PARAMS.has(key) && p[key] !== '') more.push({ key, label: key, value: p[key], raw: p[key] });
    }
    return { plate, more };
  });
  const model = $derived(modelName(next?.params?.MODEL) || next?.model || '');
  let showMore = $state(false);
</script>

<div class="head">
  <div class="top">
    <span class="label">Profiles</span>
    {#if machine}<span class="machine mono">{machine}</span>{/if}
  </div>

  <div class="profiles" role="tablist" aria-label="Profiles">
    {#each profiles as p (p.id)}
      {@const on = p.id === next?.core}
      <button role="tab" class="prof" class:on aria-selected={on} onclick={() => onpick(p.id)}>
        <span class="pmark"><BrandMark brand={brandFor(p.engine)} size={22} /></span>
        <span class="ptext">
          <span class="pname">{profileLabel(p.name, p.engine)}</span>
          <span class="psub">{p.engine}{p.model ? ` · ${p.model}` : ''}</span>
        </span>
        {#if p.live}<span class="live mono"><i></i>live</span>{/if}
      </button>
    {/each}
  </div>

  {#if next}
    <div class="plate">
      <div class="ident">
        <div class="ititle">
          <h2>{next.name}</h2>
          <span class="mode mono" class:live={next.live}
            use:tooltip={next.live ? 'This is the loaded profile: the diagram shows what is on the cards right now.' : 'This profile is not loaded: the diagram shows where it would go.'}>
            <i></i>{next.live ? 'real time' : 'preview'}
          </span>
        </div>
        {#if model}<p class="model mono">{model}</p>{/if}
      </div>

      {#if specs.plate.length}
        <dl class="specs">
          {#each specs.plate as s (s.key)}
            <div class="spec" use:tooltip={PARAMS[s.key]?.tip ? `${PARAMS[s.key].tip}\n${s.key}=${s.raw}` : `${s.key}=${s.raw}`}><dt class="mono">{s.label}</dt><dd>{s.value}</dd></div>
          {/each}
        </dl>
        {#if specs.more.length}
          <button class="morebtn mono" class:open={showMore} aria-expanded={showMore} onclick={() => (showMore = !showMore)}>
            {specs.more.length} more<ChevronDown size={12} />
          </button>
        {/if}
      {:else}
        <p class="none">This profile does not publish its parameters.</p>
      {/if}
    </div>

    {#if showMore && specs.more.length}
      <div class="more mono">
        {#each specs.more as s (s.key)}<span><em>{s.key}</em>{s.value}</span>{/each}
      </div>
    {/if}
  {/if}

  {#if notes.length}
    <ul class="notes">{#each notes as n (n)}<li>{n}</li>{/each}</ul>
  {/if}
</div>

<style>
  .head {
    background: var(--s1); border-radius: 12px; box-shadow: 0 0 0 1px var(--line);
    padding: 14px 16px 0;
  }
  .top { display: flex; align-items: baseline; justify-content: space-between; gap: 16px; margin-bottom: 10px; }
  .machine { font-size: var(--fs-micro); letter-spacing: .04em; color: var(--dim); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

  /* ── profiles: two lines each, so a name is never the whole story ── */
  .profiles { display: grid; grid-template-columns: repeat(auto-fill, minmax(210px, 1fr)); gap: 8px; }
  .prof {
    display: flex; align-items: center; gap: 10px; min-width: 0; cursor: pointer; text-align: left;
    padding: 9px 11px; border: none; border-radius: 9px;
    background: var(--bg); box-shadow: inset 0 0 0 1px var(--line);
    color: var(--muted); font: inherit;
    transition: background var(--t-fast), box-shadow var(--t-fast), color var(--t-fast);
  }
  .prof:hover { background: var(--s2); color: var(--text); }
  /* selected, the way the rail selects a project: raised, with an accent hairline */
  .prof.on { background: var(--surface-raised); box-shadow: inset 0 0 0 1px var(--accent-line); color: var(--text); }
  .pmark { display: inline-flex; --c: var(--dim); }
  .prof.on .pmark { --c: var(--accent); }
  .ptext { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 1px; }
  .pname { font-size: var(--fs-body); font-weight: 620; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .psub { font-size: var(--fs-micro); color: var(--dim); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .live { flex-shrink: 0; display: inline-flex; align-items: center; gap: 5px; font-size: 9px; letter-spacing: .1em; text-transform: uppercase; color: var(--ok); }
  .live i, .mode i { width: 6px; height: 6px; border-radius: 50%; background: currentColor; }

  /* ── the spec plate ── */
  .plate {
    display: flex; align-items: center; gap: 12px 28px; flex-wrap: wrap;
    margin: 14px -16px 0; padding: 13px 16px 14px; border-top: 1px solid var(--line);
  }
  .ident { min-width: 0; flex-shrink: 0; max-width: 100%; }
  .ititle { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
  h2 { margin: 0; font-size: var(--fs-title); font-weight: 640; color: var(--text); }
  .mode {
    display: inline-flex; align-items: center; gap: 6px; cursor: default;
    font-size: 9px; letter-spacing: .1em; text-transform: uppercase; color: var(--dim);
    padding: 3px 7px; border-radius: 4px; box-shadow: inset 0 0 0 1px var(--line2);
  }
  .mode.live { color: var(--ok); box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--ok) 40%, transparent); }
  .mode:not(.live) i { background: none; box-shadow: inset 0 0 0 1px currentColor; }
  .model { margin: 3px 0 0; font-size: var(--fs-micro); color: var(--dim); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

  /* a label over its value, like the plate on a machine */
  .specs { flex: 1; min-width: 0; display: flex; flex-wrap: wrap; align-items: flex-end; gap: 10px 26px; margin: 0; }
  .spec { display: flex; flex-direction: column; gap: 3px; cursor: default; }
  dt { font-size: 9px; letter-spacing: .1em; text-transform: uppercase; color: var(--dim); white-space: nowrap; }
  dd { margin: 0; font-size: var(--fs-body); font-weight: 600; color: var(--text); white-space: nowrap; font-variant-numeric: tabular-nums; }
  .morebtn {
    display: inline-flex; align-items: center; gap: 4px; flex-shrink: 0; align-self: flex-end; cursor: pointer;
    padding: 4px 8px; border: none; border-radius: 5px; background: transparent;
    font-size: var(--fs-micro); letter-spacing: .04em; color: var(--dim);
    transition: color var(--t-fast), background var(--t-fast);
  }
  .morebtn:hover { color: var(--text); background: var(--s2); }
  .morebtn :global(svg) { transition: transform var(--t-fast); }
  .morebtn.open :global(svg) { transform: rotate(180deg); }
  .none { margin: 0; font-size: var(--fs-small); color: var(--dim); }

  .more {
    display: flex; flex-wrap: wrap; gap: 6px 18px; margin: 0 -16px; padding: 10px 16px 12px;
    border-top: 1px solid var(--line); font-size: 10.5px; color: var(--text);
  }
  .more em { font-style: normal; color: var(--dim); margin-right: 6px; }

  /* what the drawing cannot say by itself — the plate's footnotes */
  .notes { list-style: none; margin: 0 -16px; padding: 9px 16px 10px; border-top: 1px solid var(--line); display: flex; flex-direction: column; gap: 3px; }
  .notes li { position: relative; padding-left: 14px; font-size: var(--fs-small); line-height: 1.5; color: var(--muted); }
  .notes li::before { content: ''; position: absolute; left: 2px; top: .62em; width: 5px; height: 5px; border-radius: 50%; background: var(--warn); }

  @media (max-width: 640px) {
    .profiles { grid-template-columns: 1fr; }
    .machine { display: none; }
  }
</style>
