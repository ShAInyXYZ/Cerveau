<script>
  // notice card — for errors / questions / plan. Left accent rule, tone-coded.
  let { tone = 'accent', label = '', children, actions } = $props();
  // tone: accent | err | warn | ok | info
</script>

<div class="card {tone} anim-rise">
  {#if label}<div class="clabel label">{label}</div>{/if}
  <div class="cbody">{@render children?.()}</div>
  {#if actions}<div class="cactions">{@render actions?.()}</div>{/if}
</div>

<style>
  .card {
    --tone: var(--faint);
    border-radius: 10px;
    background: linear-gradient(180deg, color-mix(in srgb, var(--tone) 4%, var(--s1)) 0%, var(--s1) 46%);
    box-shadow: 0 0 0 1px color-mix(in srgb, var(--tone) 13%, transparent), 0 4px 12px -8px rgba(0,0,0,.5);
    padding: 13px 15px;
  }
  .card.accent { --tone: var(--accent); }
  .card.err    { --tone: var(--err); }
  .card.warn   { --tone: var(--warn); }
  .card.ok     { --tone: var(--ok); }
  .card.info   { --tone: var(--info); }

  .clabel { margin-bottom: 8px; color: var(--tone); letter-spacing: .16em; }

  .cbody { font-size: 13px; line-height: 1.55; }
  .cactions { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 10px; }
</style>
