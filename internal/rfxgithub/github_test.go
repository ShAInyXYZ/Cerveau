package rfxgithub

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_COUNT=0")
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, b)
	}
	return string(b)
}

func repoTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitTest(t, dir, "init", "-b", "main")
	gitTest(t, dir, "config", "user.name", "RFX Test")
	gitTest(t, dir, "config", "user.email", "rfx@example.invalid")
	return dir
}

func invoke(t *testing.T, dir, talent, args string) Result {
	t.Helper()
	return Run(context.Background(), dir, talent, json.RawMessage(args), false)
}

func TestUnbornStatusDiffLogAndLiteralStage(t *testing.T) {
	dir := repoTest(t)
	names := []string{"space name.txt", "-flag.txt", "[abc].txt", "line\nbreak.txt", "untouched.txt"}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	status := invoke(t, dir, "git-status", `{}`)
	if !status.OK {
		t.Fatalf("status: %+v", status)
	}
	s := status.Data.(Status)
	if !s.Unborn || s.Branch != "main" || len(s.Files) != 5 {
		t.Fatalf("unborn status: %+v", s)
	}
	args, _ := json.Marshal(map[string]any{"paths": names[:4]})
	stage := invoke(t, dir, "git-add", string(args))
	if !stage.OK {
		t.Fatalf("stage: %+v", stage)
	}
	staged := strings.Split(strings.TrimSuffix(gitTest(t, dir, "diff", "--cached", "--name-only", "-z"), "\x00"), "\x00")
	if len(staged) != 4 || strings.Contains(strings.Join(staged, "\x00"), "untouched.txt") {
		t.Fatalf("staged: %q", staged)
	}
	diff := invoke(t, dir, "git-diff", `{"scope":"all"}`)
	if !diff.OK || !strings.Contains(diff.Data.(Diff).Patch, "+space name.txt") {
		t.Fatalf("unborn diff: %+v", diff)
	}
	log := invoke(t, dir, "git-log", `{"count":5}`)
	if !log.OK || len(log.Data.(History).Commits) != 0 {
		t.Fatalf("unborn log: %+v", log)
	}
}

func TestStageRejectsBroadEscapingMissingAndUnknownArgs(t *testing.T) {
	dir := repoTest(t)
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested", "file"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{`{}`, `{"paths":[]}`, `{"paths":["."]}`, `{"paths":["nested"]}`, `{"paths":["../other"]}`, `{"paths":["/etc/passwd"]}`, `{"paths":[".git/config"]}`, `{"paths":["escape/file"]}`, `{"paths":["missing"]}`, `{"paths":["nested/file"],"all":true}`} {
		if r := invoke(t, dir, "git-add", args); r.OK {
			t.Fatalf("accepted %s: %+v", args, r)
		}
	}
	if staged := gitTest(t, dir, "diff", "--cached", "--name-only"); staged != "" {
		t.Fatalf("unexpected staging: %s", staged)
	}
}

func TestCommitOnlyStagedAndFailuresAreNotSuccess(t *testing.T) {
	dir := repoTest(t)
	for _, name := range []string{"chosen", "untouched"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if r := invoke(t, dir, "git-commit", `{"message":"test: empty"}`); r.OK || r.Error.Code != "nothing_staged" {
		t.Fatalf("empty: %+v", r)
	}
	if r := invoke(t, dir, "git-add", `{"paths":["chosen"]}`); !r.OK {
		t.Fatalf("stage: %+v", r)
	}
	if r := invoke(t, dir, "git-commit", `{"message":"test: selected only"}`); !r.OK {
		t.Fatalf("commit: %+v", r)
	}
	if files := gitTest(t, dir, "ls-tree", "--name-only", "HEAD"); files != "chosen\n" {
		t.Fatalf("committed %q", files)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "index.lock"), []byte("operator lock"), 0600); err != nil {
		t.Fatal(err)
	}
	r := invoke(t, dir, "git-add", `{"paths":["untouched"]}`)
	if r.OK || r.Error.Code != "git_failed" || !strings.Contains(r.Error.Message, "index.lock") {
		t.Fatalf("failure swallowed: %+v", r)
	}
}

