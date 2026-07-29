# Security Policy

## Reporting a vulnerability

Please report security issues **privately** — do not open a public issue.

Email **info@cerveau.sh** with:

- a description of the issue and its impact,
- steps to reproduce (or a proof of concept),
- the affected version / commit.

You'll get an acknowledgement as soon as reasonably possible. Please allow time
for a fix before any public disclosure.

## Scope & threat model

Cerveau is a **single-user, local-first** tool. Understand these properties
before deploying it:

- **The HTTP API is unauthenticated.** It binds to `127.0.0.1` by default for
  exactly this reason. Anyone who can reach the API can drive the agent.
- **The agent can execute shell commands** (in Autopilot mode). The `bash` tool
  is **not OS-sandboxed** — it runs with the workspace as its working directory,
  but it can read and write anywhere the OS user can (`cat /etc/passwd`,
  `~/.ssh`, etc.). Only the dedicated file tools (`read`/`write`/`edit`) are
  jailed to the workspace. Treat access to the API as equivalent to shell
  access to your machine, full stop. (A Landlock-based `bash` jail is on the
  roadmap; the guard is not a substitute for it.)
- **Do not expose the API to a network** (changing the bind address, a reverse
  proxy, port-forwarding) without putting your own authentication in front of
  it. Authenticated remote access is on the roadmap; until then, keep it local.

## What is *not* a vulnerability

- **Guard bypass by obfuscation.** The dispatch guard blocks obvious destructive
  commands (`rm -rf /`, force-push, `DROP TABLE`, …) by pattern-matching
  arguments. It is a safety floor against fat-finger mistakes and blatant abuse,
  **not a sandbox** — a determined, obfuscated command can evade it. Real
  isolation is the OS user's responsibility (run Cerveau as an unprivileged
  user; consider a container or VM for untrusted workloads).
- Issues that require the operator to have already exposed the unauthenticated
  API to untrusted networks against the guidance above.

## Supported versions

Cerveau is pre-1.0. Only the latest `main` receives security fixes.
