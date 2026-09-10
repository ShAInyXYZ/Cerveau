<script>
  import { Field, Dot } from '../kit/index.js';
  import { tooltip } from '../kit/tooltip.js';
  import { relTime } from './api';
  import { groupByProject } from './projects.js';
  import { Plus, Boxes, ChevronRight, Pencil, Trash2, Zap, Settings as SettingsIcon } from 'lucide-svelte';

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
        <div class="folder-row" class:active={isActiveProj}>
          <button class="pfolder" class:active={isActiveProj}
            aria-expanded={!!expanded[p.path]}
            onclick={() => toggle(p.path)} use:tooltip={p.instant ? 'ephemeral · auto-deletes in 24h' : p.path}>
            {#if p.instant}<span class="ficon"><Zap size={14} /></span>{/if}
            <span class="pname">{p.name}</span>
            <span class="session-count" aria-label={`${p.sessions.length} sessions`}>{p.sessions.length}</span>
            <span class="chev" class:open={expanded[p.path]}><ChevronRight size={13} /></span>
          </button>
          {#if !p.instant}
            <button class="padd" use:tooltip={"new session in this project"}
              aria-label={`New session in ${p.name}`} onclick={(e) => startCreate(e, p.path)}>
              <Plus size={13} />
            </button>
          {/if}
        </div>

        {#if expanded[p.path]}
          <div class="sessions">
            {#if creatingIn === p.path}
              <div class="screate anim-rise">
                <Field bind:value={newName} placeholder="session name…" onenter={submit}
                  onkeydown={(e) => e.key === 'Escape' && (creatingIn = null)} />
              </div>
            {/if}
            {#each p.sessions as s (s.id)}
              <div class="sess" class:on={s.id === activeId}>
                {#if runningIds.includes(s.id)}
                  <span class="sdot live" role="img" aria-label="a turn is running in this session"></span>
                {:else if s.id === activeId}<Dot tone="accent" size={5} />{:else}<span class="sdot"></span>{/if}
                <div class="scol">
                  {#if renamingId === s.id}
                    <!-- svelte-ignore a11y_autofocus -->
                    <input
                      class="srename" bind:value={renameVal} autofocus aria-label="Session name"
                      onblur={commitRename}
                      onkeydown={(e) => {
                        e.stopPropagation();
                        if (e.key === 'Enter') commitRename();
                        if (e.key === 'Escape') renamingId = null;
                      }} />
                    <span class="smeta mono" class:ttl={s.instant}>
                      {s.instant ? expiresIn(s) : (lastLabel(s.id) || s.id.slice(0, 15))}
                    </span>
                  {:else}
                    <button class="sselect" aria-current={s.id === activeId ? 'true' : undefined}
                      onclick={() => onSelect(s.id)} ondblclick={(e) => startRename(e, s)}
                      use:tooltip={'double-click to rename'}>
                      <span class="sname">{s.name}</span>
                      <span class="smeta mono" class:ttl={s.instant}>
                        {s.instant ? expiresIn(s) : (lastLabel(s.id) || s.id.slice(0, 15))}
                      </span>
                    </button>
                  {/if}
                </div>
                {#if renamingId !== s.id}
                  <div class="sactions">
                    <button class="sedit" onclick={(e) => startRename(e, s)} aria-label={`Rename ${s.name}`}
                      use:tooltip={'rename'}><Pencil size={11} /></button>
                    <button class="sedit del" onclick={() => onDelete?.(s)} aria-label={`Delete ${s.name}`}
                      use:tooltip={'delete'}><Trash2 size={11} /></button>
                  </div>
                {/if}
              </div>
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
    <button class="settings-btn" class:on={settingsOpen} aria-pressed={settingsOpen} onclick={() => onSettings?.()}>
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
    /* No border-bottom: the app header above already draws a full-width rule,
       and a second one 34px under it read as a thick, uneven double edge. */
  }
  .instant-btn {
    margin-left: auto; display: inline-flex; align-items: center; justify-content: center;
    width: 28px; height: 28px; color: var(--muted);
    background: transparent; border: none;
    border-radius: var(--r); cursor: pointer;
    transition: color var(--t-fast), background var(--t-fast);
  }
  .instant-btn:hover { color: var(--text); background: var(--s2); }
  .instant-btn:active { background: var(--s3); }
  .screate { padding: 2px 0 4px; }

  .tree { flex: 1; overflow-y: auto; padding: var(--sp-4); display: flex; flex-direction: column; gap: var(--sp-1); }
  .project { display: flex; flex-direction: column; }

  /* Secondary navigation stays flat; only selection and focus carry emphasis. */
  .folder-row { display: flex; align-items: center; min-width: 0; border-radius: var(--r-control); padding-right: var(--sp-2); }
  .pfolder {
    flex: 1; min-width: 0; min-height: 32px; display: flex; align-items: center; gap: var(--sp-4);
    padding: var(--sp-4) var(--sp-5); border: none; border-radius: var(--r-control);
    background: transparent;
    color: var(--muted); cursor: pointer;
    transition: color var(--t-fast);
  }
  .folder-row:hover { background: var(--s2); }
  .folder-row.active { background: var(--surface-raised); box-shadow: inset 0 0 0 1px var(--accent-line); }
  .pfolder:hover { color: var(--text); }
  .pfolder.active { color: var(--text); }
  .chev { display: inline-flex; color: var(--muted); transition: transform var(--t-fast), opacity var(--t-fast); flex-shrink: 0; opacity: 0; }
  .chev.open { transform: rotate(90deg); opacity: 1; }
  .folder-row:hover .chev, .folder-row:focus-within .chev { opacity: 1; }
  .session-count { font: var(--fs-small) var(--font-mono); color: var(--muted); }
  .ficon { display: inline-flex; color: var(--dim); flex-shrink: 0; }
  .pfolder.active .ficon { color: var(--muted); }
  .pname { flex: 1; text-align: left; font-size: var(--fs-small); font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .pfolder.active .pname { font-weight: 600; }
  /* Native sibling controls remain reachable without nested buttons. */
  .padd {
    display: inline-flex; align-items: center; justify-content: center;
    width: 28px; height: 28px; flex-shrink: 0; border-radius: var(--r-control);
    padding: 0; border: none; background: transparent; color: var(--muted); cursor: pointer;
    opacity: 0; transition: opacity var(--t-fast), color var(--t-fast), background var(--t-fast);
  }
  .folder-row:hover .padd, .folder-row:focus-within .padd { opacity: 1; }
  .padd:hover { color: var(--text); background: var(--s3); }

  /* A turn is executing in this session — including one started from the CLI,
     which the panel otherwise renders identically to an idle session. */
  .sdot.live {
    background: var(--ok);
  }

  /* Indentation and one quiet guide communicate project/session nesting. */
  .sessions {
    display: flex; flex-direction: column; gap: var(--sp-1);
    margin: var(--sp-1) 0 var(--sp-2) var(--sp-6); padding-left: var(--sp-2);
    border-left: 1px solid var(--s3);
  }
  .sess {
    position: relative;
    display: flex; align-items: flex-start; gap: var(--sp-3);
    text-align: left; background: transparent; border-radius: var(--r);
    padding: 0 var(--sp-3); color: var(--muted); min-height: 44px;
    transition: background var(--t-fast);
  }
  .sess:hover { background: var(--s2); }
  .sess.on { background: transparent; color: var(--text); }
  .sess.on::before { content: ''; position: absolute; left: -5px; top: var(--sp-4); bottom: var(--sp-4); width: 1px; background: var(--accent); }
  .sdot { width: 5px; height: 5px; border-radius: 50%; background: var(--faint); margin-top: 13px; flex-shrink: 0; }
  .sess :global(.dot) { margin-top: 13px; }
  .scol { min-width: 0; flex: 1; display: flex; flex-direction: column; }
  .sselect { display: flex; flex-direction: column; width: 100%; min-height: 44px; text-align: left; background: transparent; border: none; border-radius: var(--r); padding: var(--sp-3) 0; cursor: pointer; }
  .sname { display: block; width: 100%; font-size: var(--fs-small); font-weight: 500; color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .sess.on .sname { color: var(--text); font-weight: 550; }
  .smeta { max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: var(--fs-micro); color: var(--muted); margin-top: var(--sp-1); letter-spacing: .04em; }
  .smeta.ttl { color: var(--warn); }

  /* inline rename input — matches the name's slot exactly */
  .srename {
    font-size: var(--fs-small); font-weight: 500; color: var(--text);
    background: var(--bg); border: 1px solid var(--line2); border-radius: var(--r);
    padding: var(--sp-1) var(--sp-3); margin-top: var(--sp-2); width: 100%; font-family: var(--font-sans);
  }
  .sactions { display: flex; align-items: center; gap: var(--sp-1); margin-top: var(--sp-2); }
  .sedit {
    display: inline-flex; align-items: center; justify-content: center;
    width: 24px; height: 28px; flex-shrink: 0; border-radius: var(--r);
    padding: 0; border: none; background: transparent; color: var(--muted); cursor: pointer; opacity: 0;
    transition: opacity var(--t-fast), color var(--t-fast), background var(--t-fast);
  }
  .sess:hover .sedit, .sess:focus-within .sedit { opacity: 1; }
  .sedit:hover { color: var(--text); background: var(--s3); }
  .sedit.del:hover { color: var(--err); background: color-mix(in srgb,var(--err) 12%,transparent); }
  @media (hover: none) { .padd, .sedit { opacity: 1; } }

  .empty { padding: 24px 12px; text-align: center; }

  .railfoot { flex-shrink: 0; border-top: 1px solid var(--line); padding: 8px; display: flex; flex-direction: column; gap: 6px; }
  .settings-btn {
    display: flex; align-items: center; gap: 8px; width: 100%;
    text-align: left; padding: var(--sp-4); border: none; border-radius: var(--r); min-height: 32px;
    background: transparent; color: var(--muted); cursor: pointer;
    font-size: var(--fs-small); font-weight: 500;
    transition: color var(--t-fast), background var(--t-fast);
  }
  .settings-btn:hover { color: var(--text); background: var(--s2); }
  .settings-btn.on { color: var(--accent); background: var(--accent-soft); }
  .skills { display: flex; align-items: center; gap: 7px; padding: 4px 10px; color: var(--dim); }
</style>
