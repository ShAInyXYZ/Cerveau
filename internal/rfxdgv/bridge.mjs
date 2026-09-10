// Adapter only. All DGV graph semantics come from the unmodified upstream core.
import { pathToFileURL } from 'node:url';

const chunks = [];
for await (const chunk of process.stdin) chunks.push(chunk);
try {
  const req = JSON.parse(Buffer.concat(chunks).toString('utf8'));
  const core = await import(pathToFileURL(process.argv[1]).href);
  let doc = req.document;
  let result;
  const short = (v, n = 600) => typeof v === 'string' && v.length > n ? v.slice(0, n) + '… [truncated]' : v;
  const brief = (item) => Object.fromEntries(Object.entries(item).filter(([k]) => !['position', 'size', 'history'].includes(k)).map(([k, v]) => [k, short(v)]));
  const bounded = (items, n = req.limit) => ({ items: items.slice(0, n), total: items.length, truncated: items.length > n });
  const diagnostics = (doc) => {
    const all = core.lint(doc);
    return { summary: core.summarizeDiagnostics(all), ...bounded(all, 30) };
  };
  if (req.action === 'catalog') {
    result = core.catalogSummary();
  } else if (req.action === 'create' || req.action === 'update') {
    const previous = req.action === 'update' ? doc : null;
    if (req.action === 'update') {
      const patch = core.preparePatch(doc, req.patch);
      const next = core.applyPatch(doc, patch).doc;
      // Upstream applyPatch prunes dangling edges. A bounded upsert must not
      // silently drop either old or explicitly requested architecture.
      for (const edge of [...(doc.edges ?? []), ...(patch.edges ?? [])]) {
        if (!next.edges.some(e => e.id === edge.id)) throw new Error(`invalid_patch: edge ${edge.id} would be removed; fix its endpoints`);
      }
      doc = next;
    }
    const beforeLayout = diagnostics(doc);
    if (!beforeLayout.summary.ok) throw new Error('invalid_graph: ' + JSON.stringify(beforeLayout));
    doc = core.layout(core.normalize(doc), {}, { direction: 'LR' });
    const history = core.withHistory(previous, doc, { by: 'rfx' });
    result = { document: history.doc, diagnostics: diagnostics(history.doc), history_added: history.entries.length };
  } else if (req.action === 'check') {
    result = {};
    if (req.checks !== 'drift') result.lint = diagnostics(doc);
    if (req.checks !== 'lint') {
      const drift = core.drift(doc, req.files, { depth: 2 });
      result.drift = { ok: drift.ok, linked: drift.linked, summary: drift.summary, ...bounded(drift.findings, 30) };
    }
    result.ok = (result.lint?.summary.ok ?? true) && (result.drift?.ok ?? true);
  } else if (req.action === 'read') {
    const hit = core.findElement(core.normalize(doc), req.on);
    if (!hit) throw new Error(`not_found: no graph element ${req.on}`);
    result = { type: hit.type, element: hit.item, relations: bounded((doc.edges ?? []).filter(e => e.source === req.on || e.target === req.on), 20) };
  } else if (req.action === 'context') {
    const normalized = core.normalize(doc);
    let selected = normalized.nodes;
    if (req.focus) {
      if (!normalized.nodes.some(n => n.id === req.focus) && !normalized.frames.some(f => f.id === req.focus)) throw new Error(`not_found: no node or frame ${req.focus}`);
      const ids = new Set([req.focus]);
      for (const n of normalized.nodes) if (n.frame === req.focus) ids.add(n.id);
      for (const e of normalized.edges) if (e.source === req.focus || e.target === req.focus) { ids.add(e.source); ids.add(e.target); }
      selected = normalized.nodes.filter(n => ids.has(n.id));
      selected.sort((a, b) => Number(b.id === req.focus) - Number(a.id === req.focus));
    }
    const nodes = selected.slice(0, req.limit);
    const ids = new Set(nodes.map(n => n.id));
    const frames = new Set(nodes.map(n => n.frame).filter(Boolean));
    const sources = new Set(), missing = [], unmapped = [];
    for (const n of nodes) {
      const paths = n.path == null ? [] : Array.isArray(n.path) ? n.path : [n.path];
      if (!paths.length) unmapped.push(n.id);
      for (const pattern of paths) {
        const re = core.pathMatcher(pattern);
        const hits = req.files.filter(f => re.test(f));
        if (!hits.length) missing.push({ node: n.id, path: pattern });
        for (const f of hits) sources.add(f);
      }
    }
    result = {
      title: short(normalized.meta.title, 200), description: short(normalized.meta.description, 400),
      focus: req.focus || null,
      nodes: { items: nodes.map(brief), total: selected.length, truncated: selected.length > nodes.length },
      frames: bounded(normalized.frames.filter(f => frames.has(f.id)).map(brief)),
      edges: bounded(normalized.edges.filter(e => ids.has(e.source) && ids.has(e.target)).map(brief), 30),
      _sources: [...sources].sort(), _missing: missing, _unmapped: unmapped,
    };
  } else throw new Error('invalid_action');
  process.stdout.write(JSON.stringify(result));
} catch (err) {
  process.stderr.write(String(err.message).slice(0, 12000));
  process.exitCode = 1;
}
