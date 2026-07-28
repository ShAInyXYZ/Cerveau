# Contributing to Cerveau

Contributions are welcome — bug reports, fixes, features, docs.

## Sign your work (DCO)

Cerveau uses the [Developer Certificate of Origin](https://developercertificate.org/)
instead of a CLA. It's one line, no signup, no paperwork: by signing off you
certify that you wrote the change (or otherwise have the right to submit it)
under the project's Apache-2.0 license.

Add a `Signed-off-by` line to every commit:

```
Signed-off-by: Your Name <you@example.com>
```

Git does it for you:

```bash
git commit -s -m "fix: whatever you fixed"
```

That's the whole process. PRs with unsigned commits will be asked to
`git rebase --signoff` before merge.

## Ground rules

- **One change per PR.** Small and reviewable beats big and heroic.
- **Match the codebase.** Go stdlib-first, no framework creep; the panel is
  Svelte 5 runes + the in-repo kit — use existing components before adding deps.
- **`go test ./...` must pass.** Bug fixes come with a regression test.
- **Honest output.** Tool feedback, errors and docs never claim more than what
  actually happened — this is a project principle, not a style preference.

## Naming

"Cerveau" is a project trademark (see [NOTICE](NOTICE)). Forks are welcome and
encouraged under the Apache-2.0 license — under a different name.
