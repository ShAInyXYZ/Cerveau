# blender — operator's card (hardsurface modeling talent)

Scope: PURE MODELING. No materials, no UVs, no rendering — out of this talent.

## The loop
1. `blender-new` a scene (once per asset)
2. `write` a bpy script into the workspace (the modeling happens HERE — you write the code)
3. `blender-run` it against the scene
4. `blender-inspect` to verify — dimensions and poly counts are your eyes
5. Fix the script, run again. Iterate.

## Invocation patterns (bpy, Blender 5.0)
- Cube: `bpy.ops.mesh.primitive_cube_add(location=(0,0,0)); o=bpy.context.active_object; o.scale=(1,0.5,2); bpy.ops.object.transform_apply(scale=True)`
- Bevel (the hardsurface workhorse): `m=o.modifiers.new('Bev','BEVEL'); m.width=0.05; m.segments=2`
- Boolean: `b=o.modifiers.new('Cut','BOOLEAN'); b.operation='DIFFERENCE'; b.object=cutter; bpy.ops.object.modifier_apply(modifier='Cut')` (delete the cutter after)
- ALWAYS `bpy.ops.object.transform_apply(scale=True)` after scaling — un-applied scale breaks bevels and booleans
- SAVE or the work is lost: `bpy.ops.wm.save_as_mainfile(filepath=bpy.data.filepath)` at the END of every script

## Failure → fix
- `Python: Traceback` in stderr → read the LAST line first; fix the script, re-run. blender-run exits non-zero on script exceptions (--python-exit-code 1 wired in)
- `Error: Cannot read file` → the scene path is relative to the WORKSPACE root, not to the script
- Object invisible in inspect → forgot `transform_apply`, or the boolean ate it — inspect shows dimensions, check them
- Changes missing next run → the script didn't save; save call must be inside the script, blender-run never saves for you

## Never
- Never add materials, UV maps, cameras, lights, or render calls — out of scope, they waste turns
- Never model by dozens of blender-run micro-steps — write ONE script per feature, not per cube
- Never catch exceptions in the script to "keep going" — fail loud, the harness reads the traceback
