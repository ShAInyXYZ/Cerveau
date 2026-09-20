<script>
  import { Field, Dot } from '../kit/index.js';
  import { tooltip } from '../kit/tooltip.js';
  import { relTime } from './api';
  import { groupByProject, filterProjects, projectName } from './projects.js';
  import WorkspacePicker from './WorkspacePicker.svelte';
  import { Plus, Boxes, ChevronRight, Pencil, Trash2, Zap, Folder, FolderPlus, Search, X, Settings as SettingsIcon } from 'lucide-svelte';

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

  const grouped = $derived(groupByProject(sessions));

  // A project is a folder that has a session, so "Add project" is: pick the
  // folder, then name its first session. Until that session exists the folder
  // is only `pending` — shown at the top with the name field open, and gone
  // again if the user backs out.
  let picking = $state(false);
  let pending = $state('');
  const all = $derived(
    pending && !grouped.some((p) => p.path === pending)
      ? [{ path: pending, name: projectName(pending), sessions: [], pending: true }, ...grouped]
      : grouped);

  // With ninety projects the list is only usable if it can be narrowed.
  let query = $state('');
  const projects = $derived(filterProjects(all, query));
  const searching = $derived(query.trim().length > 0);
  const shownSessions = $derived(projects.reduce((n, p) => n + p.sessions.length, 0));

  let expanded = $state({});
  $effect(() => {
    if (activeWorkspace && expanded[activeWorkspace] === undefined) {
      expanded = { ...expanded, [activeWorkspace]: true };
    } else if (projects.length && Object.keys(expanded).length === 0) {
      expanded = { [projects[0].path]: true };
    }
  });
  function toggle(path) { expanded = { ...expanded, [path]: !expanded[path] }; }
  // a search opens what it found; clearing it returns the tree as it was left
  const isOpen = (p) => searching || !!expanded[p.path];

  function addProject(path) {
    picking = false;
    if (!path) return;
    pending = path; query = '';
    creatingIn = path; newName = '';
    expanded = { ...expanded, [path]: true };
  }
  function cancelCreate() { creatingIn = null; newName = ''; pending = ''; }

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
    newName = ''; creatingIn = null; pending = '';
  }
  function lastLabel(id) {
    const ev = lastEvents[id];
    return ev ? `${ev.type.replace('msg.', '').replace('.', ' ')} · ${relTime(ev.ts)}` : '';
  }
</script>

