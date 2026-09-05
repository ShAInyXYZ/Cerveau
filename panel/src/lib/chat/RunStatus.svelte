<script lang="ts">
 import { sessionStore as s } from '../stores/session.svelte.ts';
 const run=$derived(s.run);
</script>
<div class="run-status">
 {#if s.connectionLost}<p role="status">Connection lost; run state unknown. Showing the last confirmed state. Reconnecting…</p>{/if}
 {#if s.requestError}<p role="alert">{s.requestError} Your input has been kept.</p>{/if}
 {#if run}
  <div class="line">
   <span role="status">{run.status.replaceAll('_',' ')}{run.phase ? ` · ${run.phase.replaceAll('_',' ')}` : ''}{run.tool ? ` · ${run.tool}` : ''}{run.step>=0 ? ` · step ${run.step+1}` : ''}</span>
   {#if s.running}
    {#if run.status==='paused' || run.status==='pause_requested'}
     <button disabled={s.connectionLost} onclick={()=>s.resume()}>Resume</button>
    {:else}<button disabled={s.connectionLost || run.status==='cancelling'} onclick={()=>s.pause()}>Pause</button>{/if}
    <button disabled={s.connectionLost || run.status==='cancelling'} onclick={()=>s.kill()}>Stop</button>
   {:else if s.plan && !s.plan.done && s.plan.blocked<0}
    <button onclick={()=>s.runAutopilot()}>Continue plan</button>
   {/if}
  </div>
  {#if s.running}<small>Run settings: {run.thinking_effort} / {run.thinking_mode} · sampling {run.sampling} · {run.calls} model calls</small>{/if}
 {/if}
</div>
<style>
 .run-status{margin:0 var(--dock-inset) 8px;color:var(--text);font-size:12px;overflow-wrap:anywhere}
 .line{display:flex;flex-wrap:wrap;gap:8px;align-items:center}.line span{flex:1}
 button{font:inherit;color:var(--text);background:var(--s2);border:1px solid var(--line2);border-radius:6px;padding:5px 9px;cursor:pointer}
 button:disabled{opacity:.5;cursor:default}button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
 small{display:block;margin-top:4px;color:var(--muted)}p{color:var(--err);margin:6px 0}
 @media(max-width:640px){.run-status{margin-inline:0}}
</style>
