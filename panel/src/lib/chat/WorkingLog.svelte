<script lang="ts">
  import { tooltip } from '../../kit/tooltip.js';
  import { mergeSteps } from '../steps';
  import { sessionStore } from '../stores/session.svelte.ts';
  import {
    Pause, Square, Terminal, FileText, Search, Globe, Brain, Monitor, Server,
    ChevronRight, Check, X, Loader,
  } from 'lucide-svelte';

  const OUTPUT_CAP = 4000;

  const toolSteps = $derived(mergeSteps(sessionStore.liveSteps));
  const anyRunning = $derived(toolSteps.some((s) => s.status === 'run'));

  const ICONS: Record<string, typeof Terminal> = {
    bash: Terminal, read: FileText, write: FileText, edit: FileText, outline_file: FileText,
    grep: Search, find_symbol: Search, find_references: Search, file_map: Search,
    web_fetch: Globe, remember: Brain, check_page: Monitor, serve: Server,
  };
  const toolIcon = (name?: string) => ICONS[name ?? ''] ?? Terminal;

  let openStep = $state<string | null>(null);

  // live elapsed readout
  let now = $state(Date.now());
  $effect(() => {
    if (!sessionStore.running) return;
    const i = setInterval(() => (now = Date.now()), 500);
    return () => clearInterval(i);
  });
  const elapsed = $derived(
    sessionStore.runStarted ? Math.floor((now - sessionStore.runStarted) / 1000) : 0,
  );
</script>

