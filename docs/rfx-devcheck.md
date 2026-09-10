# DevCheck 0.1 — browser facts before visual guessing

DevCheck is a native RFX pack, not an MCP server. Three fixed procedures replace
open-ended browser scripting: inspect a local application, check declared DOM
assertions, and capture compact visual evidence. The model chooses an action and
fills typed slots; it does not invent a browser program or infer a DOM fact from
pixels. The user can invoke the same inspection and viewport-capture talents from
the pack's dock card.

This is the first bounded browser implementation. It is **not** a complete
application acceptance suite, desktop screenshot app, accessibility auditor, or
security sandbox. It never reports a screenshot as proof that an application works.

## Talents and outcomes

| Talent | Small input | Reported outcome |
|---|---|---|
| `devcheck-inspect` | Local URL; optional observation delay | `observed`: bounded DOM labels, console warnings/errors, page exceptions, HTTP failures and blocked requests |
| `devcheck-check` | Local URL; 1–20 CSS assertions | `passed` or `failed`: exactly the caller's assertions, with actual values and failure reasons |
| `devcheck-capture` | Local URL; page, selector or rectangle | `captured`: compressed JPEG artifact reference, dimensions, byte count and SHA-256 |

Infrastructure errors return `unverified`, a stable error code and a retained
receipt when the workspace is writable. A failed assertion exits nonzero, so the
host does not treat it as a successful tool execution. `observed` and `captured`
mean that evidence was collected, **not** that the application passed. Even
`passed` covers only the declared checks; unrelated console errors are still
reported and are not silently converted into an acceptance verdict.

Checks support `exists`, `visible`, `text` and `count`. `text` compares exact
trimmed text with internal whitespace normalized to one space. Text and visibility
require exactly one matching element: ambiguous selectors fail rather than silently
choosing the first element. Use an explicit negative `exists` assertion for a
missing element. No JavaScript, XPath, regex execution, clicks, typing or shell
arguments are accepted. Browser-native CSS selectors are data, not executable code.

The inspection list is a compact DOM-label inventory, **not** a computed
accessibility tree or a WCAG verdict. It does not collect input values, cookies,
storage, HTML dumps or an existing user profile. Visible page text, console
messages and screenshots may nevertheless contain sensitive application data;
inspect only an application the user placed in scope.

## Browser and network boundary

- Targets must explicitly name `http://localhost:PORT` or
  `http://127.0.0.1:PORT`, with port 1024–65535. No default ports, credentials,
  fragments, alternate numeric-IP encodings, remote hosts, HTTPS or CDP endpoints.
- Each call launches a fresh headless Chromium and isolated anonymous context.
  No persistent profile, authentication state, shared browser session or automatic
  dependency installation is used.
- Browser routing allows only **the exact target origin** and GET/HEAD. Different
  loopback ports, host aliases, private-network addresses, external resources,
  redirects outside that origin, mutating methods and WebSockets are blocked.
- A local proxy repeats the policy and connects to the approved IPv4 loopback
  port literally, without resolving arbitrary hostnames. CONNECT and upgrades are
  denied; authorization/cookie headers and response cookies are not forwarded.
- Service workers, downloads and permissions are disabled. Popups are closed.
  Browser background networking, QUIC and unproxied WebRTC UDP are disabled.
- Per call: at most 200 proxied requests, 2 MiB per response, 8 MiB aggregate wire
  bytes; event arrays cap at 80 each, DOM labels at 80, observation delay at 1.5 s,
  browser work at 25 s. Response limits describe wire bytes, not a hard browser RAM
  quota. The host's exec timeout is 40 s and kills the process group if needed.

These are application-level fences and resource budgets, **not an OS network or
filesystem sandbox** against hostile browser exploits or concurrent filesystem
attackers. Do not execute untrusted packs or inspect hostile pages as though the
capability card supplied hard isolation. GET endpoints can have side effects if
the application itself violates HTTP semantics; DevCheck cannot undo them. Apps
requiring login, POST requests, WebSockets or external assets may look incomplete;
blocked-resource facts explain that limitation, not an invented application bug.

## Evidence and image handoff

Every accepted invocation creates an exclusive directory under the **active session
workspace**, never under a global workspace:

```text
.devcheck/<timestamp>-<random-id>/
  evidence.json
  capture.jpg       # only when capture succeeded
```

No caller-controlled output path exists. The evidence root must be a real
directory, not a symlink; files use exclusive no-follow creation and mode 0600.
Existing receipts are never overwritten, moved or deleted. Retained receipts can
contain application information and should follow the workspace's normal privacy
and retention policy. Cleanup is an explicit operator action, not an automatic
part of a model run.

