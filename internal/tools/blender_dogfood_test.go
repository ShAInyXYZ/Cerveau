package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cerveau/internal/guard"
	"cerveau/internal/rfx"
)

// The Blender talent dogfooded end to end FOR REAL: loader → registry →
// guard → blender headless. new → write script → run → inspect, exactly the
// loop the agent will use. Skips cleanly if blender is absent.
func TestBlenderTalentLive(t *testing.T) {
	if _, err := os.Stat("/usr/local/bin/blender"); err != nil {
		t.Skip("blender not installed — talent validated by loader+fuzz only")
	}
	ws := t.TempDir()
	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	l := rfx.NewLoader(filepath.Join(repoRoot, "rfx"), func(string) bool { return true })
	var blenderDefs []rfx.Reflex
	for _, d := range l.List() {
		if strings.HasPrefix(d.Name, "blender-") {
			blenderDefs = append(blenderDefs, d)
		}
	}
	if len(blenderDefs) != 3 {
		t.Fatalf("want 3 blender reflexes, got %d; errs: %v", len(blenderDefs), l.Errors())
	}

	reg := NewRegistry(Entry{Tool: NewBash(ws), RiskTier: RiskDangerous, Modes: []string{ModeAutopilot}})
	grd := guard.New(ws)
	reg.SetGuard(grd.Check)
	reg.SetRemediator(func(tool string, args json.RawMessage) (json.RawMessage, error) {
		return grd.Remediate(tool, args, time.Now())
	})
	if errs := reg.AddReflexes(blenderDefs); len(errs) != 0 {
		t.Fatal(errs)
	}
	call := func(name string, args map[string]any) (string, error) {
		raw, _ := json.Marshal(args)
		return reg.ExecuteMode(context.Background(), name, raw, ModeAutopilot)
	}

	// 1. new scene
	if out, err := call("blender-new", map[string]any{"scene": "part.blend"}); err != nil {
		t.Fatalf("blender-new failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(ws, "part.blend")); err != nil {
		t.Fatal("scene file not created")
	}

	// 2. the agent's modeling script (as the write tool would produce)
	script := `
import bpy
bpy.ops.mesh.primitive_cube_add(location=(0,0,0))
o = bpy.context.active_object
o.name = "Bracket"
o.scale = (2, 1, 0.5)
bpy.ops.object.transform_apply(scale=True)
m = o.modifiers.new('EdgeBevel', 'BEVEL')
m.width = 0.1
m.segments = 2
bpy.ops.object.modifier_apply(modifier='EdgeBevel')
bpy.ops.wm.save_as_mainfile(filepath=bpy.data.filepath)
`
	if err := os.WriteFile(filepath.Join(ws, "model.py"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := call("blender-run", map[string]any{"scene": "part.blend", "script": "model.py"}); err != nil {
		t.Fatalf("blender-run failed: %v\n%s", err, out)
	}

	// 3. inspect — the agent's eyes
	out, err := call("blender-inspect", map[string]any{"scene": "part.blend"})
	if err != nil {
		t.Fatalf("blender-inspect failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "OBJ Bracket MESH") {
		t.Fatalf("modeled object not visible in inspect: %s", out)
	}
	if !strings.Contains(out, "[4.0, 2.0, 1.0]") {
		t.Fatalf("dimensions wrong after transform_apply: %s", out)
	}

	// 4. failure path: a broken script must fail LOUD with the traceback kept.
	if err := os.WriteFile(filepath.Join(ws, "broken.py"), []byte("import bpy\nbpy.ops.mesh.primitive_cube_add(size='not-a-number')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = call("blender-run", map[string]any{"scene": "part.blend", "script": "broken.py"})
	if err == nil {
		t.Fatal("broken script did not fail")
	}
	if !strings.Contains(out, "Traceback") && !strings.Contains(out, "Error") {
		t.Fatalf("traceback not kept for self-correction: %s", out)
	}
}
