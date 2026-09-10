# GitHub RFX 2.0.0 — repository-bound verbs

The canonical source is `rfx/github/`; `cmd/rfx-github` supplies its compiled
runtime. This replaces the installed 1.7.0 shell pipelines when the operator
installs the new pack and helper. Development does not alter installed copies,
global Git identity, GitHub accounts, or the working repository.

## Boundaries

Every talent receives one strictly decoded JSON object on stdin. The helper
invokes fixed `git`/`gh` argv, never model-supplied shell commands. It works only
when the session workspace is the exact canonical repository root. Parent
repositories, linked worktrees and external/symlinked `.git` directories are
refused; supporting them needs an explicit host capability, not a wider jail.

Inherited `GIT_*` values and global/system Git config do not influence commands.
Commit identity must be set locally. Hooks, signing, fsmonitor, external diff and
text conversion are disabled. Staging refuses configured clean/process filters
because they can execute programs. These restrictions do **not** establish an OS
sandbox for arbitrary third-party subprocess packs.

`git-status` returns success with `data.repository:false` in a non-repository
workspace. This expected state is not a failed plan step and never initializes
the folder automatically. Genuine command faults still fail visibly.

Read-only status checks are explicit initially: the panel must not repeatedly
start manual reflex runs just to poll its display.

## Migration from the twelve installed talents

| Legacy talent | 2.0.0 behavior | Authority |
| --- | --- | --- |
| `git-status` | Porcelain-v2 JSON, exact NUL-separated paths, unborn repo support, bounded file list | Read-only |
| `git-diff` | `scope: all / staged / worktree`; bounded patch and separate untracked paths | Read-only |
| `git-log` | `count: 1..50`; structured commits, empty unborn history | Read-only |
| `git-add` | Required `paths` array; literal files only, including tracked deletions; no `add -A` | Host-sensitive mutation |
| `git-commit` | Only the current staged index; no hidden staging, hooks or signing | Host-sensitive mutation |
| `git-init` | Exact workspace, branch main; rejects an existing/parent repo | Explicit user request, host-sensitive |
| `git-profile` | Repository-local name/email only | User-chosen identity, host-sensitive |
| `git-suggest` | Bounded staged review bundle, `model_called:false`; no hardcoded Core endpoint | Read-only; host owns inference |
| `gh-accounts` | Sanitized active github.com account, not a claim to enumerate every saved account | Read-only authenticated API |
| `gh-switch` | Removed from callable manifests; helper returns `operator_only` | Operator manages account selection |
| `git-push` | Explicit remote, destination branch and reviewed full `expected_head`; no force; github.com only | Explicit host-owned human approval |
| `git-publish` | Explicit OWNER/REPO, visibility, `expected_head`; creates origin but **does not push** | Explicit host-owned human approval |

Changed argument shapes and the account/suggestion behavior make this a pack
major-version change, not a silent 1.7 patch. The old custom panel must not be
copied onto these new schemas. The installed copy remains untouched until an
operator-approved migration; installing into an existing directory must remove
the obsolete `gh-switch` manifest only after retaining and verifying a backup.

## Network approval and failure semantics

The helper refuses push/publish unless `CRV_RFX_HUMAN_APPROVED=1` is injected by
the host from an actual human-approval context. This reserved value must be
scrubbed from ambient environment and manifest allowlists; `confirmed` in JSON
is not an approval channel. A standalone local operator can set the variable;
it is not an authentication mechanism against someone who already controls the
process environment. Guarding helper invocation remains the host's job.

Global Git credential helpers are intentionally not inherited. An HTTPS push
depending on such a helper can therefore fail even when the operator's usual
shell succeeds. `gh` account/create operations use its existing operator-owned
configuration; this pack does not run auth setup or rewrite global Git config.
Network installation/acceptance must verify the intended repository-local or
SSH authentication separately. Do not silently widen environment access to fix
an authentication failure.

Push checks the reviewed HEAD before using that exact OID, takes an explicit
destination ref, rejects multiple/non-GitHub/credential-bearing push URLs, and
never adds `--force`. Publish refuses an existing origin and separates repository
creation from upload. Failure after a network operation can have an unknown
effect; its receipt says to inspect remote state, not blindly retry.

No real push, repository publication, account switching, or live authenticated
GitHub acceptance test was performed during implementation. Temp-repository
tests verify fail-closed approval and pre-network validation only.

## Result contract for the host and panel

The helper writes exactly one JSON result and exits nonzero on failure:

```json
{"schema_version":1,"ok":true,"talent":"git-status","repository":"/workspace/repo","data":{"repository":true,"branch":"main","head":"...","unborn":false,"files":[{"path":"space name.txt","index":"M","worktree":"."}],"total_files":1},"truncated":false}
```

This illustrative status omits additional fields: upstream/ahead/behind,
repository-local identity, credential-redacted remotes and last commit. Renames
include `original_path`; untracked entries set `untracked:true`. Consumers parse
the JSON; they must not reconstruct paths or state with regexes. The v1 exec
wrapper preserves this object because it has no string `output` field.

Errors contain stable `code`, bounded `message`, `retry_safe` and, where
applicable, `effect`. `schema_version` versions results independently of RFX
manifest version. Status retains at most 100 exact paths and a 20 KB serialized
file budget; `total_files` preserves the actual count. Diff patches are capped
at 12 KB, history at 50 commits, subprocess capture at 256 KB, input at 32 KB,
and final JSON at 60 KB. Truncation is explicit and never cuts a JSON document
in half. Untracked file contents are not included in a Git diff.

The UI should offer explicit refresh, exact file checkboxes, staged/unstaged
review, local identity disclosure and host-owned confirmations. It must preserve
the current session/workspace identity while a request is in flight, show
bounded-result warnings, and distinguish missing repository from command error.
No account switch control or automatic staging is appropriate.

## Verification

`go test -race ./internal/rfxgithub ./cmd/rfx-github` exercises only temporary
repositories: spaces/dashes/glob/newline paths, untracked files, explicit staging,
unborn status/diff/log, rename/deletion, narrow workspace identity, lock failures,
output limits, inherited environment injection, hooks/filter refusal and JSON
protocol errors. Mutating-network tests stop before making network calls.
Tracked deletions remain stageable after their parent directories are removed:
the existing ancestor chain is checked without symlink traversal, then the exact
literal tracked path is verified. Missing untracked paths are still refused.

Install the helper alongside the Cerveau executable with its trusted directory
on PATH, and install `rfx/github` as a unit. A manifest uses the fixed argv
`[/usr/bin/env, rfx-github, <talent>]`; no caller-supplied path or executable is
accepted. Missing helper/dependency errors must remain visible. Do not label
manifest validation or these local integration tests as live GitHub verification.
