# DevCheck — operator's card

## Invocation patterns
- `devcheck-inspect {"url":"http://localhost:4173"}`
- `devcheck-check {"url":"http://localhost:4173","checks":[{"selector":"#app","kind":"visible","expected":true}]}`
- `devcheck-capture {"url":"http://localhost:4173","target":"selector","selector":"#app"}`

## Failure → fix
- `URL_SCOPE` → use an explicit local HTTP app port 1024..65535; external origins are not supported.
- `DEPENDENCY_MISSING` → operator must configure the packaged Playwright runtime; never install dependencies during a model run.
- `CAPTURE_TARGET` → choose one visible CSS element smaller than 4096 px / 8 megapixels.
- `CHECK_FAILED` → read failed_checks and the receipt; fix the app or correct the assertion, never report a visual pass.

## Never
- Never use an existing user browser profile, CDP endpoint, desktop source or arbitrary script.
- Never equate captured/observed with passed. Checks prove only their declared assertions.
- Never send an image repeatedly; prefer DOM and request facts when they answer the question.

## Idioms
- `page` is a 1280×960 viewport, not the whole document. JPEG max 1280×960, default 128 KiB.
- Receipts and hashed images live in `.devcheck/` in the active session workspace.
- Fresh anonymous context; network limited to same-origin GET/HEAD. Login, mutations, WebSockets and external assets are blocked.