{#if sessionStore.running}
  <section class="working" aria-label="agent working log">
    <div class="whead">
      <span class="wname label">CERVEAU</span>
      <span class="wstatus">{anyRunning ? 'working' : 'thinking'}<span class="ell">…</span></span>
      <span class="wtime tag">{elapsed}s</span>
      <div class="rspace"></div>
      <button class="ictl" onclick={() => sessionStore.pause()} use:tooltip={'pause'}
        aria-label="pause the running turn"><Pause size={11} /></button>
      <button class="ictl danger" onclick={() => sessionStore.kill()} use:tooltip={'stop'}
        aria-label="stop the running turn"><Square size={11} /></button>
    </div>

    {#if toolSteps.length}
      <div class="steps">
        {#each toolSteps as s (s.id)}
          {#if s.kind === 'tool'}
            {@const SvgIcon = toolIcon(s.name)}
            <div class="tool" class:done={s.status === 'ok'} class:fail={s.status === 'fail'}>
              <button
                class="toolhead"
                class:has-output={s.output}
                onclick={() => (openStep = openStep === s.id ? null : s.id)}
                aria-expanded={openStep === s.id}
                aria-label={`${s.name} ${s.status === 'run' ? 'running' : s.status === 'fail' ? 'failed' : 'done'}${s.output ? ', toggle output' : ''}`}
              >
                <span class="tstate">
                  {#if s.status === 'run'}<Loader class="spin" size={13} />
                  {:else if s.status === 'fail'}<X size={13} />
                  {:else}<Check size={13} />{/if}
                </span>
                <SvgIcon size={13} class="ticon" />
                <span class="tname">{s.name}</span>
                <code class="targ">{s.arg}</code>
                {#if s.output}
                  <ChevronRight class={openStep === s.id ? 'tchev open' : 'tchev'} size={13} />
                {/if}
              </button>
              {#if s.output && openStep === s.id}
                <pre class="toolout">{s.output.length > OUTPUT_CAP ? s.output.slice(0, OUTPUT_CAP) + '\n…[truncated]' : s.output}</pre>
              {/if}
            </div>
          {:else if s.kind === 'note' && (s.noteKind === 'no_progress' || s.noteKind === 'breaker_tripped')}
            <div class="step stalled">
              <span class="sd stall"></span>
              <span class="stext">{s.text}</span>
            </div>
          {:else if s.kind === 'note' && s.noteKind === 'context_compacted'}
            <div class="step compaction">
              <span class="sd compacted"></span>
              <span class="stext">{s.text}</span>
            </div>
          {:else if s.kind === 'note'}
            <div class="step"><span class="sd note"></span><span class="stext">{s.text}</span></div>
          {:else if s.kind === 'error' || s.kind === 'abort'}
            <div class="step"><span class="sd {s.kind}"></span><span class="stext">{s.text}</span></div>
          {/if}
        {/each}
      </div>
    {/if}
  </section>
{/if}

<style>
  .working { align-self: stretch; max-width: 780px; padding: 2px 0; }
  .whead { display: flex; align-items: center; gap: 9px; }
  .wname { color: var(--dim); }
  .wstatus {
    font-family: var(--font-mono); font-size: 12px; color: var(--muted);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .ell { color: var(--accent); animation: pulse 1.2s ease-in-out infinite; }
  @keyframes pulse { 0%,100% { opacity: 1; } 50% { opacity: .3; } }
  .wtime { color: var(--faint); }
  .rspace { flex: 1; min-width: 12px; }
  .ictl {
    display: inline-flex; align-items: center; justify-content: center;
    width: 22px; height: 22px; border-radius: 5px;
    background: transparent; color: var(--faint); cursor: pointer; border: none;
    transition: color var(--t-fast), background var(--t-fast);
  }
  .ictl:hover { color: var(--text); background: color-mix(in srgb, #fff 5%, transparent); }
  .ictl.danger:hover { color: var(--err); }

  .steps { display: flex; flex-direction: column; gap: 5px; margin: 9px 0 0; }

  .step {
    display: flex; align-items: center; gap: 8px;
    font-size: 11.5px; color: var(--dim); padding-left: 3px;
  }
  .sd { width: 4px; height: 4px; border-radius: 50%; background: var(--faint); flex-shrink: 0; }
  .sd.note { background: var(--dim); }
  /* Compaction is not routine chatter: the session just lost history, and a
     user who cannot see that reads the model's next gap as disobedience. */
  .sd.compacted { background: var(--am, #e8873a); }
  /* The model is circling. Visible so a long quiet run can be read as stuck
     rather than as thinking hard. */
  .sd.stall { background: var(--warn, #c2603f); }
  .step.stalled { border-left: 2px solid var(--warn, #c2603f); padding-left: 8px; margin: 4px 0; }
  .step.stalled .stext { color: var(--warn, #c2603f); }
  .step.compaction {
    border-left: 2px solid var(--am, #e8873a);
    padding-left: 8px;
    margin: 4px 0;
  }
  .step.compaction .stext { color: var(--am, #e8873a); }
  .sd.error, .sd.abort { background: var(--err); }
  .stext { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

  .tool {
    border-radius: 8px;
    background: color-mix(in srgb, #fff 2.5%, transparent);
    box-shadow: inset 0 0 0 1px var(--ring);
    overflow: hidden;
  }
  .toolhead {
    display: flex; align-items: center; gap: 8px; width: 100%;
    padding: 7px 10px; text-align: left;
    background: transparent; border: none; color: var(--muted);
    font-family: inherit; cursor: default;
  }
  .toolhead.has-output { cursor: pointer; }
  .toolhead.has-output:hover { background: color-mix(in srgb, #fff 3%, transparent); }

  .tstate { display: inline-flex; flex-shrink: 0; color: var(--dim); }
  .tool.done .tstate { color: var(--ok); }
  .tool.fail .tstate { color: var(--err); }
  .toolhead :global(.spin) { animation: spin 1s linear infinite; color: var(--accent); }
  @keyframes spin { to { transform: rotate(360deg); } }

  .toolhead :global(.ticon) { color: var(--faint); flex-shrink: 0; }
  .tname {
    font-family: var(--font-mono); font-size: 11px; font-weight: 500;
    color: var(--dim); flex-shrink: 0; letter-spacing: .02em;
  }
  .targ {
    flex: 1; min-width: 0;
    font-family: var(--font-mono); font-size: 11.5px; color: var(--text);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .toolhead :global(.tchev) { color: var(--faint); flex-shrink: 0; transition: transform var(--t-med); }
  .toolhead :global(.tchev.open) { transform: rotate(90deg); }

  .toolout {
    margin: 0; padding: 9px 12px;
    background: rgba(0,0,0,.32);
    box-shadow: inset 0 1px 0 0 var(--lift);
    font-family: var(--font-mono); font-size: 11px; line-height: 1.5;
    color: var(--muted); white-space: pre-wrap; word-break: break-word;
    max-height: 260px; overflow: auto;
  }
</style>
