# github — operator's card (2.0.0)

## Invocation patterns
- git-status {} → inspect the exact repository and file paths.
- git-diff {"scope":"all"} → review; untracked content needs an explicit file read.
- git-add {"paths":["specific file.txt"]} → git-diff {"scope":"staged"} → git-commit {"message":"type: summary"}.
- git-suggest {} returns staged review data; the current host model writes the suggestion, with no hidden inference.

## Failure → fix
- "nothing staged" → review and stage specific files; do not stage everything.
- "workspace must equal the repository root" → select the intended repo workspace.
- "repository-local name and email are required" → ask the user for identity, then git-profile.
- "HEAD differs from the reviewed commit" → review the new HEAD and obtain fresh approval.
- "human_approval_required" → use the host confirmation; argument flags cannot approve.
- "external_filter" → operator review is required; do not bypass with shell.

## Never
- Never initialize, switch accounts, commit, publish or push without user intent.
- Never stage directories, force-push, infer an owner, or treat a truncated patch as a full review.
- Never retry an unknown remote effect without inspecting GitHub first.

## Idioms
Git identity is local to this repository. Hooks/signing are disabled; linked worktrees are not supported. gh-accounts shows the active account only. Account switching is operator-only. git-publish creates but does not push; git-push is a separate host-approved action.
