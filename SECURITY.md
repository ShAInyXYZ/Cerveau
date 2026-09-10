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

- **Loopback clients are trusted.** The default bind is `127.0.0.1:7700`.
  A fresh, unpaired local setup does not require authentication. Keep the
  explicit loopback address; do not substitute `:7700` or a wildcard bind.
- **The agent can execute shell commands.** Ordinary Autopilot `bash` calls
  run with the workspace as their working directory, but retain the OS user's
  permissions elsewhere. Dedicated workspace file tools constrain their
  paths; this does not make the entire agent a sandbox. Treat API access as
  equivalent to shell access to your machine.
- **Some procedures have additional isolation.** Recovery shell calls and
  native check/debug procedures require Bubblewrap and fail closed when their
  isolation is unavailable. Read-only host access prevents writes, not reads
  of private files; it is not a confidentiality sandbox. Do not assume these
  restrictions apply to every tool or external RFX process.
- **Remote access requires deliberate configuration.** Paired remote API
  requests use a bearer token and device-signature checks; local operator
  access remains trusted. Protect configuration files, pairing invitations
  and device keys. Use a trusted encrypted tunnel and review proxy forwarding
  behavior rather than exposing the plain HTTP API publicly.

## What is *not* a vulnerability

- **Guard bypass by obfuscation.** The dispatch guard blocks obvious destructive
  commands (`rm -rf /`, force-push, `DROP TABLE`, …) by pattern-matching
  arguments. It is a safety floor against fat-finger mistakes and blatant abuse,
  **not a sandbox** — a determined, obfuscated command can evade it. Real
  isolation is the OS user's responsibility (run Cerveau as an unprivileged
  user; consider a container or VM for untrusted workloads).

Authentication, confinement or isolation behavior that does not match the
documented boundary should still be reported privately. Misconfiguration is
not a reason to dismiss a genuine implementation vulnerability.

## Supported versions

Cerveau is pre-1.0. Only the latest `main` receives security fixes.
