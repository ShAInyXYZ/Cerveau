<script>
  import { Field, Dot } from '../kit/index.js';
  import { tooltip } from '../kit/tooltip.js';
  import { relTime } from './api';
  import { groupByProject } from './projects.js';
  import { Plus, Boxes, ChevronRight, Folder, FolderOpen, Pencil, Trash2, Zap, Settings as SettingsIcon } from 'lucide-svelte';

  let {
    sessions = [], activeId, activeWorkspace = '', lastEvents = {}, skills = [], runningIds = [],
    onSelect, onCreate, onRename, onDelete, onInstant, onSettings, settingsOpen = false
  } = $props();

  // rough "expires in" for an instant session (24h TTL from last activity)
  function expiresIn(s) {
    const base = s.last_seen || s.created;
    if (!base) return '';
    const ms = new Date(base).getTime() + 24 * 3600 * 1000 - Date.now();
    if (ms <= 0) return 'expiring';
    const h = Math.floor(ms / 3600000);
    return h >= 1 ? `expires in ${h}h` : `expires in <1h`;
  }

  // inline rename — double-click a session name to edit; id never changes
  let renamingId = $state(null);
  let renameVal = $state('');
  function startRename(e, s) {
    e.stopPropagation();
    renamingId = s.id; renameVal = s.name;
  }
  function commitRename() {
    const name = renameVal.trim();
    if (renamingId && name) onRename?.(renamingId, name);
    renamingId = null;
  }

  const projects = $derived(groupByProject(sessions));

  let expanded = $state({});
  $effect(() => {
    if (activeWorkspace && expanded[activeWorkspace] === undefined) {
      expanded = { ...expanded, [activeWorkspace]: true };
    } else if (projects.length && Object.keys(expanded).length === 0) {
      expanded = { [projects[0].path]: true };
    }
  });
  function toggle(path) { expanded = { ...expanded, [path]: !expanded[path] }; }

  // inline new-session, scoped to a specific project (workspace)
  let creatingIn = $state(null);   // project path currently adding a session
  let newName = $state('');
  function startCreate(e, path) {
    e.stopPropagation();
    creatingIn = path;
    if (!expanded[path]) expanded = { ...expanded, [path]: true };
  }
  function submit() {
    if (newName.trim() && creatingIn) onCreate(newName.trim(), creatingIn);
    newName = ''; creatingIn = null;
  }
  function lastLabel(id) {
    const ev = lastEvents[id];
    return ev ? `${ev.type.replace('msg.', '').replace('.', ' ')} · ${relTime(ev.ts)}` : '';
  }
</script>