The JSON stdout envelope uses `schema: "cerveau.devcheck.v1"`, `ok`, `action`,
`verdict`, bounded `facts` and observation counts. It references the full evidence
receipt and any artifacts by workspace-relative path, bytes, MIME and SHA-256.
Failed checks also include up to four concrete failures in the short result.
The complete stdout envelope is capped at 5,500 bytes before host ingress;
`summary_truncated` marks omitted diagnostics, while full receipts and artifact
identities remain intact. Nothing on stdout is base64 image data.

An image artifact has `role: "context-image"` and `mime: "image/jpeg"`. A host
must resolve it through the session workspace boundary, validate size/hash/MIME,
and record attachment ownership before offering it to a model. **An artifact path
alone is not a claim that the model received an image.** Root-loop attachment
consumption is a separate host integration; the pack only produces evidence.

`target: "page"` means the initial **1280×960 viewport**, not a scrolling full-page
capture. `target: "rectangle"` uses coordinates inside that viewport. A selector
must match one visible element at most 4096 px per side and 8 megapixels before
encoding. Oversized elements are refused; the caller should choose a smaller
element. Images are encoded in a separate blank context, resized to at most
1280×960 and compressed to the hard byte cap: **128 KiB by default**, optionally
16–256 KiB. Quality and original/final dimensions are recorded. A size-cap failure
remains unverified; it does not return a missing or oversized image as success.

## Desktop/window capture is a different consent boundary

A headless browser screenshot cannot capture the user's desktop, another native
window or an arbitrary screen region. DevCheck 0.1 makes none of those claims.
The proposed user-facing path is a ChatUI **Share screen/window** action using the
browser's consent picker, followed by user-reviewed region selection and the same
bounded-image attachment flow. On Wayland, an eventual native helper should use
the desktop's Screenshot/ScreenCast portal rather than raw display access.

Neither path should silently select a source, preserve a capture grant, start
continuous recording or bypass a canceled picker. The user must see and approve
what will be attached. Automation of native desktop capture remains deferred until
that permission and preview contract is implemented and tested.

## Packaging and invocation

The pack manifests execute `/usr/bin/env rfx-devcheck <action>`. The distributable
must put a trusted `rfx-devcheck` launcher on the Cerveau service's PATH and keep
`engine.mjs` adjacent to the launcher runtime. This avoids workspace-relative
script lookup and does not extend the frozen v1 placeholder language. The service
PATH must not include an untrusted workspace directory before the launcher.

Dependencies are **Node.js 22+**, **Playwright 1.48+** and a compatible existing
Chromium binary. The launcher resolves relative imports from its own runtime
location; process cwd remains the active session workspace. Configure
`CERVEAU_PLAYWRIGHT_MODULE` to an installed Playwright module's absolute path and
`CERVEAU_CHROMIUM` to the installed Chromium executable when they are not available
through the normal runtime. These two names are explicitly allowed through the
RFX environment card. The tools do not install, download, update or retune anything.

Example stdin for `rfx-devcheck check`:

```json
{"url":"http://localhost:4173","checks":[{"selector":"#app","kind":"visible","expected":true},{"selector":".error-banner","kind":"exists","expected":false}]}
```

The dock card provides the URL field, Inspect app and Capture viewport buttons;
the check/selector/rectangle APIs remain available as typed talents. All three are
`sensitive` because they contact a local app and write new evidence files; they
are enabled only in autopilot mode and never run from an automatic polling timer.

## Verification

Tests were written before the engine, first failing because the runtime did not
exist. The manifest test validates all three talent files and the pack's widget
schema. Node tests cover input/URL/method/origin boundaries, symlink refusal,
structured CLI/dependency failures, declared check semantics, byte/dimension caps,
and retained evidence on failed navigation or capture.

```sh
go test ./rfx/devcheck
node --test scripts/devcheck/devcheck.test.mjs
```

The default Node run explicitly skips browser cases unless
`CERVEAU_PLAYWRIGHT_MODULE` is set. Set that variable and `CERVEAU_CHROMIUM` to an
**already installed** compatible runtime to run the fixture browser cases. They
create disposable local HTTP servers on random test ports and new temporary
workspaces. They never access production port 7700, the Brain Core, installed user
packs or an existing browser profile. Browser fixture evidence is retained in the
temporary workspaces rather than silently deleting user-visible artifacts.

The verified scope is those deterministic local fixtures. Model-driven DevCheck
selection, real application compatibility, native desktop consent, and image
consumption by the live Brain Core are separate acceptance cases and must not be
inferred from this fixture pass.

Implementation references: [isolated browser contexts and WebSocket routing](https://playwright.dev/docs/api/class-browsercontext),
[request interception and service worker caveat](https://playwright.dev/docs/network),
[browser screenshot options](https://playwright.dev/docs/api/class-page#page-screenshot).
