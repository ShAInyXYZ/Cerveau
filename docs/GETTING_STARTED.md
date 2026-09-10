# Getting started on Linux

This guide builds Cerveau from source. You supply a separately installed,
tool-capable llama.cpp or vLLM server and its model; the build does not install
an inference engine or download model weights.

## 1. Build the application

You need Git, Bash on PATH, Go 1.25 or newer, and Node.js with npm. Node 24+ is recommended;
the supported older major versions are Node 20.19+ and Node 22.12+.

```bash
git clone https://github.com/ShAInyXYZ/Cerveau.git
cd Cerveau
npm --prefix panel ci
npm --prefix panel run build
mkdir -p build
go build -o build/crv ./cmd/crv
go build -o build/crvcli ./cmd/crvcli
```

Build the panel before the Go executable: its output in `internal/panel/dist`
is embedded into `crv`. Keep Node available if you will use JavaScript editing
or native debugging tools; it is not exclusively a build dependency.

## 2. Configure a workspace and model endpoint

Create the configuration directory:

```bash
mkdir -p "$HOME/.config/cerveau"
```

Save the following as `~/.config/cerveau/config.json`. Replace the workspace
placeholder with an existing project's absolute path. JSON does not expand
`~` or shell variables inside path values.

```json
{
  "addr": "127.0.0.1:7700",
  "workspace": "/absolute/path/to/your/project",
  "model_ctx": 32768,
  "endpoints": {
    "model": "http://127.0.0.1:8080"
  }
}
```

The model URL is the server base URL, **without `/v1`**. Cerveau appends paths
such as `/v1/chat/completions`. Use your server's actual port and a context
capacity it supports. Unspecified configuration fields retain their defaults.

Protect the configuration, which can later contain access credentials:

```bash
chmod 600 "$HOME/.config/cerveau/config.json"
```

First-run caveat: when Cerveau creates a missing configuration automatically,
config-field environment overrides such as `CRV_MODEL_URL` and `CRV_ADDR` are
not applied until a later load. Create the file before a customized first
launch. Existing configuration loads support `CRV_REMOTE_ACCESS_TOKEN`,
`CRV_ADDR`, `CRV_MODEL_URL`, `CRV_EMBEDDER_URL`, `CRV_TYPESENSE_URL`, and
`CRV_SESSIONS_DIR`.

Default locations and endpoints:

- Configuration: `~/.config/cerveau/config.json`.
- Session journals: `~/.crv/sessions/`; code indexes, managed memory data and
  other runtime state also live under `~/.crv/`.
- Model: `http://localhost:8080`; optional embedder: `http://localhost:8081`.
- Typesense's initial configured URL is `http://localhost:8108`; managed startup
  selects a port starting at 8188 and saves the actual URL and key.
- Native debugging tools also save evidence inside the project under
  `.devcheck/`. Runtime data is not confined entirely to `~/.crv/`.

## 3. Start and check Cerveau

First start your separately installed llama.cpp or vLLM server using its
model-appropriate tool-calling configuration. Have it listening on the model
endpoint above before launching Cerveau. Then, from the repository root:

```bash
./build/crv -config "$HOME/.config/cerveau/config.json"
```

Startup can download and start managed Typesense, and sends a tool-call canary
to the model. It is not a configuration-only check.

Open <http://127.0.0.1:7700>. In another terminal, from the repository root:

```bash
./build/crvcli -addr http://127.0.0.1:7700 health
./build/crvcli -addr http://127.0.0.1:7700 sessions
```

If curl is available, the health endpoint is also directly readable:

```bash
curl --fail --silent --show-error http://127.0.0.1:7700/api/health
```

Inspect the component statuses and startup canary result; a responding panel
does not prove that every model or tool is ready. Choose the intended workspace
before requesting changes. CLI sessions default to the caller's current
directory; use its global `-workspace` flag to select another project. Global
CLI flags precede the command.

Use CLI `-addr` rather than sharing an exported `CRV_ADDR`: the core expects
`host:port`, while the CLI expects an HTTP base URL.

## Optional memory and debugging dependencies

**Memory.** Typesense is managed automatically in the default setup. Its first
installation requires network access for a checksum-verified binary download.
If it is unavailable, Cerveau can still start; session journals and local
failure evidence remain available, but Typesense-backed recall is unavailable.
With Typesense but no compatible embedder, recall can use lexical search.

Hybrid vector recall additionally needs a Python 3.10+ environment containing
`torch`, `fastapi`, `pydantic`, `sentence_transformers`, and `uvicorn`, plus
trusted local Nemotron-3-Embed-1B model files. These are not installed by the
following command. Once prepared, start the sidecar **before Cerveau** so its
startup probe can configure embedding support:

```bash
EMBED_MODEL=/absolute/path/to/Nemotron-3-Embed-1B python3 sidecars/nemotron_embed.py
```

The sidecar defaults to CPU execution, eight threads and loopback port 8081.
Hybrid retrieval also validates schema and embedding conventions; unsupported
combinations are not silently treated as compatible. Failure-triggered recall
can fall back to lexical and local evidence. See
[embedding compatibility and fallback](memory-embedding-conventions.md).

**JavaScript edits and native tools.** Installed Node outside the workspace and
working Bubblewrap (`bwrap`) are required for JavaScript mutation syntax checks.
Bubblewrap is also required by the recovery shell and native debugging tools.
Unavailable isolation fails closed; there is no unprotected fallback. These
dependencies are not required merely to start the HTTP server.

**Native browser procedures.** `browser_run` and `runtime_profile` additionally
need installed Chromium and Playwright with WebSocket routing support. Set
these in the environment that launches `crv`, replacing the placeholders:

```bash
export CERVEAU_PLAYWRIGHT_MODULE=/absolute/path/to/playwright/index.mjs
export CERVEAU_CHROMIUM=/absolute/path/to/chromium
```

The Playwright value must name an existing absolute module file. The Chromium
override is optional when automatic discovery finds your installed browser.
Neither is downloaded automatically. These procedures target local static
applications: exact-origin GET/HEAD only, no authenticated browser profile,
POST, WebSockets or remote assets. Actions and screenshots alone do not prove
application correctness. See [native tool contracts and limits](native-debug-tools-2026-09-07.md).

## Security and next steps

Keep the explicit `127.0.0.1:7700` bind. Do not substitute `:7700` or expose the
API through a public proxy as a shortcut. Loopback clients are trusted, and
ordinary Autopilot shell commands run with your OS user's privileges. Run as
an unprivileged user and treat API access as shell access. Read-only tool
isolation is not a confidentiality sandbox. See [the security policy](../SECURITY.md)
before configuring remote access.

- [Contributing and development](../CONTRIBUTING.md).
- [Native RFX implementation and limits](rfx-native-0.6.md),
  [GitHub pack](rfx-github.md), and [DevCheck pack](rfx-devcheck.md).
- [Legacy saved-plan recovery boundaries](legacy-plan-recovery-2026-09-07.md).
- [Advanced Core profiles](../deploy/profiles/README.md): hardware-specific
  systemd deployments, not a universal application installer.

The source-build commands above do not install or enable a system service.
`node scripts/build-release.mjs` is a separate test-and-package workflow, not
an installation command.
