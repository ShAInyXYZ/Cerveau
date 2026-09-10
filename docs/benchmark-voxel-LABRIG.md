# Voxel-world benchmark prompt

Paste the text below into a new Cerveau autopilot session whose workspace is an empty, disposable project folder. Use Default sampling and Medium thinking on every autopilot step. These are a baseline, not a proven optimal configuration. Do not run this benchmark in the Cerveau source tree.

---

Build a playable, editable voxel world in this workspace. The test is whether generation, rendering, lighting, collision and editing agree—not whether a screenshot resembles Minecraft. No crafting, mobs or survival systems.

Work autonomously through a stepwise plan. Use the DGV Reflex if available to record a compact architecture, dependencies and verification status in dgv/. Keep it updated at meaningful milestones, not on every tool call. Use DevCheck if available for browser errors, DOM checks and screenshots. Inspect existing context before rebuilding it. Tool unavailability is not an excuse to claim an unperformed check passed.

Keep the application offline and dependency-free: raw WebGL2, your own matrix math and seeded noise, procedural colors/textures. No package installs, CDN, external assets or frameworks. Serve through a local static server; no build step. Do not modify files outside this workspace, install tools globally or stop unrelated processes.

## Deliverables

- index.html: UI, input and renderer; imports world.js.
- world.js: the shared ES module for world storage, generation, meshing, lighting, raycasting and physics. Importable without DOM or WebGL. The browser uses this implementation, not a duplicate.
- tests.mjs: executable, deterministic Node assertions for the contract below. Document the Node invocation needed to load world.js as ESM without dependencies.
- NOTES.md: startup instructions, conventions, measured results, limitations and unfinished work.

Application files belong at the workspace root. dgv/, .devcheck/ and test-evidence/ are allowed for planning and evidence. App startup may request only its local application files; generated screenshots and test evidence are not runtime assets.

## World

Use 16×16×128 chunks with flat typed arrays; Y is vertical, 0–127. Handle negative X/Z correctly. Provide air plus at least eight block types with explicit solidity, opacity and emission properties. Include water and an emitting block. Water is non-solid and transparent; emitters may be solid but must inject their own light.

Generate terrain bands, caves and a fixed sea level from your own octave-summed seeded noise. Generation must be independent of chunk creation order. Begin with a 5×5 chunk area centered on the player. Load and unload by distance as the player moves. Keep edits as sparse differences from generated terrain so they survive unloading; reverting an edit to its generated value removes that difference. Regeneration explicitly clears edits.

Missing neighbors are not automatically air: meshing must consult deterministic terrain plus edits at boundaries. Specify how lighting obtains sufficient neighbor context for consistent loaded boundaries without keeping the whole world resident. Report generation, lighting, meshing and first-playable timings on this actual machine; do not claim an unspecified laptop performance target.

## Meshing and rendering

Implement real greedy meshing. Merge compatible coplanar faces by block/material, face direction and compatible light/AO values. Expose naive visible-face and greedy-quad counts. No faces between opaque solids; no water faces against water. Handle opaque/water interfaces consistently without duplicate coplanar surfaces.

Use per-vertex voxel AO from two side neighbors and one corner neighbor. When both sides are occupied, use maximum occlusion; otherwise use their occupancy sum. Select triangle diagonals from opposite-corner AO values, not geometric diagonal length. Do not merge faces when that would erase lighting or AO detail.

Render opaque geometry first, then transparent water with blending and an appropriate depth-write policy. An edit invalidates every mesh that samples its changed block, lighting or AO neighborhood, including affected neighboring chunks and corners.

## Lighting

Keep separate integer skylight and block-light channels, each 0–15; expose both and their maximum. Direct sky travels downward through air without attenuation. Other propagation loses at least one level per cell. Document water attenuation and opaque/emitter behavior.

Use queued incremental removal and addition propagation for edits. Correctly remove obsolete light, preserve other sources, and relight newly opened routes across chunk boundaries. Initial loading or regeneration may build lighting from scratch; ordinary edits must not rebuild all loaded lighting. Expose cells visited and whether an edit triggered a full rebuild.

## Movement and editing

Provide pointer-lock mouse look, WASD, jump, sneak and a flight toggle. Flight still collides. Use per-axis AABB collision with bounded substeps or swept checks, sliding and one-block step-up. Document player dimensions, maximum speeds, gravity, fixed timestep and feet-position convention. Avoid wall-clock reads in physics. Large tick durations must be subdivided safely.

