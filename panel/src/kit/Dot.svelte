<script>
  // status indicator — a small square LED (TP-7 uses square indicators)
  let { tone = 'off', pulse = false, size = 7 } = $props();
  // tone: off | ok | err | warn | info | accent | semantic | episodic
</script>

<span class="dot {tone}" class:pulse style="--sz:{size}px"></span>

<style>
  .dot {
    width: var(--sz); height: var(--sz); flex-shrink: 0; display: inline-block;
    border-radius: 1px;
    background: var(--faint);
  }
  .dot.ok { background: var(--ok); }
  .dot.err { background: var(--err); }
  .dot.warn { background: var(--warn); }
  .dot.info { background: var(--info); }
  .dot.accent { background: var(--accent); }
  .dot.semantic { background: var(--semantic); }
  .dot.episodic { background: var(--episodic); }
  .dot.pulse { animation: dotpulse 1.4s ease-in-out infinite; }
  @keyframes dotpulse { 0%,100% { opacity: 1; } 50% { opacity: .35; } }
</style>