func TestRepositoryBoundaryAndLocalIdentity(t *testing.T) {
	dir := repoTest(t)
	subdir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subdir, 0700); err != nil {
		t.Fatal(err)
	}
	if r := invoke(t, subdir, "git-status", `{}`); r.OK || r.Error.Code != "workspace_not_repo_root" {
		t.Fatalf("parent repo permitted: %+v", r)
	}
	if r := invoke(t, dir, "git-profile", `{"name":"Local RFX","email":"local@example.invalid"}`); !r.OK {
		t.Fatalf("profile: %+v", r)
	}
	if got := gitTest(t, dir, "config", "--local", "user.name"); got != "Local RFX\n" {
		t.Fatal(got)
	}
	if r := invoke(t, t.TempDir(), "git-status", `{}`); !r.OK || r.Data.(map[string]any)["repository"] != false {
		t.Fatalf("not repo: %+v", r)
	}
}

func TestMutatingNetworksRequireHostApprovalAndNoGlobalSwitch(t *testing.T) {
	dir := repoTest(t)
	for _, talent := range []string{"git-push", "git-publish"} {
		r := invoke(t, dir, talent, `{"confirmed":true}`)
		if r.OK || r.Error.Code != "human_approval_required" {
			t.Fatalf("%s: %+v", talent, r)
		}
	}
	if r := invoke(t, dir, "gh-switch", `{"user":"someone"}`); r.OK || r.Error.Code != "operator_only" {
		t.Fatalf("switch: %+v", r)
	}
}

func TestStatusRenamesDeletesAndBoundedDiff(t *testing.T) {
	dir := repoTest(t)
	if err := os.WriteFile(filepath.Join(dir, "old name"), []byte("unchanged content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, dir, "add", "--", "old name")
	gitTest(t, dir, "commit", "-m", "initial")
	gitTest(t, dir, "mv", "--", "old name", "new name")
	s := invoke(t, dir, "git-status", `{}`)
	if !s.OK || len(s.Data.(Status).Files) != 1 || s.Data.(Status).Files[0].OriginalPath != "old name" {
		t.Fatalf("rename: %+v", s)
	}
	if err := os.WriteFile(filepath.Join(dir, "new name"), []byte(strings.Repeat("bounded output\n", 20000)), 0600); err != nil {
		t.Fatal(err)
	}
	d := invoke(t, dir, "git-diff", `{"scope":"all"}`)
	if !d.OK || !d.Truncated || len(d.Data.(Diff).Patch) > PatchLimit {
		t.Fatalf("diff bound: %+v", d)
	}
	if err := os.Remove(filepath.Join(dir, "new name")); err != nil {
		t.Fatal(err)
	}
	if r := invoke(t, dir, "git-add", `{"paths":["new name"]}`); !r.OK {
		t.Fatalf("stage deletion: %+v", r)
	}
}

func TestSuggestIsBoundedReviewDataNotHiddenInference(t *testing.T) {
	dir := repoTest(t)
	r := invoke(t, dir, "git-suggest", `{}`)
	if !r.OK {
		t.Fatalf("suggest: %+v", r)
	}
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"model_called":false`) {
		t.Fatalf("misleading result: %s", b)
	}
}

func TestStageTrackedDeletionAfterParentDirectoriesRemoved(t *testing.T) {
	dir := repoTest(t)
	path := "removed/nested/deleted file.txt"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, path), []byte("verified committed fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, dir, "add", "--", path)
	gitTest(t, dir, "commit", "-m", "test: tracked nested file")
	if got := gitTest(t, dir, "show", "HEAD:"+path); got != "verified committed fixture\n" {
		t.Fatalf("fixture backup missing: %q", got)
	}
	for _, relative := range []string{path, "removed/nested", "removed"} {
		if err := os.Remove(filepath.Join(dir, relative)); err != nil {
			t.Fatal(err)
		}
	}
	args, _ := json.Marshal(map[string]any{"paths": []string{path}})
	if result := invoke(t, dir, "git-add", string(args)); !result.OK {
		t.Fatalf("tracked deletion rejected: %+v", result)
	}
	if got := gitTest(t, dir, "diff", "--cached", "--name-status"); got != "D\t"+path+"\n" {
		t.Fatalf("staged deletion: %q", got)
	}
	if result := invoke(t, dir, "git-add", `{"paths":["removed/nested/not-tracked.txt"]}`); result.OK {
		t.Fatalf("untracked missing path accepted: %+v", result)
	}
}

func TestStageRejectsSymlinkedParentEvenInsideWorkspace(t *testing.T) {
	dir := repoTest(t)
	if err := os.Mkdir(filepath.Join(dir, "real"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real", "file"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}
	if result := invoke(t, dir, "git-add", `{"paths":["alias/file"]}`); result.OK || result.Error.Code != "invalid_arguments" {
		t.Fatalf("symlinked parent was not rejected by argument validation: %+v", result)
	}
}
