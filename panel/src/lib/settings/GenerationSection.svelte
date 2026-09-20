<script>
  import { settingsStore } from '../stores/settings.svelte.ts';
  import { tooltip } from '../../kit/tooltip.js';
  import { Segmented } from '../../kit/index.js';

  // How the model answers. Both are per-call fields, so a change applies to
  // the next model call with no restart — which is why they are not in Engine,
  // where everything costs a reload.

  // ── sampling: the SESSION DEFAULT. A single turn can override it from the
  // chat bar; this is the value everything else uses.
  const sampling = $derived(settingsStore.sampling);
  const SAMPLING_TIP = {
    default:  'No temperature or top_p override — the Core supplies its configured defaults for this model.',
    strict:   'temperature 0.2, no top_p override. Optional preset; not proven best for code.',
    neutral:  'temperature 0.55, top_p 0.85. Looser, for drafting and exploration. Not benchmarked here.',
    creative: 'temperature 0.7, top_p 0.9. Widest spread, for when there is no single correct answer. Not benchmarked here.'
  };
  const setSampling = (name) => settingsStore.setSampling(name);

  // ── thinking: which turns reason before answering, and how hard ──
  // Chat stays direct; a build gets to think.
  const thinking = $derived(settingsStore.thinking);
  // The turn-mode knob on the chat bar also has an "autopilot". Same word,
  // different setting; a build ran with thinking limited to the planning
  // call while the user believed it was thinking everywhere (2026-09-05).
  // The API values stay; only what the user reads changes.
  const THINK_MODE_LABEL = {
    off: 'never', plan: 'plan only', autopilot: 'every step', always: 'every turn'
  };
  const THINK_MODE_TIP = {
    off: 'Never think. Fastest; the model reasons in its answer text if at all.',
    plan: 'Think only while dividing a build into steps. Measured: the planning call reasons ~500 tokens and plans well; code-writing calls reason ~10k and overflow. The default.',
    autopilot: 'Think on every autopilot call, steps included. Slower; each code step reasons for minutes.',
    always: 'Think on every turn, chat included. Slowest.'
  };
  const THINK_EFFORT_TIP = {
    low: 'Brief thinking, straight to the conclusion. The default.',
    medium: 'Longer reasoning. In testing one planning step thought for 13k tokens.',
    xhigh: 'The model\'s default: validate assumptions, weigh alternatives. Thousands of tokens per call.'
  };
  const setThinking = (patch) => settingsStore.setThinking(patch);

  void settingsStore.load();
</script>

<section>
  <h2 class="sect-title">Generation</h2>
  <p class="sect-note">
    How the model answers. Both apply to the next model call — nothing restarts.
  </p>

  {#if sampling.presets?.length}
    <div class="samp first">
      <div class="samp-head">
        <span class="samp-label">Sampling</span>
        <span class="samp-hint">the default for every future run · one turn can override it from the chat bar</span>
      </div>
      <!-- kit/Segmented, not a local copy: it carries role="tablist" and
           aria-selected, which a hand-rolled row of buttons does not. -->
      <div use:tooltip={SAMPLING_TIP[sampling.active] || ''}>
        <Segmented
          options={sampling.presets.map((p) => ({ value: p, label: p }))}
          value={sampling.active}
          onchange={setSampling} />
      </div>
    </div>
  {/if}

  {#if thinking.modes?.length}
    <div class="samp">
      <div class="samp-head">
        <span class="samp-label">Thinking</span>
        <span class="samp-hint">reason before answering · costs time, not context</span>
      </div>
      <div class="think-rows">
        <div use:tooltip={THINK_MODE_TIP[thinking.mode] || ''}>
          <Segmented
            options={thinking.modes.map((m) => ({ value: m, label: THINK_MODE_LABEL[m] ?? m }))}
            value={thinking.mode}
            onchange={(mode) => setThinking({ mode })} />
        </div>
        <div class:dimmed={thinking.mode === 'off'} use:tooltip={THINK_EFFORT_TIP[thinking.effort] || ''}>
          <Segmented
            options={thinking.efforts.map((e) => ({ value: e, label: e }))}
            value={thinking.effort}
            onchange={(effort) => setThinking({ effort })} />
        </div>
      </div>
    </div>
  {/if}

  {#if !sampling.presets?.length && !thinking.modes?.length}
    <p class="empty mono">This core does not report sampling or thinking settings.</p>
  {/if}
  {#if settingsStore.error}<p class="alert" role="alert">{settingsStore.error}</p>{/if}
</section>

<style>
  .samp.first { margin-top: 0; }
  .think-rows { display: flex; gap: 10px; flex-wrap: wrap; align-items: center; }
  .think-rows .dimmed { opacity: .45; }
</style>