<aside class="rail">
  <div class="railhead">
    <span class="label">PROJECTS</span>
    <span class="total mono">{grouped.length}</span>
    <button class="instant-btn" onclick={() => onInstant?.()} aria-label="instant session"
      use:tooltip={'ephemeral scratch session · auto-deletes in 24h'}>
      <Zap size={13} strokeWidth={2.3} />
    </button>
  </div>

  <div class="actions">
    <button class="add-project" onclick={() => (picking = true)}>
      <FolderPlus size={14} /><span>Add project</span>
    </button>
  </div>

  <div class="find">
    <Search size={13} />
    <input type="text" bind:value={query} placeholder="Find a project or session…" spellcheck="false"
      aria-label="Find a project or session" onkeydown={(e) => e.key === 'Escape' && (query = '')} />
    {#if searching}
      <button class="clear" onclick={() => (query = '')} aria-label="clear the search"><X size={12} /></button>
    {/if}
  </div>
  <!-- only while searching: at rest the chip in the header already says how many -->
  <div class="status mono" role="status">
    {#if searching}{projects.length} of {all.length} projects · {shownSessions} session{shownSessions === 1 ? '' : 's'}{/if}
  </div>

  <div class="tree">
    {#each projects as p (p.path)}
      {@const isActiveProj = p.path === activeWorkspace}
      <div class="project">
        <!-- chevron · folder + name + count · new session. Three native
             buttons side by side, so none is nested in another and each is
             reachable by keyboard. -->
        <div class="folder-row" class:active={isActiveProj}>
          <button class="ptoggle" aria-expanded={isOpen(p)} aria-label={`${isOpen(p) ? 'Collapse' : 'Expand'} ${p.name}`}
            onclick={() => toggle(p.path)}>
            <span class="chev" class:open={isOpen(p)}><ChevronRight size={13} /></span>
          </button>
          <button class="pfolder" class:active={isActiveProj} onclick={() => toggle(p.path)}
            use:tooltip={p.instant ? 'ephemeral · auto-deletes in 24h' : p.path}>
            <span class="ficon">{#if p.instant}<Zap size={13} />{:else}<Folder size={13} />{/if}</span>
            <span class="pname">{p.name}</span>
            <span class="session-count mono" aria-label={`${p.sessions.length} sessions`}>{p.sessions.length}</span>
          </button>
          {#if !p.instant}
            <button class="padd" use:tooltip={"new session in this project"}
              aria-label={`New session in ${p.name}`} onclick={(e) => startCreate(e, p.path)}>
              <Plus size={13} />
            </button>
          {:else}<span class="padd-gap"></span>{/if}
        </div>

        {#if isOpen(p)}
          <div class="sessions">
            {#if creatingIn === p.path}
              <div class="screate anim-rise">
                <Field bind:value={newName} placeholder={p.pending ? 'name its first session…' : 'session name…'} onenter={submit}
                  onkeydown={(e) => e.key === 'Escape' && cancelCreate()} />
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
            {#if !p.sessions.length && creatingIn !== p.path}
              <div class="nosess">No sessions yet.</div>
            {/if}
          </div>
        {/if}
      </div>
    {/each}

    {#if projects.length === 0}
      <div class="empty">
        {#if searching}Nothing matches “{query.trim()}”.{:else}No projects yet. Add a folder to start one.{/if}
      </div>
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

<WorkspacePicker bind:open={picking} current={activeWorkspace} onPick={addProject} />

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
  /* the count sits in a chip so it reads as a total, not as part of the label */
  .total {
    display: inline-flex; align-items: center; justify-content: center;
    min-width: 18px; height: 18px; padding: 0 5px; border-radius: 4px;
    background: var(--s2); color: var(--muted); font-size: 9px;
  }

  .actions { flex-shrink: 0; padding: 0 var(--sp-4) var(--sp-4); }
  .add-project {
    display: flex; align-items: center; gap: var(--sp-4); width: 100%; min-height: 34px;
    padding: 0 var(--sp-5); border: none; border-radius: 6px; cursor: pointer;
    background: var(--s2); box-shadow: var(--elev-1); color: var(--text);
    font: inherit; font-size: var(--fs-small); font-weight: 550;
    transition: background var(--t-fast);
  }
  .add-project :global(svg) { color: var(--accent); flex-shrink: 0; }
  .add-project:hover { background: var(--s3); }

  .find { position: relative; flex-shrink: 0; margin: 0 var(--sp-4); color: var(--muted); }
  .find > :global(svg) { position: absolute; left: 9px; top: 10px; pointer-events: none; }
  .find input {
    width: 100%; height: 32px; box-sizing: border-box; padding: 0 26px 0 29px;
    border: none; border-radius: 6px; background: var(--s2); box-shadow: inset 0 0 0 1px var(--ring);
    color: var(--text); font: inherit; font-size: var(--fs-small);
  }
  .find input::placeholder { color: var(--dim); }
  .find input:focus { outline: none; box-shadow: inset 0 0 0 1px var(--accent-line); }
  .clear {
    position: absolute; right: 4px; top: 4px; width: 24px; height: 24px;
    display: inline-flex; align-items: center; justify-content: center;
    border: none; border-radius: var(--r); background: transparent; color: var(--muted); cursor: pointer;
  }
  .clear:hover { color: var(--text); background: var(--s3); }
  .status { flex-shrink: 0; padding: var(--sp-3) var(--sp-5) 0; font-size: var(--fs-micro); color: var(--dim); }
  .status:empty { padding: var(--sp-3) 0 0; }

  .screate { padding: 2px 0 4px; }

  .tree { flex: 1; overflow-y: auto; padding: var(--sp-2) var(--sp-4) var(--sp-7); display: flex; flex-direction: column; gap: var(--sp-1); }
  .project { display: flex; flex-direction: column; }

  /* A row is three controls on one surface: chevron, the project, new session.
     Flat at rest; only selection and focus carry emphasis. The + and the
     chevron are always there — hidden-until-hover left the counts floating in
     mid-row with nothing to line up against. */
  .folder-row {
    display: grid; grid-template-columns: 26px minmax(0, 1fr) 28px; align-items: center;
    min-width: 0; border-radius: 6px; padding: 0 var(--sp-1);
  }
  .folder-row:hover { background: var(--s2); }
  .folder-row.active { background: var(--surface-raised); box-shadow: inset 0 0 0 1px var(--accent-line); }
  .ptoggle, .padd {
    display: inline-flex; align-items: center; justify-content: center;
    width: 26px; height: 28px; padding: 0; border: none; border-radius: 6px;
    background: transparent; color: var(--dim); cursor: pointer;
    transition: color var(--t-fast), background var(--t-fast);
  }
  .padd { width: 28px; }
  .ptoggle:hover, .padd:hover { color: var(--text); background: var(--s3); }
  .padd-gap { width: 28px; }
  .chev { display: inline-flex; transition: transform var(--t-fast); }
  .chev.open { transform: rotate(90deg); }
  .pfolder {
    min-width: 0; min-height: 32px; display: flex; align-items: center; gap: var(--sp-3);
    padding: 0 var(--sp-2) 0 0; border: none; background: transparent;
    color: var(--muted); cursor: pointer; font: inherit;
    transition: color var(--t-fast);
  }
  .pfolder:hover, .pfolder.active { color: var(--text); }
  .ficon { display: inline-flex; color: var(--dim); flex-shrink: 0; }
  .pfolder.active .ficon { color: var(--accent); }
  .pname { flex: 1; min-width: 0; text-align: left; font-size: var(--fs-small); font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .pfolder.active .pname { font-weight: 600; }
  .session-count { flex-shrink: 0; font-size: 9px; color: var(--dim); font-variant-numeric: tabular-nums; }
  .nosess { padding: var(--sp-4) var(--sp-3); font-size: var(--fs-small); color: var(--dim); }

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
  @media (hover: none) { .sedit { opacity: 1; } }
  /* a finger needs more than 28px */
  @media (pointer: coarse) {
    .pfolder { min-height: 44px; }
    .ptoggle, .padd { height: 40px; }
    .add-project { min-height: 44px; }
    .find input { height: 40px; }
    .find > :global(svg) { top: 14px; }
    .clear { top: 8px; }
  }

  .empty { padding: 24px 12px; text-align: center; font-size: var(--fs-small); color: var(--dim); line-height: 1.5; }

  .railfoot { flex-shrink: 0; border-top: 1px solid var(--line); padding: 8px; display: flex; flex-direction: column; gap: 6px; }
  .settings-btn {
    display: flex; align-items: center; gap: 8px; width: 100%;
    text-align: left; padding: var(--sp-4); border: none; border-radius: var(--r); min-height: 32px;
    background: transparent; color: var(--muted); cursor: pointer;
    font-size: var(--fs-small); font-weight: 500;
    transition: color var(--t-fast), background var(--t-fast);
  }
  .settings-btn:hover { color: var(--text); background: var(--s2); }
  /* open is a state, not an alarm: the block of accent read as an error */
  .settings-btn.on { color: var(--text); background: var(--s2); }
  .settings-btn.on :global(svg) { color: var(--accent); }
  .skills { display: flex; align-items: center; gap: 7px; padding: 4px 10px; color: var(--dim); }
</style>
