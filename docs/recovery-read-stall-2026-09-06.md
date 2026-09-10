# Live recovery still stalls in diagnosis

Diagnosis recorded 2026-09-06. No harness implementation, installation, Core
change, session resumption or benchmark source edit was performed in this review.

## Evidence

- Installed revision: `a3a8367-dirty-9c527991491f`, embedded Planner 1.5.1.
- Session: `20260905-160857-minecraft`.
- Latest run: `f4538ef4dd60bbc3e2d674d0`, 02:53:47–02:57:55 UTC.
- Terminal state: suspended, `plan_blocked`, step index 2 (step 3).
- 16 model calls, all `finish_reason: tool_calls`; no empty length-stopped
  replies. The output-reissue fix was not exercised by this run.
- No `write`, `edit` or `apply_patch` calls. Recorded tool calls: 9 read,
  7 recovery_read, 1 grep, 6 Bash (including two harness verification calls).
  The model's Bash commands were inspection commands, not source writes.
- Maximum observed prompt: 38,343 tokens; total reported completion tokens:
  5,653. No context-overflow or output-limit failure was observed.
- world.js remains 34,639 bytes, SHA-256
  `c4d8de79fab002c1fb1935b130c2d3b7b15276690f6348a101ba54f4036be0c2`,
  identical to the pre-install broken file. This run did not introduce new damage.
- `evt_001509` / `evt_001510`: committed verification failed with the same
  `world.js:754 SyntaxError: Unexpected end of input`.
- `evt_001514`: report correctly distinguishes two awaiting recheck, one
  blocked, five not started. The plan/UI evidence-label fix is working.

## Findings

### 1. Explicit line reads advertise more than they return

`internal/tools/read.go:126` prints the requested range before constructing
the result. Lines 130–132 then cut the rendered text at 8,000 bytes, potentially
mid-line, without correcting the range header or giving an exact continuation.

Observed examples:

| Tool result | Header range | Last returned line |
| --- | --- | --- |
| evt_001382 | 1–340 | 163, partial |
| evt_001389 | 340–560 | 492, partial |
| evt_001421 | 160–340 | 319, partial |
| evt_001470 | 263–560 | 407, partial |

The model initially advanced to the requested next range, leaving gaps, then
spent later calls inspecting overlapping regions. The tool does append a
generic truncation warning, but its header still misstates returned coverage.
This is a concrete tool-contract defect, not proof that fixing it alone makes
the model solve the benchmark.

### 2. Journal search works, but source retrieval is expensive and noisy

`evt_001404` and `evt_001485` returned matching historical source events for
`const world = {`. In the second attempt, `evt_001490` read `evt_000901`;
`evt_001492` returned an older API object, `return world`, and closing brace.
The search was available and used; this was not a missing-tool failure.

However, `internal/loop/recovery.go:328` sorts hits oldest first. The query
`flushUpdates` at `evt_001502` ended its first page at `evt_000707`, with older
stubs and command text mixed with useful source. Search operates on raw JSON
tool payloads. An old write such as `evt_000052` contains 19,023 characters of
source but its full recorded payload is 41,294 bytes because parsed arguments
and raw arguments coexist. Reading that envelope does not directly provide a
source-focused view near the matching symbol. Hits do not carry source-field
offsets, file-version provenance or a paired success result.

Historical source remains evidence, not automatically a safe replacement.
The recovered old API tail contains unfinished later-step stubs. It cannot
justify blindly restoring the entire earlier file or marking meshing complete.

### 3. Recovery still restarts investigation instead of preserving its progress

Both attempts emitted the new four-round checkpoint (`evt_001390`,
`evt_001471`) but neither transitioned to a repair. `runStep` creates a fresh
model window per attempt (`internal/loop/autopilot.go:401`). The next recovery
brief carries bounded journal snippets, not a structured record of what source
was covered, which evidence was useful, and what remains to be inspected.

