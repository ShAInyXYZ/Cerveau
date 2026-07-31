<script>
  import { AlertTriangle, HelpCircle, Info, ChevronDown, ArrowRight } from 'lucide-svelte';

  // A single card component for both agent QUESTIONS and ERRORS, in Cerveau's
  // warm-ink + rose identity. Two interaction shapes:
  //   • choices  — full-width stacked option rows (each a real decision)
  //   • actions  — a compact inline button row (retry / dismiss)
  let {
    tone = 'err',            // err | warn | info | accent
    kind = '',               // small uppercase label (ASKS, FATAL, …)
    title = '',
    detail = '',
    meta = [],
    choices = null,          // [{ label, value, hint?, ghost? }] → stacked rows
    onChoose,                // (value) => void
    defaultOpen = false,     // start with the detail expanded (errors → true)
    children,
    actions                  // snippet → inline action buttons
  } = $props();

  const Icon = { err: AlertTriangle, warn: AlertTriangle, info: Info, accent: HelpCircle }[tone] ?? Info;
  let open = $state(defaultOpen);
</script>

<div class="notice {tone}">
  <div class="main">
    <Icon class="ico" size={17} strokeWidth={2.2} />

    <div class="col">
      <div class="topline">
        {#if kind}<span class="kind">{kind}</span>{/if}
        <span class="title">{title}</span>
      </div>

      {#if children}<div class="body">{@render children?.()}</div>{/if}

      {#if meta.length}
        <div class="meta">
          {#each meta as m}
            <span class="chip"><span class="mk">{m.label}</span><span class="mv">{m.value}</span></span>
          {/each}
        </div>
      {/if}

      {#if detail && open}
        <pre class="detail">{detail}</pre>
      {/if}
    </div>

    {#if detail}
      <button class="disc" class:open onclick={() => (open = !open)} aria-label="details">
        <ChevronDown size={14} />
      </button>
    {/if}
  </div>

  {#if choices?.length}
    <div class="choices">
      {#each choices as c}
        <button class="choice" class:ghost={c.ghost} type="button" onclick={() => onChoose?.(c.value ?? c.label)}>
          <span class="ctext">
            <span class="clabel">{c.label}</span>
            {#if c.hint}<span class="chint">{c.hint}</span>{/if}
          </span>
          <ArrowRight class="carrow" size={14} strokeWidth={2.2} />
        </button>
      {/each}
    </div>
  {/if}

  {#if actions}
    <div class="acts">{@render actions?.()}</div>
  {/if}
</div>

<style>
  .notice {
    --tone: var(--err);
    position: relative;
    /* never let a flex-column parent shrink the card — with overflow:hidden a
       shrunk card collapses to just its top hairline (a stray accent line) */
    flex-shrink: 0;
    border-radius: 12px;
    background:
      linear-gradient(180deg, color-mix(in srgb, var(--tone) 6%, var(--s1)) 0%, var(--s1) 46%);
    /* elevation, not a border — hairline tone ring + soft cast shadow */
    box-shadow:
      0 0 0 1px color-mix(in srgb, var(--tone) 16%, transparent),
      0 1px 0 0 color-mix(in srgb, #fff 4%, transparent) inset,
      0 -1px 0 0 var(--shade) inset;
    overflow: hidden;
    animation: rise .2s cubic-bezier(.16,1,.3,1);
  }
  .notice.err    { --tone: var(--err); }
  .notice.warn   { --tone: var(--warn); }
  .notice.info   { --tone: var(--info); }
  .notice.accent { --tone: var(--accent); }

  .main { display: flex; gap: 11px; padding: 15px 16px; }

  /* bare icon — no chip, just the tone-colored glyph aligned to the title */
  .main :global(.ico) {
    flex-shrink: 0;
    margin-top: 2px;
    color: var(--tone);
  }

  .col { flex: 1; min-width: 0; }

  .topline { display: flex; align-items: baseline; gap: 9px; flex-wrap: wrap; }
  .kind {
    flex-shrink: 0;
    font-family: var(--font-mono); font-size: 9px; font-weight: 600;
    letter-spacing: .18em; text-transform: uppercase;
    color: var(--tone); opacity: .9;
  }
  .title { font-size: 14px; font-weight: 560; color: var(--text); line-height: 1.5; }

  .body { font-size: 13px; color: var(--muted); line-height: 1.55; margin-top: 5px; }

  .meta { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 10px; }
  .chip {
    display: inline-flex; align-items: center; gap: 6px;
    font-family: var(--font-mono); font-size: 10px;
    padding: 2px 4px 2px 8px; border-radius: 5px;
    background: color-mix(in srgb, #fff 3%, transparent);
    box-shadow: inset 0 0 0 1px var(--ring);
  }
  .chip .mk { color: var(--faint); letter-spacing: .1em; }
  .chip .mv { color: var(--muted); background: var(--bg); padding: 1px 6px; border-radius: 3px; }

  .detail {
    margin: 10px 0 0; padding: 10px 12px;
    background: rgba(0,0,0,.28); border-radius: 7px;
    box-shadow: inset 0 0 0 1px var(--ring);
    font-family: var(--font-mono); font-size: 11px; line-height: 1.55;
    color: var(--muted); white-space: pre-wrap; overflow-x: auto;
  }

  .disc {
    flex-shrink: 0; align-self: flex-start;
    display: inline-flex; align-items: center; justify-content: center;
    width: 28px; height: 28px; border: none; border-radius: 8px;
    background: transparent; color: var(--faint); cursor: pointer;
    transition: color .12s, background .12s;
  }
  .disc:hover { color: var(--muted); background: color-mix(in srgb, #fff 5%, transparent); }
  .disc :global(svg) { transition: transform .18s ease; }
  .disc.open :global(svg) { transform: rotate(180deg); }

  /* ── stacked choices: each option is a full-width row ── */
  .choices {
    display: flex; flex-direction: column; gap: 6px;
    padding: 4px 10px 12px;
  }
  .choice {
    display: flex; align-items: center; justify-content: space-between; gap: 12px;
    width: 100%; text-align: left;
    padding: 11px 12px 11px 14px;
    border: none; border-radius: 9px; cursor: pointer;
    background: color-mix(in srgb, #fff 3.5%, transparent);
    box-shadow: inset 0 0 0 1px var(--ring);
    color: var(--text);
    transition: background .13s, box-shadow .13s, transform .05s;
  }
  .choice:hover {
    background: color-mix(in srgb, var(--accent) 12%, transparent);
    box-shadow: inset 0 0 0 1px var(--accent-line);
  }
  .choice:active { transform: translateY(1px); }
  .choice :global(.carrow) { color: var(--faint); flex-shrink: 0; transition: color .13s, transform .13s; }
  .choice:hover :global(.carrow) { color: var(--accent); transform: translateX(2px); }

  .ctext { display: flex; flex-direction: column; gap: 1px; min-width: 0; }
  .clabel { font-size: 13.5px; font-weight: 500; line-height: 1.35; }
  .chint  { font-size: 11.5px; color: var(--dim); line-height: 1.3; }

  /* the escape row ("decide yourself") — visually subordinate */
  .choice.ghost {
    background: transparent; box-shadow: none;
    color: var(--dim); padding: 8px 12px 8px 14px;
  }
  .choice.ghost .clabel { font-weight: 450; font-size: 12.5px; }
  .choice.ghost:hover { background: color-mix(in srgb, #fff 4%, transparent); box-shadow: none; color: var(--muted); }
  .choice.ghost:hover :global(.carrow) { color: var(--muted); }

  /* ── inline action row (errors) ── */
  .acts {
    display: flex; gap: 6px; flex-wrap: wrap;
    padding: 11px 16px;
    background: rgba(0,0,0,.18);
    box-shadow: inset 0 1px 0 0 var(--lift);
  }

  @keyframes rise { from { opacity: 0; transform: translateY(5px) scale(.994); } to { opacity: 1; transform: none; } }

  @media (prefers-reduced-motion: reduce) {
    .notice { animation: none; }
    .choice, .choice :global(.carrow) { transition: none; }
  }
</style>
