package rfxgithub

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cerveau/internal/rfx"
)

func TestProtocolAlwaysOneJSONResultAndExitFailure(t *testing.T) {
	dir := repoTest(t)
	for _, args := range []string{`null`, `{} {}`, `{"count":1.5}`, strings.Repeat(" ", InputLimit+1)} {
		var out bytes.Buffer
		code := DecodeAndRun(context.Background(), dir, "git-log", strings.NewReader(args), &out, false)
		var r Result
		if code == 0 || json.Unmarshal(out.Bytes(), &r) != nil || r.OK || r.Error == nil {
			t.Fatalf("input %q: code %d: %s", clip(args, 40), code, out.String())
		}
	}
}

func TestGitRedirectEnvironmentDoesNotEscapeRepo(t *testing.T) {
	dir := repoTest(t)
	other := repoTest(t)
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "user.name")
	t.Setenv("GIT_CONFIG_VALUE_0", "Injected Identity")
	r := invoke(t, dir, "git-profile", `{"name":"Chosen Local","email":"chosen@example.invalid"}`)
	if !r.OK {
		t.Fatalf("profile: %+v", r)
	}
	// Read through the helper, which intentionally ignores all injected GIT_*.
	s := invoke(t, dir, "git-status", `{}`)
	o := invoke(t, other, "git-status", `{}`)
	if !s.OK || !o.OK || s.Data.(Status).Identity.Name != "Chosen Local" || o.Data.(Status).Identity.Name != "RFX Test" {
		t.Fatalf("cross-repository config write: local=%+v other=%+v", s, o)
	}
}

func TestHooksAndCleanFiltersDoNotExecute(t *testing.T) {
	dir := repoTest(t)
	marker := filepath.Join(dir, "hook-ran")
	hook := "#!/bin/sh\n touch '" + marker + "'\n"
	if err := os.WriteFile(filepath.Join(dir, ".git", "hooks", "pre-commit"), []byte(hook), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("value"), 0600); err != nil {
		t.Fatal(err)
	}
	if r := invoke(t, dir, "git-add", `{"paths":["file"]}`); !r.OK {
		t.Fatalf("stage: %+v", r)
	}
	if r := invoke(t, dir, "git-commit", `{"message":"test: no hooks"}`); !r.OK {
		t.Fatalf("commit: %+v", r)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("hook executed: %v", err)
	}
	gitTest(t, dir, "config", "filter.untrusted.clean", "touch filter-ran")
	if r := invoke(t, dir, "git-add", `{"paths":["file"]}`); r.OK || r.Error.Code != "external_filter" {
		t.Fatalf("filter accepted: %+v", r)
	}
}

func TestApprovedNetworkValidationHappensBeforeAnyNetwork(t *testing.T) {
	dir := repoTest(t)
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("value"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, dir, "add", "file")
	gitTest(t, dir, "commit", "-m", "test")
	head := strings.TrimSpace(gitTest(t, dir, "rev-parse", "HEAD"))
	for _, tc := range []struct{ talent, args, code string }{
		{"git-push", `{"remote":"origin","branch":"main","expected_head":"` + strings.Repeat("0", 40) + `"}`, "head_changed"},
		{"git-push", `{"remote":"--all","branch":"main","expected_head":"` + head + `"}`, "invalid_arguments"},
		{"git-push", `{"remote":"origin","branch":"main:other","expected_head":"` + head + `"}`, "invalid_arguments"},
		{"git-publish", `{"name":"implicit-owner","visibility":"private","expected_head":"` + head + `"}`, "invalid_arguments"},
		{"git-publish", `{"name":"owner/repo","visibility":"public","expected_head":"` + strings.Repeat("0", 40) + `"}`, "head_changed"},
	} {
		r := Run(context.Background(), dir, tc.talent, json.RawMessage(tc.args), true)
		if r.OK || r.Error.Code != tc.code {
			t.Fatalf("%s: %+v", tc.args, r)
		}
	}
	gitTest(t, dir, "remote", "add", "origin", "https://example.invalid/owner/repo.git")
	r := Run(context.Background(), dir, "git-push", json.RawMessage(`{"remote":"origin","branch":"main","expected_head":"`+head+`"}`), true)
	if r.OK || r.Error.Code != "unsupported_remote" {
		t.Fatalf("external destination accepted: %+v", r)
	}
	r = Run(context.Background(), dir, "git-publish", json.RawMessage(`{"name":"owner/repo","visibility":"private","expected_head":"`+head+`"}`), true)
	if r.OK || r.Error.Code != "origin_exists" {
		t.Fatalf("existing origin ignored: %+v", r)
	}
}

func TestCredentialFreeGitHubDestinationAllowlist(t *testing.T) {
	for _, value := range []string{"https://github.com/owner/repo.git", "git@github.com:owner/repo.git", "ssh://git@github.com/owner/repo.git"} {
		if !githubURL(value) {
			t.Fatalf("rejected %s", value)
		}
	}
	for _, value := range []string{"https://token@github.com/owner/repo", "https://github.com.evil.invalid/owner/repo", "https://github.com/owner/repo?token=secret", "git@github.com:owner/repo\ngit@github.com:other/repo", "file:///tmp/repo", "ssh://root@github.com/owner/repo", "https://github.com/owner/.."} {
		if githubURL(value) {
			t.Fatalf("accepted %s", value)
		}
	}
	if got := redact("https://name:password@example.invalid/repo"); strings.Contains(got, "password") {
		t.Fatal(got)
	}
}

func TestBoundedBufferKeepsSizeAndSignalsTruncation(t *testing.T) {
	b := &boundedBuffer{limit: 10}
	n, err := b.Write([]byte(strings.Repeat("x", 100)))
	if n != 100 || err != nil || !b.truncated || len(b.data) != 10 {
		t.Fatalf("%+v %d %v", b, n, err)
	}
}

func TestCanonicalPackHasElevenValidArgvTalents(t *testing.T) {
	root := filepath.Join("..", "..", "rfx", "github")
	data, err := os.ReadFile(filepath.Join(root, "pack.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := rfx.ParsePack(data, filepath.Join(root, "pack.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err = rfx.ValidatePack(p); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(root, "*.rfx.yaml"))
	if err != nil || len(paths) != 11 {
		t.Fatalf("talents %d: %v", len(paths), err)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		reflex, err := rfx.Parse(data, path)
		if err != nil {
			t.Fatal(err)
		}
		if err = rfx.Validate(reflex, func(string) bool { return false }); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if reflex.Kind != rfx.KindExec || len(reflex.Argv) != 3 || reflex.Argv[0] != "/usr/bin/env" || reflex.Argv[1] != "rfx-github" || reflex.Argv[2] != reflex.Name {
			t.Fatalf("unexpected argv: %+v", reflex)
		}
		for _, env := range reflex.Card.Env {
			if env == "CRV_RFX_HUMAN_APPROVED" {
				t.Fatal("approval may not come from a manifest allowlist")
			}
		}
	}
}
