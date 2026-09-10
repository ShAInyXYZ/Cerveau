package rfxdgv

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestGitIgnoredReferenceTreeDoesNotExhaustInventory(t *testing.T) {
	ws := t.TempDir()
	cmd := exec.Command("git", "init", "--quiet")
	cmd.Dir = ws
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	if err := cmd.Run(); err != nil {
		t.Skipf("Git not available: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".gitignore"), []byte("reference-tree/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "app.go"), []byte("package fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ignored := filepath.Join(ws, "reference-tree")
	if err := os.Mkdir(ignored, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxFiles+1; i++ {
		if err := os.WriteFile(filepath.Join(ignored, strconv.Itoa(i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	files, scope, err := inventory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || !strings.Contains(scope, "respecting ignore rules") {
		t.Fatalf("ignored tree entered inventory: %d %s", len(files), scope)
	}
}

func coreForTest(t *testing.T) string {
	t.Helper()
	p := os.Getenv("RFX_DGV_CORE")
	if p == "" {
		p = filepath.Join("..", "..", "..", "Dia-GramV", "packages", "core", "src", "index.js")
	}
	p, _ = filepath.Abs(p)
	if _, err := os.Stat(p); err != nil {
		t.Skip("original DGV core unavailable; set RFX_DGV_CORE to run engine integration tests")
	}
	return p
}

func invoke(t *testing.T, c *Client, action string, args any) Result {
	t.Helper()
	b, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Run(context.Background(), action, b)
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return r
}

func fixtureDoc() map[string]any {
	return map[string]any{"dgv": 1, "meta": map[string]any{"title": "Fixture"}, "frames": []any{}, "nodes": []any{
		map[string]any{"id": "api", "kind": "module", "label": "API", "path": "src/api.go", "status": "todo"},
		map[string]any{"id": "store", "kind": "module", "label": "Store", "path": "src/store.go", "status": "todo"},
	}, "edges": []any{map[string]any{"id": "api-store", "source": "api", "target": "store", "kind": "import"}}}
}

func TestNamesAndInputsAreBoundedBeforeEngine(t *testing.T) {
	c := &Client{Workspace: t.TempDir(), Core: "/missing-core.js"}
	for _, name := range []string{"../outside", "/tmp/graph", "a/b", "", "x.dgv.json", strings.Repeat("x", 101)} {
		b, _ := json.Marshal(map[string]any{"name": name})
		if _, err := c.Run(context.Background(), "read", b); err == nil || !strings.Contains(err.Error(), "invalid_name") {
			t.Errorf("name %q: %v", name, err)
		}
	}
	for _, raw := range []string{"null", "[]", `{"name":"x","unknown":true}`, `{"name":"x"} {}`} {
		if _, err := c.Run(context.Background(), "read", []byte(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if _, err := c.Run(context.Background(), "delete", []byte(`{}`)); err == nil {
		t.Fatal("accepted delete")
	}
	if _, err := c.Run(context.Background(), "create", []byte(strings.Repeat(" ", MaxInputBytes+1))); err == nil {
		t.Fatal("accepted oversized input")
	}
}

func TestWorkspaceJailRejectsGraphAndSourceSymlinks(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(ws, "dgv")); err != nil {
		t.Fatal(err)
	}
	c := &Client{Workspace: ws, Core: "/missing-core.js"}
	if _, err := c.Run(context.Background(), "list", []byte(`{}`)); err == nil {
		t.Fatal("followed escaping graph directory")
	}
}

func TestNativeEngineCreateContextCheckAndUpdate(t *testing.T) {
	ws := t.TempDir()
	if err := os.Mkdir(filepath.Join(ws, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"api", "store"} {
		if err := os.WriteFile(filepath.Join(ws, "src", name+".go"), []byte("package fixture\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := &Client{Workspace: ws, Core: coreForTest(t)}
	created := invoke(t, c, "create", map[string]any{"name": "fixture", "document": fixtureDoc()})
	hash := created["graph_sha256"].(string)
	if len(hash) != 64 {
		t.Fatalf("missing graph identity: %v", created)
	}
	listed := invoke(t, c, "list", map[string]any{})
	if listed["total"].(int) != 1 {
		t.Fatalf("list: %v", listed)
	}
	first := invoke(t, c, "context", map[string]any{"name": "fixture", "focus": "api"})
	fresh := first["freshness"].(map[string]any)
	if fresh["state"] != "unbased" {
		t.Fatalf("claimed unreviewed graph is fresh: %v", fresh)
	}
	fingerprint := fresh["fingerprint"].(string)
	same := invoke(t, c, "context", map[string]any{"name": "fixture", "focus": "api", "observed_hash": fingerprint})
	if same["freshness"].(map[string]any)["state"] != "unchanged" {
		t.Fatalf("stable context: %v", same)
	}
	if err := os.WriteFile(filepath.Join(ws, "src", "api.go"), []byte("package changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	changed := invoke(t, c, "context", map[string]any{"name": "fixture", "focus": "api", "observed_hash": fingerprint})
	if changed["freshness"].(map[string]any)["state"] != "changed" {
		t.Fatal("same path with changed contents was called unchanged")
	}
	check := invoke(t, c, "check", map[string]any{"name": "fixture", "checks": "all"})
	if check["ok"] != true {
		t.Fatalf("check: %v", check)
	}
	before, _ := os.ReadFile(filepath.Join(ws, "dgv", "fixture.dgv.json"))
	updated := invoke(t, c, "update", map[string]any{"name": "fixture", "expected_hash": hash, "patch": map[string]any{"nodes": []any{map[string]any{"id": "api", "status": "wip"}}}})
	if updated["graph_sha256"] == hash {
		t.Fatal("update identity unchanged")
	}
	backup, err := os.ReadFile(filepath.Join(ws, updated["backup"].(string)))
	if err != nil || string(backup) != string(before) {
		t.Fatalf("backup not exact: %v", err)
	}
	staleArgs, _ := json.Marshal(map[string]any{"name": "fixture", "expected_hash": hash, "patch": map[string]any{"nodes": []any{map[string]any{"id": "api", "status": "done"}}}})
	if _, err := c.Run(context.Background(), "update", staleArgs); err == nil || !strings.Contains(err.Error(), "stale_graph") {
		t.Fatalf("stale update: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(ws, "dgv", "fixture.dgv.json"))
	if strings.Contains(string(after), `"status": "done"`) || !strings.Contains(string(after), `"by": "rfx"`) {
		t.Fatal("stale update wrote or history missing")
	}
}

func TestMutationRejectsRemovalAndInvalidEdgesWithoutChangingGraph(t *testing.T) {
	c := &Client{Workspace: t.TempDir(), Core: coreForTest(t)}
	created := invoke(t, c, "create", map[string]any{"name": "fixture", "document": fixtureDoc()})
	for _, patch := range []any{
		map[string]any{"remove": map[string]any{"nodes": []string{"api"}}},
		map[string]any{"history": []any{}},
		map[string]any{"edges": []any{map[string]any{"id": "bad", "source": "api", "target": "missing", "kind": "import"}}},
		map[string]any{"nodes": []any{map[string]any{"id": "new", "kind": "wrong", "label": "Bad"}}},
	} {
		args, _ := json.Marshal(map[string]any{"name": "fixture", "expected_hash": created["graph_sha256"], "patch": patch})
		if _, err := c.Run(context.Background(), "update", args); err == nil {
			t.Fatalf("accepted unsafe patch: %#v", patch)
		}
		read := invoke(t, c, "read", map[string]any{"name": "fixture", "on": "api"})
		if read["graph_sha256"] != created["graph_sha256"] {
			t.Fatal("rejected patch changed graph")
		}
	}
	args, _ := json.Marshal(map[string]any{"name": "fixture", "document": fixtureDoc()})
	if _, err := c.Run(context.Background(), "create", args); err == nil {
		t.Fatal("create overwrote existing graph")
	}
}

func TestContextMissingAndEscapingSourcesCannotBeUnchanged(t *testing.T) {
	c := &Client{Workspace: t.TempDir(), Core: coreForTest(t)}
	invoke(t, c, "create", map[string]any{"name": "fixture", "document": fixtureDoc()})
	r := invoke(t, c, "context", map[string]any{"name": "fixture"})
	fresh := r["freshness"].(map[string]any)
	if fresh["state"] != "incomplete" {
		t.Fatalf("missing paths not reported: %v", fresh)
	}
	r2 := invoke(t, c, "context", map[string]any{"name": "fixture", "observed_hash": fresh["fingerprint"]})
	if r2["freshness"].(map[string]any)["state"] == "unchanged" {
		t.Fatal("missing sources certified unchanged")
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(c.Workspace, "src")); err != nil {
		t.Fatal(err)
	}
	r3 := invoke(t, c, "context", map[string]any{"name": "fixture"})
	if r3["freshness"].(map[string]any)["state"] != "incomplete" {
		t.Fatal("escaping source was trusted")
	}
}

func TestExistingBackupSymlinkCannotAuthorizeReplacement(t *testing.T) {
	c := &Client{Workspace: t.TempDir(), Core: coreForTest(t)}
	created := invoke(t, c, "create", map[string]any{"name": "fixture", "document": fixtureDoc()})
	hash := created["graph_sha256"].(string)
	dir := filepath.Join(c.Workspace, "dgv", ".rfx-backups")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../fixture.dgv.json", filepath.Join(dir, "fixture-"+hash+".json")); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"name": "fixture", "expected_hash": hash, "patch": map[string]any{"nodes": []any{map[string]any{"id": "api", "status": "done"}}}})
	if _, err := c.Run(context.Background(), "update", raw); err == nil || !strings.Contains(err.Error(), "backup_verification_failed") {
		t.Fatalf("backup alias accepted: %v", err)
	}
	r := invoke(t, c, "read", map[string]any{"name": "fixture", "on": "api"})
	if r["graph_sha256"] != hash {
		t.Fatal("rejected symlink backup changed graph")
	}
}
