# Native debugging tools — 2026-09-07

Scope: four built-in tools for Cerveau autopilot, with recovery guidance. The old Minecraft workspace and session are not repaired, restarted or replaced by this change. A new benchmark should use a new session and an empty disposable workspace; these tool tests are not a claim that the benchmark will complete in one run.

## Tool contracts

| Tool | Bounded operation | What its result proves |
| --- | --- | --- |
| `browser_run` | 1–20 typed actions in one fresh page: click, fill, key, pointer, condition wait, DOM check, capture; at most six screenshots | Only declared DOM conditions are checked. Actions and captures alone are `executed`, not an application pass. |
| `runtime_profile` | 1–5 seconds of startup CPU sampling, located hotspots, completed long tasks, frame intervals and resource observations | A profile describes the observed interval, not application correctness. Unavailable metrics are explicitly unavailable. |
| `run_checks` | Existing Node assertion script, Node TAP tests or Go JSON tests; 1–120 second process limit | Fixed runner results, failures and source identities. A script exit does not reveal an assertion count. |
| `code_diagnostics` | Node syntax, existing local TypeScript compiler, or Go vet on 1–16 explicit files; total process budget 100–60000 ms | Located findings under the selected adapter, not a complete project build or runtime test. |

Tools retain evidence beneath the target workspace's `.devcheck/`. Model-facing results are bounded structured summaries; receipt paths and hashes allow the full retained evidence to be retrieved. Failed and unverified invocations return tool errors as well as structured results, so they cannot look like successful checks merely because a helper executed.

### Browser boundaries

- State persists between steps within one call, not across calls. No user browser profile, credentials, arbitrary eval, caller scripts or external CDP attachment.
- Real input uses browser automation; fixed DOM conditions are `exists`, `visible`, exact normalized text, or count. Text checks inspect bounded DOM text content, not rendered pixels. Screenshots need separate visual inspection; capture success is not visual verification.
- Explicit `http://localhost:PORT` or `http://127.0.0.1:PORT` only, ports 1024–65535. An independent proxy connects to literal IPv4 loopback and permits the exact target origin's GET/HEAD requests. POST, WebSockets, remote assets, credentials, popups, downloads and service workers are not supported. These tools target local static applications, not authenticated or mutating web workflows.
- Viewport 1280×960; JPEG cap defaults to 128 KiB, configurable 16–256 KiB. Screenshots may be compressed/resized and report their dimensions. Pointer input uses absolute viewport coordinates; pointer lock is not supported and must remain unverified/manual for the Minecraft acceptance test.
- Browser dependency paths come from trusted `CERVEAU_PLAYWRIGHT_MODULE` and optional `CERVEAU_CHROMIUM`, with the existing Chromium resolver as fallback. No package installation occurs. Playwright must support WebSocket routing.
- Runtime procedures and schemas ship inside the Cerveau binary. The Node runner has its own deadline; the Go owner imposes a 40-second process bound and validates receipt/image bytes, paths and hashes before reporting success.
- Profiling starts before navigation and uses a host-side sampling deadline, so a busy page does not control when stop is requested. At the end it briefly pauses its isolated renderer via the debugger to collect CPU evidence even from an infinite JavaScript loop; the receipt records this profiling intrusion. A still-running long task may not have emitted an observer record; frame intervals are not compositor FPS. Raw CPU profiles are discarded after bounded summarization.

### Native checks and diagnostics

`run_checks` accepts no arbitrary command, interpreter flags, environment, globs or dependency installs. The `node_script` adapter accepts bounded literal `script_args` after its single script path, for existing test-group selectors:

```json
{"runner":"node_script","paths":["tests.mjs"],"script_args":["skylight"],"source_paths":["world.js"],"timeout_seconds":30}
```

The fixed adapters use `node --experimental-default-type=module ./tests.mjs`, `node --test --test-reporter=tap ./test.mjs`, or `go test -json -count=1 -mod=readonly ./package`. Node scripts report exit status with unknown assertion count; Node TAP can report expected/actual and locations; Go JSON does not provide universal typed expected/actual values, so raw failure output is retained.

`script_args` permits at most 32 nonempty strings of at most 1024 UTF-8 bytes each, without NULs. Other runners reject the field. Arguments are not shell-expanded or interpreted as Node flags and are part of the receipt's check identity. Normal and recovery Bash now use `pipefail`, so piping failing checks through `tail` no longer hides their exit status; this does not enable `errexit` or prevent explicit shell error handling.

Before/after inventories bind relevant local source and manifest files plus explicit `source_paths`; they exclude dependencies and evidence directories. Inputs/test files are protected in comparisons. An explicit `prior_receipt` comparison requires the same check identity and unchanged protected checks. Changed output is only a status transition, not proof of improvement. Changed sources during execution or changed protected checks invalidate a pass. Inventories are bounded at 4096 files, 16384 visited entries, 8 MiB/file and 64 MiB total; dependency trees are not recursively inventoried.

```json
{"checker":"node","paths":["world.js"],"js_mode":"module","timeout_ms":20000}
```

Node diagnostics read syntax from stdin, with `.mjs`/`.cjs` semantics fixed and `.js` defaulting to modules. TypeScript uses an already-installed workspace compiler with explicit files and compiler defaults; **it does not apply `tsconfig.json`**. Go vet accepts explicit Go files in one directory, not arbitrary package patterns. Hashes cover named files; they do not certify every imported dependency or project setting.

All four tools use an owned process runner with a read-only host, private temporary directory and PID namespace. Checks additionally have a read-only workspace and no network. Go downloads/toolchain installation are disabled. Bubblewrap is required: unavailable isolation fails closed. This is write/network containment, **not a confidentiality sandbox**; existing local check code can still read host files. Test suites requiring project writes, network or unavailable dependencies need a separately authorized workflow, not an unprotected fallback.

## Recovery integration

Recovery instructions route syntax failures to narrow diagnostics, existing tests to structured checks, browser interactions to `browser_run`, and startup stalls to `runtime_profile`. These tools do not replace committed acceptance predicates, mark plan steps verified, expand retry budgets, or automatically amend plans. Existing verification still decides whether a repaired step and affected shared-file checks pass.

Subsequent [implementation follow-ups](backlog-fixes-2026-09-07.md) cover bounded
advisory plan amendments and UI changes. An amendment does not authorize weaker
checks, different required outcomes or a silent change to delivery order.

## Verification

Permanent regression suites live in `internal/tools/*native*_test.go`, `runchecks_test.go`, `codediagnostics_test.go` and `browsernative/engine.test.mjs`.

Initial integrated commands run on this machine:

```sh
go test ./internal/tools -run 'Test(Native|BrowserNative|RunChecks)' -count=1 -v
go test ./internal/tools -run 'TestBrowserRun|TestRuntimeProfile' -count=1 -v
```

Both passed. The real browser sequence filled an input, clicked a button, asserted the resulting text and captured a validated image; a second fresh call correctly failed an assertion expecting the prior page's state. A 1-second profile located `busyFixture` in a disposable page and observed its deliberate 150 ms completed long task. Native tests exercised real installed Node and Go, read-only write rejection, bounded output and cancellation. These were local disposable fixtures, not the Minecraft world.

The release gate also runs the browser engine's Node tests, frontend tests/type checks/build, all Go tests with the race detector, Go vet and whitespace checks. Full build and installation identities are recorded in `build/<bundle>/build.json` and the separate verified installation receipt; a build alone is not an installation. No model benchmark is started automatically.
