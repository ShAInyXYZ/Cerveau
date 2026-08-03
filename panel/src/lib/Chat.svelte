<script>
  import { Button, Dot, Notice } from '../kit/index.js';
  import { tooltip } from '../kit/tooltip.js';
  import Markdown from './Markdown.svelte';
  import ModeKnob from './ModeKnob.svelte';
  import AttachKnob from './AttachKnob.svelte';
  import WorkspacePath from './WorkspacePath.svelte';
  import { fmtTime } from './api.js';
  import { Pause, Square, ArrowUp, Terminal, FileText, Search, Globe, Brain, ChevronRight, ChevronDown, ListChecks, Check, X, Loader } from 'lucide-svelte';

  let {
    messages = [], running = false, runStarted = null, mode = $bindable('discussion'),
    question = null, errors = [], report = null, workspace = '', liveSteps = [],
    modalities = null, isInstant = false,
    onSend, onSteer, onPause, onKill, onAnswer, onRunAutopilot, onWorkspaceChanged,
    onRetry, onDismissError, onAttach
  } = $props();

  // Assistant turns that carry ONLY a tool call have empty text. Their tool call
  // is already shown in the live-steps / Activity rail, so rendering them as chat
  // bubbles just produces an empty CERVEAU block. Show a message only if it's from
  // the user, or it's an assistant message with actual text.
  const visibleMessages = $derived(
    messages.filter((m) => m.type === 'msg.user' || (m.payload?.text ?? '').trim() !== '')
  );

  // Merge tool.call + its tool.result into one card (running → done/fail) so the
  // working block reads like a Claude-Code tool log: command + its output inline.
  const toolSteps = $derived.by(() => {
    const out = [];
    for (const s of liveSteps) {
      if (s.kind === 'result') {
        const call = [...out].reverse().find((c) => c.kind === 'tool' && c.name === s.name && c.status === 'run');
        if (call) { call.status = s.ok ? 'ok' : 'fail'; call.output = s.output ?? ''; continue; }
      }
      out.push({ ...s });
    }
    return out;
  });
  const toolIcon = (name) => ({
    bash: Terminal, read: FileText, write: FileText, edit: FileText, outline_file: FileText,
    grep: Search, find_symbol: Search, find_references: Search, file_map: Search,
    web_fetch: Globe, remember: Brain
  }[name] ?? Terminal);
  let openStep = $state(null); // id of the step whose output is expanded

  // last thing the user said — what "retry" re-runs
  const lastUserText = $derived.by(() => {
    for (let i = messages.length - 1; i >= 0; i--) {
      if (messages[i].type === 'msg.user') return messages[i].payload?.text ?? '';
    }
    return '';
  });

  let draft = $state('');
  let planOpen = $state(true);   // the pinned plan strip starts open
  let scroller;

  const isAuto = $derived(mode === 'autopilot');

  // Show only the latest MEANINGFUL error. Drop retry chatter and bare
  // cancellations ("context canceled" from an aborted/steered turn) — those are
  // not failures the user needs to act on, and an empty/near-empty card renders
  // as a stray red hairline that lingers after the card is gone.
  const activeError = $derived.by(() => {
    const real = errors.filter((e) => {
      const what = (e.what ?? '').trim();
      const why = (e.why ?? e.detail ?? '').toLowerCase();
      if (!what) return false;
      if (/retrying/i.test(what)) return false;
      if (/context canceled|canceled|cancelled/.test(why)) return false;
      return true;
    });
    return real.length ? real[real.length - 1] : null;
  });

  function submit() {
    const t = draft.trim();
    if (!t) return;
    draft = '';
    if (running) onSteer(t); else onSend(t);
  }

  let now = $state(Date.now());
  $effect(() => {
    if (!running) return;
    const i = setInterval(() => (now = Date.now()), 500);
    return () => clearInterval(i);
  });
  const elapsed = $derived(runStarted ? Math.floor((now - runStarted) / 1000) : 0);

  $effect(() => {
    messages.length;
    if (scroller) queueMicrotask(() => (scroller.scrollTop = scroller.scrollHeight));
  });