Use normalized-direction voxel DDA for picking within reach, with hit coordinate, outward face normal and distance in world units. Define edge/corner tie-breaking. Starting inside a solid returns it at distance zero with normal [0,0,0]. Refuse placement for that zero-normal hit and whenever the new block intersects the player's AABB. Left click removes; right click places the selected type.

Show crosshair, targeted-block outline and block selector. The HUD must show position, chunk, facing, target, loaded chunks, quads and frame time, plus all controls. Spawn somewhere safe with visible terrain. A clear WebGL-unavailable message is required but does not count as a successful rendering test.

## Shared test API

Export createWorld(options) from world.js and expose the browser instance as window.__world. It must support:

- getBlock(x,y,z), setBlock(x,y,z,type).
- getLight(x,y,z) → {sky, block, level}.
- loadChunk(cx,cz), unloadChunk(cx,cz), loadedChunks(), and chunkBlocks(cx,cz) returning a copy for comparison.
- chunkMeshStats(cx,cz) → {naiveFaces, quads, vertices}.
- raycast(origin,direction,maxDistance) → {block, normal, distance} or null; coordinates are three-element arrays.
- teleport(x,y,z), setInput({forward,right,jump,sneak,fly,yaw,pitch}), playerState(), tick(seconds). State reports feet position, velocity and grounded. Document units and input ranges.
- seed(), regenerate(seed), flushUpdates(), updateStats(), invariants().

Provide a deterministic fixture option for flat or empty terrain so tests can isolate behavior without changing normal procedural mode. flushUpdates() settles queued lighting and meshing before assertions. invariants() must inspect actual state and return diagnostic violations, not a hardcoded empty array. Explain its coverage and cost. Rendering and physics must consume the same world state these methods inspect.

## Acceptance tests

Build and run assertions; record exact commands, seeds, observations and failures. Never substitute implementation descriptions for results.

1. Determinism: compare every block in selected positive and negative chunks from two fresh worlds with the same seed. Repeat with neighbors loaded in reverse order. Arrays must match exactly.
2. Persistence: edit interior, edge and corner blocks; unload and reload; verify each value. Restore a block to its generated value and verify its sparse edit is removed.
3. Meshing: in a uniformly lit flat fixture, compare greedy quads against naive visible faces and report the ratio. Verify surface coverage and absent opaque interior faces, including across chunk borders; count reduction alone cannot prove correctness.
4. Skylight: in a solid fixture, open a one-cell-wide shaft eleven cells deep to unobstructed sky. After settling, all eleven air cells must have sky=15. Close its top with an opaque block; with no alternative opening, the remaining enclosed shaft must have sky=0.
5. Block light: inside a sealed dark fixture, place a level-15 source at x=15 with an air passage through x=16. Assert block=14 at x=16. Add a second source; remove the first and verify the second's contribution remains. Remove both and verify no stranded block light.
6. Enclosure: surround a lone emitting block with opaque neighbors. Its own source stays lit; its contribution outside the enclosure must become zero. Reopen one side and verify propagation returns. Report edit queue work and confirm no full-world lighting rebuild.
7. Physics: over a flat floor whose top is y=1, drop the player from feet y=100 for at most 1,200 ticks of 1/60 second, stopping when grounded. Assert grounded, feet at floor height within tolerance, and no solid intersection. Fly into a full-height wall at the app's maximum speed for 300 ticks of 1/60 second; assert no tunnelling. Test diagonal wall sliding and one-block step-up separately.
8. Raycast/editing: test all six face normals, negative coordinates, misses, inside-solid distance zero, and refusal to place inside the player. Verify border edits update affected meshes and lighting after flushUpdates().
9. Browser: actually load the app in a WebGL2-capable browser. Check console/network failures, take a screenshot showing visible terrain, and exercise movement plus remove/place. After an edit, verify API state and visible geometry agree. If available automation cannot drive pointer lock or inspect the canvas adequately, mark those checks unverified and provide manual steps; Node tests do not count as browser proof.

Do not disable assertions, weaken requirements, return fabricated metrics or mark unavailable browser checks as passed. On failure, identify the cause, fix it and rerun the relevant assertions. Report PASS, FAIL or UNVERIFIED for each acceptance item, with evidence paths and remaining limitations. Only call the task complete if the implementation and required checks genuinely support it.