<aside class="rail">
  <div class="railhead">
    <span class="label">PROJECTS</span>
    <span class="tag">{projects.length}</span>
    <button class="instant-btn" onclick={() => onInstant?.()} aria-label="instant session"
      use:tooltip={'ephemeral scratch session · auto-deletes in 24h'}>
      <Zap size={13} strokeWidth={2.3} />
    </button>
  </div>

  <div class="tree">
    {#each projects as p (p.path)}
      {@const isActiveProj = p.path === activeWorkspace}
      <div class="project">
        <button class="pfolder" class:active={isActiveProj} class:instant={p.instant}
          onclick={() => toggle(p.path)} use:tooltip={p.instant ? 'ephemeral · auto-deletes in 24h' : p.path}>
          <span class="chev" class:open={expanded[p.path]}><ChevronRight size={13} /></span>
          <span class="ficon">
            {#if p.instant}<Zap size={14} />{:else if expanded[p.path]}<FolderOpen size={14} />{:else}<Folder size={14} />{/if}
          </span>
          <span class="pname">{p.name}</span>
          {#if !p.instant}
            <span class="padd" use:tooltip={"new session in this project"}
              onclick={(e) => startCreate(e, p.path)} role="button" tabindex="0">
              <Plus size={13} />
            </span>
          {/if}
        </button>

        {#if expanded[p.path]}
          <div class="sessions">
            {#if creatingIn === p.path}
              <div class="screate anim-rise">
                <Field bind:value={newName} placeholder="session name…" onenter={submit}
                  onkeydown={(e) => e.key === 'Escape' && (creatingIn = null)} />
              </div>
            {/if}
            {#each p.sessions as s (s.id)}
              <button class="sess" class:on={s.id === activeId} onclick={() => onSelect(s.id)}>
                {#if runningIds.includes(s.id)}
                  <span class="sdot live" title="a turn is running in this session"></span>
                {:else if s.id === activeId}<Dot tone="accent" size={5} />{:else}<span class="sdot"></span>{/if}
                <span class="scol">
                  {#if renamingId === s.id}
                    <!-- svelte-ignore a11y_autofocus -->
                    <input
                      class="srename" bind:value={renameVal} autofocus
                      onclick={(e) => e.stopPropagation()}
                      onblur={commitRename}
                      onkeydown={(e) => {
                        e.stopPropagation();
                        if (e.key === 'Enter') commitRename();
                        if (e.key === 'Escape') renamingId = null;
                      }} />
                  {:else}
                    <span class="sname" ondblclick={(e) => startRename(e, s)}
                      use:tooltip={'double-click to rename'}>{s.name}</span>
                  {/if}
                  <span class="smeta mono" class:ttl={s.instant}>
                    {s.instant ? expiresIn(s) : (lastLabel(s.id) || s.id.slice(0, 15))}
                  </span>
                </span>
                {#if renamingId !== s.id}
                  <span class="sedit" onclick={(e) => startRename(e, s)} role="button" tabindex="0"
                    use:tooltip={'rename'}><Pencil size={11} /></span>
                  <span class="sedit del" onclick={(e) => { e.stopPropagation(); onDelete?.(s); }} role="button" tabindex="0"
                    use:tooltip={'delete'}><Trash2 size={11} /></span>
                {/if}
              </button>
            {/each}
          </div>
        {/if}
      </div>
    {/each}

    {#if projects.length === 0}
      <div class="empty label">NO PROJECTS YET</div>
    {/if}
  </div>

  <div class="railfoot">
    {#if skills.length}
      <div class="skills" use:tooltip={"loaded soft-skills"}>
        <Boxes size={12} /><span class="label">SKILLS</span><span class="tag">{skills.length}</span>
      </div>
    {/if}
    <button class="settings-btn" class:on={settingsOpen} onclick={() => onSettings?.()}>
      <SettingsIcon size={14} /><span>Settings</span>
    </button>
  </div>
</aside>

<style>
  .rail {
    width: var(--rail-w); flex-shrink: 0;
    display: flex; flex-direction: column; min-height: 0;
    background: var(--s1);
    border-right: 1px solid var(--line);
  }
  .railhead {
    display: flex; align-items: center; gap: 8px;
    height: 34px; flex-shrink: 0; padding: 0 12px;
    border-bottom: 1px solid var(--line);
  }
  .instant-btn {
    margin-left: auto; display: inline-flex; align-items: center; justify-content: center;
    width: 26px; height: 26px; color: var(--dim);
    background: var(--s2); border: 1px solid var(--line2);
    border-radius: 7px; cursor: pointer;
    transition: color .1s, background .1s, border-color .1s, transform .05s;
  }
  .instant-btn:hover { color: var(--text); background: var(--s3); border-color: var(--faint); }
  .instant-btn:active { transform: translateY(1px); }
  .screate { padding: 2px 0 4px; }

  .tree { flex: 1; overflow-y: auto; padding: 10px 8px; display: flex; flex-direction: column; gap: 8px; }
  .project { display: flex; flex-direction: column; }

  /* ---- project folder header: a rounded PILL (like the chat bar) ---- */
  .pfolder {
    width: 100%; display: flex; align-items: center; gap: 8px;
    padding: 8px 12px; border: none; border-radius: 999px;
    background: var(--surface-raised);
    box-shadow: var(--elev-1);
    color: var(--muted); cursor: pointer;
    transition: box-shadow .12s, color .12s;
  }
  .pfolder:hover { color: var(--text); }
  .pfolder.active {
    color: var(--text);
    box-shadow: 0 0 0 1px var(--accent-line), 0 1px 0 0 var(--lift) inset;
  }
  .chev { display: inline-flex; color: var(--faint); transition: transform .15s; flex-shrink: 0; }
  .chev.open { transform: rotate(90deg); color: var(--dim); }
  .ficon { display: inline-flex; color: var(--dim); flex-shrink: 0; }
  .pfolder.active .ficon { color: var(--accent); }
  /* instant group — always reads as the ephemeral/accent one */
  .pfolder.instant .ficon { color: var(--accent); }
  .pfolder.instant .pname { color: var(--accent); }
  .pname { flex: 1; text-align: left; font-size: 12px; font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  /* per-project add: hidden until the pill is hovered */
  .padd {
    display: inline-flex; align-items: center; justify-content: center;
    width: 22px; height: 22px; flex-shrink: 0; border-radius: 6px;
    color: var(--dim); cursor: pointer;
    opacity: 0; transition: opacity .12s, color .1s, background .1s;
  }
  .pfolder:hover .padd { opacity: 1; }
  .padd:hover { color: var(--accent); background: var(--accent-soft); }

  /* A turn is executing in this session — including one started from the CLI,
     which the panel otherwise renders identically to an idle session. */
  .sdot.live {
    background: var(--ok, #7fa650);
    box-shadow: 0 0 0 0 var(--ok, #7fa650);
    animation: livepulse 1.6s ease-out infinite;
  }
  @keyframes livepulse {
    0%   { box-shadow: 0 0 0 0 rgba(127,166,80,.6); }
    70%  { box-shadow: 0 0 0 5px rgba(127,166,80,0); }
    100% { box-shadow: 0 0 0 0 rgba(127,166,80,0); }
  }

  /* ---- sessions nested as children beneath the pill ---- */
  .sessions {
    display: flex; flex-direction: column; gap: 1px;
    margin: 4px 0 0 20px; padding-left: 12px;
    border-left: 1px solid var(--line);
  }
  .sess {
    position: relative;
    display: flex; align-items: flex-start; gap: 8px;
    text-align: left; background: transparent; border: none; border-radius: 7px;
    padding: 7px 10px; cursor: pointer; color: var(--muted);
    transition: background .12s, box-shadow .12s;
  }
  /* connector tick from the guide line to each session */
  .sess::before {
    content: ''; position: absolute; left: -12px; top: 15px;
    width: 8px; height: 1px; background: var(--line);
  }
  .sess:hover { background: color-mix(in srgb, #fff 3.5%, transparent); }
  .sess.on { background: var(--surface-raised); box-shadow: var(--elev-1); }
  .sess.on::before { background: var(--accent-line); }
  .sdot { width: 5px; height: 5px; border-radius: 50%; background: var(--faint); margin-top: 5px; flex-shrink: 0; }
  .sess :global(.dot) { margin-top: 5px; }
  .scol { min-width: 0; flex: 1; display: flex; flex-direction: column; }
  .sname { font-size: 12px; font-weight: 500; color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .sess.on .sname { color: var(--text); font-weight: 550; }
  .smeta { font-size: 9px; color: var(--faint); margin-top: 2px; letter-spacing: .04em; }
  .smeta.ttl { color: var(--warn); }

  /* inline rename input — matches the name's slot exactly */
  .srename {
    font-size: 12px; font-weight: 500; color: var(--text);
    background: var(--bg); border: 1px solid var(--accent-line); border-radius: 5px;
    padding: 2px 6px; width: 100%; outline: none; font-family: var(--font-sans);
  }
  /* pencil affordance — hidden until the session row is hovered */
  .sedit {
    display: inline-flex; align-items: center; justify-content: center;
    width: 20px; height: 20px; flex-shrink: 0; border-radius: 5px; margin-top: 2px;
    color: var(--dim); cursor: pointer; opacity: 0;
    transition: opacity .12s, color .1s, background .1s;
  }
  .sess:hover .sedit { opacity: 1; }
  .sedit:hover { color: var(--accent); background: var(--accent-soft); }
  .sedit.del:hover { color: var(--err); background: color-mix(in srgb,var(--err) 12%,transparent); }

  .empty { padding: 24px 12px; text-align: center; }

  .railfoot { flex-shrink: 0; border-top: 1px solid var(--line); padding: 8px; display: flex; flex-direction: column; gap: 6px; }
  .settings-btn {
    display: flex; align-items: center; gap: 8px; width: 100%;
    text-align: left; padding: 8px 10px; border: none; border-radius: 8px;
    background: transparent; color: var(--dim); cursor: pointer;
    font-size: 12px; font-weight: 500;
    transition: color .12s, background .12s;
  }
  .settings-btn:hover { color: var(--text); background: color-mix(in srgb, #fff 6%, transparent); }
  .settings-btn.on { color: var(--accent); background: var(--accent-soft); }
  .foot {
    display: flex; align-items: center; gap: 8px;
    font-family: var(--font-mono); font-size: 10px; letter-spacing: .12em;
    padding: 8px 10px; border-radius: 7px;
    background: color-mix(in srgb, #fff 4%, transparent);
    box-shadow: inset 0 0 0 1px color-mix(in srgb, #fff 9%, transparent);
    color: var(--dim); cursor: pointer; border: none; transition: color .1s, box-shadow .1s;
  }
  .foot:hover { color: var(--text); }
  .foot.on { color: var(--accent); box-shadow: inset 0 0 0 1px var(--accent-line); background: var(--accent-soft); }
  .skills { display: flex; align-items: center; gap: 7px; padding: 4px 10px; color: var(--dim); }
</style>