</script>

<main class="chat">
  <div class="stream" bind:this={scroller}>
    {#if messages.length === 0 && !running}
      <div class="void">
        <div class="void-mark">CERVEAU</div>
        <div class="label">{isAuto ? 'AUTOPILOT · FULL AUTONOMY — DESCRIBE THE TASK' : 'READY · TYPE TO BEGIN'}</div>
      </div>
    {/if}

    {#each visibleMessages as m, i (m.id ?? m.ts ?? i)}
      {@const user = m.type === 'msg.user'}
      <div class="turn" class:user>
        <div class="tmeta">
          <span class="label">{user ? 'YOU' : 'CERVEAU'}</span>
          <span class="tag">{fmtTime(m.ts)}</span>
        </div>
        <div class="tbody">
          {#if user}
            <span class="utext">{m.payload?.text ?? ''}</span>
          {:else}
            <Markdown source={m.payload?.text ?? ''} />
          {/if}
        </div>
      </div>
    {/each}

    {#if activeError}
      {@const e = activeError}
      <Notice
        tone="err"
        kind={e.class}
        title={e.what}
        detail={e.why}
        defaultOpen={true}
        meta={e.tried && e.tried.trim() && e.tried !== '·' ? [{ label: 'TRIED', value: e.tried }] : []}
      >
        {#snippet actions()}
          {#if lastUserText}
            <Button size="sm" variant="primary" onclick={() => onRetry?.(lastUserText)}>retry</Button>
          {/if}
          <Button size="sm" variant="ghost" onclick={() => onDismissError?.()}>dismiss</Button>
        {/snippet}
      </Notice>
    {/if}

    {#if question}
      <Notice
        tone="accent"
        kind="asks"
        title={question.question}
        choices={[
          ...(question.options ?? []).map((opt) => ({ label: opt, value: opt })),
          { label: 'Decide yourself', value: 'decide yourself', ghost: true }
        ]}
        onChoose={(v) => onAnswer(v)}
      />
    {/if}

    {#if running}
      <div class="working">
        <div class="whead">
          <span class="wname label">CERVEAU</span>
          <span class="wstatus">
            {#if toolSteps.some((s) => s.status === 'run')}working{:else}thinking{/if}<span class="ell">…</span>
          </span>
          <span class="wtime tag">{elapsed}s</span>
          <div class="rspace"></div>
          <button class="ictl" onclick={onPause} use:tooltip={"pause"}><Pause size={11} /></button>
          <button class="ictl danger" onclick={onKill} use:tooltip={"stop"}><Square size={11} /></button>
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
                    <pre class="toolout">{s.output.length > 4000 ? s.output.slice(0, 4000) + '\n…[truncated]' : s.output}</pre>
                  {/if}
                </div>
              {:else if s.kind === 'note'}
                <div class="step"><span class="sd note"></span><span class="stext">{s.text}</span></div>
              {:else if s.kind === 'error' || s.kind === 'abort'}
                <div class="step"><span class="sd {s.kind}"></span><span class="stext">{s.text}</span></div>
              {/if}
            {/each}
          </div>
        {/if}
      </div>
    {/if}
  </div>

  <div class="dockzone">
   <div class="dockstack">
    {#if !isInstant}
      <div class="wsline"><WorkspacePath {workspace} onChanged={onWorkspaceChanged} /></div>
    {/if}
    {#if report}
      {@const pending = report.steps.filter((s) => s.status !== 'done' && s.status !== 'failed').length}
      {@const finished = pending === 0}
      <div class="planstrip" class:done={finished}>
        <button class="ps-head" onclick={() => (planOpen = !planOpen)}
          use:tooltip={planOpen ? 'collapse the plan' : 'show the plan steps'}>
          <ListChecks size={12} />
          <span class="ps-title">{report.title}</span>
          <span class="ps-counts mono">
            <span class="c ok">{report.done}</span>
            {#if report.failed}<span class="c err">{report.failed}</span>{/if}
            {#if pending}<span class="c dim">{pending}</span>{/if}
          </span>
          {#if report.handback}<span class="ps-chip warn">handback</span>{/if}
          {#if planOpen}<ChevronDown size={12} />{:else}<ChevronRight size={12} />{/if}
        </button>
        {#if planOpen}
          <div class="ps-steps">
            {#each report.steps as s, i}
              <div class="ps-step" class:on={s.status === 'done'} class:bad={s.status === 'failed'}
                class:part={s.status === 'partial'}>
                <Dot tone={s.status === 'done' ? 'ok' : s.status === 'failed' ? 'err' : s.status === 'partial' ? 'warn' : 'off'} size={5} />
                <span class="rnum tag">{String(i + 1).padStart(2, '0')}</span>
                <span class="ps-name">{s.title}</span>
                <span class="rstatus label">{s.status}</span>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    {/if}
    <div class="dockrow">
      <ModeKnob bind:mode />

      <div class="bar">

        <textarea
          class="input"
          bind:value={draft}
          rows="1"
          placeholder={running ? 'steer the running turn…' : isAuto ? 'describe the task — autopilot runs it end to end…' : 'message cerveau…'}
          onkeydown={(e) => e.key === 'Enter' && !e.shiftKey && (e.preventDefault(), submit())}
        ></textarea>
        {#if running}<span class="steerbadge label">STEER</span>{/if}
        <button class="send" class:steer={running} disabled={!draft.trim()} onclick={submit}
          use:tooltip={running ? 'steer' : 'send'}>
          <ArrowUp size={17} strokeWidth={2.5} />
        </button>
      </div>

      <AttachKnob {modalities} onAttach={(kind) => onAttach?.(kind)} />
    </div>
  </div>
</main>

<style>
  .chat { flex: 1; display: flex; flex-direction: column; min-width: 0; min-height: 0; }
  .stream {
    /* large bottom offset so the last message / tall question cards always scroll
       well clear of the floating dock (workspace chip + input bar) and its
       gradient fade — never trapped or clipped behind it */
    flex: 1; overflow-y: auto; padding: 22px 26px 400px;
    display: flex; flex-direction: column; gap: 20px;
    width: 100%; max-width: 780px; margin: 0 auto;
  }
  /* stream children must keep their natural height — a flex-column shrinks items
     by default, and a card with overflow:hidden then collapses to a stray line */
  .stream > :global(*) { flex-shrink: 0; }

  .void { margin: auto; text-align: center; display: flex; flex-direction: column; gap: 12px; align-items: center; }
  .void-mark { font-family: var(--font-mono); letter-spacing: .5em; font-size: 13px; color: var(--faint); }

  .turn { display: flex; flex-direction: column; gap: 6px; max-width: 760px; }
  .turn.user { align-self: flex-end; align-items: flex-end; max-width: 620px; }
  .tmeta { display: flex; align-items: baseline; gap: 8px; }
  .turn.user .tmeta { flex-direction: row-reverse; }
  .utext {
    display: inline-block;
    background: var(--surface-raised); border-radius: 10px;
    box-shadow: var(--elev-1);
    padding: 11px 14px; font-size: 13.5px; line-height: 1.55; white-space: pre-wrap;
    color: var(--text);
  }

  .rcounts { display: flex; gap: 12px; font-size: 10px; margin-bottom: 8px; }
  .c.ok { color: var(--ok); } .c.err { color: var(--err); } .c.dim { color: var(--dim); } .c.warn { color: var(--warn); }
  .rsteps { display: flex; flex-direction: column; gap: 4px; }
  .rstep { display: flex; align-items: center; gap: 9px; }
  .rnum { color: var(--faint); }
  .rtitle { flex: 1; font-size: 12.5px; }

  .ewhat { font-weight: 600; margin-bottom: 4px; }
  .ewhy { font-size: 11px; color: var(--muted); background: var(--bg); border: 1px solid var(--line); border-radius: var(--r-sm); padding: 6px 8px; margin: 4px 0; line-height: 1.5; }
  .etried { margin-top: 4px; }
  .qtext { font-size: 13.5px; line-height: 1.55; }

  /* quiet, restrained — no glow box. just a live status line + dim step history */
  .working { align-self: stretch; max-width: 780px; padding: 2px 0; }
  .whead { display: flex; align-items: center; gap: 9px; }
  .wname { color: var(--dim); }
  .wstatus {
    font-family: var(--font-mono); font-size: 12px; color: var(--muted);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .ell {
    color: var(--accent);
    animation: pulse 1.2s ease-in-out infinite;
  }
  @keyframes pulse { 0%,100% { opacity: 1; } 50% { opacity: .3; } }
  .wtime { color: var(--faint); }
  .rspace { flex: 1; min-width: 12px; }
  .ictl {
    display: inline-flex; align-items: center; justify-content: center;
    width: 22px; height: 22px; border-radius: 5px;
    background: transparent; color: var(--faint); cursor: pointer; border: none;
    transition: color .1s, background .1s;
  }
  .ictl:hover { color: var(--text); background: color-mix(in srgb, #fff 5%, transparent); }
  .ictl.danger:hover { color: var(--err); }

  .steps { display: flex; flex-direction: column; gap: 5px; margin: 9px 0 0; }

  /* plain one-line steps (notes, errors, aborts) */
  .step {
    display: flex; align-items: center; gap: 8px;
    font-size: 11.5px; color: var(--dim); padding-left: 3px;
  }
  .sd { width: 4px; height: 4px; border-radius: 50%; background: var(--faint); flex-shrink: 0; }
  .sd.note { background: var(--dim); }
  .sd.error, .sd.abort { background: var(--err); }
  .stext { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

  /* ── tool card: command line + collapsible output (Claude-Code style) ── */
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
  .toolhead :global(.tchev) { color: var(--faint); flex-shrink: 0; transition: transform .15s; }
  .toolhead :global(.tchev.open) { transform: rotate(90deg); }

  .toolout {
    margin: 0; padding: 9px 12px;
    background: rgba(0,0,0,.32);
    box-shadow: inset 0 1px 0 0 var(--lift);
    font-family: var(--font-mono); font-size: 11px; line-height: 1.5;
    color: var(--muted); white-space: pre-wrap; word-break: break-word;
    max-height: 260px; overflow: auto;
  }

  @media (prefers-reduced-motion: reduce) {
    .toolhead :global(.spin) { animation: none; }
  }

  /* floating dock */
  /* pinned plan strip — the plan must not scroll away mid-execution */
  .planstrip {
    /* match the chat bar (.bar), not the whole dockrow: the bar is inset by
       the 54px mode knob + 12px gap on the left and the attach button on the
       right, so mirror that inset here. */
    align-self: stretch; margin: 0 66px 8px; position: relative; z-index: 1; border-radius: 10px; overflow: hidden;
    background: var(--s1); box-shadow: inset 0 0 0 1px var(--line);
  }
  .planstrip.done { opacity: .72; }
  .ps-head {
    display: flex; align-items: center; gap: 8px; width: 100%;
    padding: 7px 11px; border: none; cursor: pointer;
    background: transparent; color: var(--dim); text-align: left;
  }
  .ps-head:hover { color: var(--text); }
  .ps-title {
    flex: 1; min-width: 0; font-size: 11.5px; font-weight: 600; color: var(--text);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .ps-counts { display: flex; gap: 6px; font-size: 10px; }
  .ps-chip {
    font-size: 8.5px; letter-spacing: .1em; text-transform: uppercase;
    padding: 2px 6px; border-radius: 4px;
  }
  .ps-chip.warn { color: var(--warn); background: color-mix(in srgb, var(--warn) 14%, transparent); }
  .ps-steps { padding: 0 11px 8px; display: flex; flex-direction: column; gap: 3px; }
  .ps-step { display: flex; align-items: baseline; gap: 7px; font-size: 11px; }
  .ps-name {
    flex: 1; min-width: 0; color: var(--muted);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .ps-step.on .ps-name { color: var(--text); }
  .ps-step.bad .ps-name { color: color-mix(in srgb, var(--err) 80%, var(--text)); }
  .ps-step.part .ps-name { color: var(--warn); }

  .dockzone {
    flex-shrink: 0;
    padding: 0 26px 20px;
    display: flex; justify-content: center;
    background: linear-gradient(to top, var(--bg) 60%, transparent);
  }
  /* the plan strip stacks ABOVE the composer, both sharing its width */
  /* the plan strip and the composer share ONE width, so they read as a
     single stacked unit — the stack matches the composer row (760px). */
  .dockstack { width: 100%; max-width: 760px; display: flex; flex-direction: column; }
  .wsline { align-self: flex-end; margin: 0 66px 6px 0; position: relative; z-index: 4; }
  .wsrow {
    width: 100%; max-width: 760px;
    display: flex; justify-content: flex-end;
    padding: 0 6px 6px 0;
  }
  .dockrow {
    width: 100%;
    display: flex; align-items: center; gap: 12px;
  }
  /* knob sits OUTSIDE the bar, on the left */
  .dockrow > :global(.knobbtn) { align-self: center; }

  .bar {
    position: relative;
    flex: 1; min-width: 0;
    display: flex; align-items: center; gap: 8px;
    background: var(--surface-raised);
    border-radius: 999px;                 /* pill */
    padding: 6px 6px 6px 18px;
    box-shadow: var(--elev-2);
    transition: box-shadow .14s;
  }
  /* workspace label: a small self-contained chip sitting just above the bar, right-aligned */
  .wschip {
    position: absolute; right: 8px; bottom: calc(100% + 6px);
    /* the pinned plan strip owns the space directly above the bar */
    max-width: 90%;
    background: var(--surface-raised);
    border-radius: 8px;
    box-shadow: var(--elev-1);
    z-index: 2;
  }
  .bar:focus-within {
    box-shadow:
      0 0 0 1px var(--accent-line),
      0 1px 0 0 var(--lift-strong) inset,
      0 -1px 0 0 var(--shade) inset;
  }

  .input {
    flex: 1; min-width: 0;
    background: transparent; border: none; outline: none; resize: none;
    color: var(--text); font-family: var(--font-sans);
    font-size: 14px; line-height: 1.5;
    padding: 8px 0;
    max-height: 120px;
  }
  .input::placeholder { color: var(--faint); }

  .steerbadge { color: var(--accent); letter-spacing: .18em; flex-shrink: 0; }

  /* the send button — a real circular action, aligned with the knob */
  .send {
    display: inline-flex; align-items: center; justify-content: center;
    width: 42px; height: 42px; flex-shrink: 0;
    border-radius: 50%;
    border: 1px solid var(--accent);
    background: var(--accent); color: var(--accent-ink);
    cursor: pointer;
    transition: filter .1s, transform .05s, background .1s;
  }
  .send:hover:not(:disabled) { filter: brightness(1.08); }
  .send:active:not(:disabled) { transform: scale(.94); }
  .send:disabled { opacity: .3; cursor: default; background: var(--s3); border-color: var(--line2); color: var(--dim); }
  .send.steer { background: var(--s3); color: var(--accent); border-color: var(--accent-line); }
</style>
