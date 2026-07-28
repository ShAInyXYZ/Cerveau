# Sound effects

Drop audio files here (`.mp3`, `.wav`, `.ogg`, or `.m4a`). Each file is picked up
automatically at build time and playable by its **base name** (no extension) via:

```js
import { play } from '../lib/sound.js';
play('send');
```

Files that don't exist yet are a silent no-op, so you can add them incrementally.

## Suggested names → where they'd be used

Name the file after the *event*, not the sound, so the same file can serve any
case that fits. These are the hooks the app is ready to wire:

| File name       | Event it represents                                  |
|-----------------|------------------------------------------------------|
| `send.*`        | user sends a message                                 |
| `receive.*`     | assistant's answer lands                             |
| `tool.*`        | a tool call starts (bash/read/write…)                |
| `done.*`        | a turn completes successfully                        |
| `error.*`       | an error card appears                                |
| `ask.*`         | the agent asks the user a question (ask_user card)   |
| `confirm.*`     | a confirm/positive action (rename saved, etc.)       |
| `delete.*`      | a session/memory is deleted                          |
| `toggle.*`      | small UI toggle (mode switch, panel open)            |
| `notify.*`      | generic soft notification / attention               |

Keep them **short (< 400 ms)** and **quiet** — feedback, not alarms.

Once your files are here, tell me and I'll wire each `play('name')` into its event.