The second attempt searched for `function rebuildDirtyMeshes` and
`function rebuildDirty`, which returned no matches. It reached useful old API
source, then spent its final model call searching again. The ordinary eight-call
iteration guard stopped each attempt. A prompt checkpoint alone did not enforce
or reliably guide a transition from investigation to repair.

### 4. Shell success is still not proof of a successful syntax check

`evt_001365` contains the syntax error alongside `EXIT: 0`: the model piped a
syntax check through `head` and printed the later shell status. The harness did
not mistake this for passing the committed check. Preserve that distinction;
do not globally redefine arbitrary shell exit semantics from text matching.

## Proposed next repair — not implemented by this diagnosis

1. Make read coverage exact: bounded complete lines, actual returned range,
   explicit next line/byte cursor, and UTF-8-safe handling of oversized lines.
   Keep caps; never silently claim unseen lines were returned.
2. Provide decoded, source-focused recovery evidence around a matching symbol,
   with file/event identity, offsets, truncation flags and outcome provenance.
   Prefer relevant recent source; retain explicit access to older versions.
   Never infer that a recorded proposed write succeeded.
3. Persist a bounded recovery record keyed to plan, failed check and workspace
   version: inspected coverage, useful source references, attempted repairs,
   verification results and unresolved evidence. Carry this record across retry
   and restart rather than resetting the investigation. Keep model hypotheses
   separate from recorded facts.
4. Define an explicit diagnosis → targeted repair → verification transition.
   Budget exhaustion without a supported repair must produce a precise blocker;
   do not force an unsafe write or buy more broad rereads by simply raising caps.

Required regression evidence before another “ready to retry” handoff: reproduce
the misleading range headers and skipped coverage, source extraction from real
journal-shaped payloads, a retry/restart that retains acquired evidence, bounded
no-progress behavior, and repair followed by unchanged verification. A scripted
model that conveniently writes on cue does not establish real-model recovery.
Live validation should use a preserved throwaway copy, not silently repair or
resume the user's benchmark. Core tuning remains out of scope.

Recovery remains **not demonstrated effective for this failure**. Record that
in DGV; do not label the overall recovery objective complete because its smaller
unit tests pass.

## Targeted file-tool design follow-up — proposed, not implemented

The tools do not require the model to reread or regenerate an entire file for
a local change. `read` already accepts inclusive `from_line` / `to_line`, so
`{"path":"world.js","from_line":145,"to_line":175}` requests a small region
around an error at lines 155–165. `edit` accepts a unique `old_string` and its
`new_string`; `apply_patch` can group these replacements. Line-number prefixes
in read output are presentation, not source to include in an edit.

Internally, public read and edit load the file with `os.ReadFile`, and edit
writes the resulting bytes with `os.WriteFile`. That whole-file disk I/O is
distinct from sending the whole file to the model. The demonstrated failure
is unreliable returned coverage and repeated investigation, not a requirement
to regenerate the whole file.

Extend the existing tools rather than introduce many overlapping tools:

- Return accurate coverage and continuation plus a source-version identity.
  Preserve whole lines where possible; explicitly represent oversized-line
  fragments. Never present requested coverage as returned coverage.
- Offer bounded context around a diagnostic location. Existing `outline_file`
  and `find_symbol` can help when their index is current; source remains the
  authority. A diagnostic line is a starting point, not proof of the cause's
  location, particularly for unexpected EOF.
- Support a version-bound region handle for replacement, or an expected source
  version with old/new text. Reject stale or ambiguous targets with a small,
  fresh context result. Do not splice by bare line numbers after the file moves.
  Preserve unrelated edits and existing workspace/mode/backup protections.
- Return a bounded change receipt: actual changed region, new source version
  and diff. Record syntax-check and committed-test outcomes separately from
  successful file mutation. A successful edit is not a successful repair.
- Carry acquired regions and evidence references across recovery attempts,
  invalidating their current-source status when the source version changes.
  Reuse evidence without silently treating historical source as current.

This is a proposal for the next harness repair, not an implementation or a
claim that improved tools alone guarantee this model will finish the task.
