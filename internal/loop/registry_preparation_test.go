package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cerveau/internal/episodic"
	"cerveau/internal/guard"
	"cerveau/internal/llm"
	"cerveau/internal/rfx"
	"cerveau/internal/skills"
	"cerveau/internal/tools"
)

func registryFixture(t *testing.T, m *scriptedModel) (*Loop, string, string, string) {
	t.Helper()
	global, session, journal := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "events.jsonl")
	registry := func(ws string) *tools.Registry {
		r := tools.NewRegistry(
			tools.Entry{Tool: tools.NewBash(ws), RiskTier: tools.RiskDangerous},
			tools.Entry{Tool: tools.NewRead(ws), RiskTier: tools.RiskSafe},
		)
		r.SetWorkspace(ws)
		r.SetGuard(guard.New(ws).Check)
		return r
	}
	l := New(llm.NewClient(m.srv.URL), registry(global),
		func(string) (*episodic.Writer, error) { return episodic.Open(journal) },
		func(string) string { return journal }, nil)
	l.SetWorkspaceFunc(func(string) string { return session })
	l.SetRegistryForWorkspace(registry)
	t.Cleanup(func() { l.WaitBackground(time.Second) })
	return l, global, session, journal
}

func registrySkills(t *testing.T, names ...string) *skills.Loader {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		body := fmt.Sprintf("---\nname: %s\ndescription: scoped capability\ntriggers: [%s]\ntools:\n  - name: %s_tool\n    command: 'cat input.txt > output.md; cat output.md'\n---\n%s skill instructions.\n", name, name, name, name)
		auditWrite(t, filepath.Join(dir, name+".md"), body)
	}
	return skills.NewLoader(dir)
}

func registryNotes(t *testing.T, journal, kind string) []string {
	t.Helper()
	events, err := episodic.Replay(journal)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, event := range events {
		if event.Type != episodic.Note {
			continue
		}
		var note struct{ Kind, Text string }
		if json.Unmarshal(event.Payload, &note) == nil && note.Kind == kind {
			out = append(out, note.Text)
		}
	}
	return out
}

func TestRegistryChatSkillNeedsOnlyLoader(t *testing.T) {
	m := newScriptedModel(toolCall("orchard_tool", `{}`), textReply("done"))
	defer m.srv.Close()
	l, global, session, journal := registryFixture(t, m)
	auditWrite(t, filepath.Join(global, "input.txt"), "GLOBAL")
	auditWrite(t, filepath.Join(session, "input.txt"), "SESSION")
	l.SetSkills(registrySkills(t, "orchard"))
	if _, err := l.Run(context.Background(), "s", "orchard inspection", "brainstorming"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(session, "output.md"))
	if err != nil || string(got) != "SESSION" {
		t.Fatalf("matched skill did not execute in session workspace: output=%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(global, "output.md")); !os.IsNotExist(err) {
		t.Fatalf("skill wrote into global workspace: %v", err)
	}
	if got := registryNotes(t, journal, "skill_loaded"); !reflect.DeepEqual(got, []string{"skill loaded: orchard"}) {
		t.Fatalf("skill load evidence = %v", got)
	}
}

func TestRegistryChatDoesNotLoadCompletedTaskSkills(t *testing.T) {
	m := newScriptedModel(textReply("done"))
	defer m.srv.Close()
	l, _, _, journal := registryFixture(t, m)
	wr, err := episodic.Open(journal)
	if err != nil {
		t.Fatal(err)
	}
	defer wr.Close()
	auditAppend(t, wr, episodic.MsgUser, map[string]string{"text": "orchard task"})
	auditAppend(t, wr, episodic.Plan, auditPlan())
	auditAppend(t, wr, episodic.Checkpoint, map[string]any{"index": 0, "status": "done"})
	l.SetSkills(registrySkills(t, "orchard", "harbor"))
	if _, err := l.Run(context.Background(), "s", "harbor inspection", "brainstorming"); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.offered) == 0 || !strings.Contains(strings.Join(m.offered[0], ","), "harbor_tool") || strings.Contains(strings.Join(m.offered[0], ","), "orchard_tool") {
		t.Fatalf("new chat inherited old task capabilities: %v", m.offered)
	}
}

func TestRegistryPlanLoadsOriginalAndCurrentInstructionOnce(t *testing.T) {
	m := newScriptedModel(textReply("done"))
	defer m.srv.Close()
	l, _, session, journal := registryFixture(t, m)
	wr, err := episodic.Open(journal)
	if err != nil {
		t.Fatal(err)
	}
	defer wr.Close()
	auditAppend(t, wr, episodic.MsgUser, map[string]string{"text": "orchard task"})
	auditAppend(t, wr, episodic.Plan, auditPlan())
	auditWrite(t, filepath.Join(session, "x"), "OLD")
	l.SetSkills(registrySkills(t, "orchard", "harbor"))
	if _, err := l.RunStep(context.Background(), "s", StepRunRequest{Step: 0, Reason: "harbor inspection"}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	offered := append([]string(nil), m.offered[0]...)
	m.mu.Unlock()
	for _, name := range []string{"orchard_tool", "harbor_tool"} {
		if !strings.Contains(strings.Join(offered, ","), name) {
			t.Fatalf("plan lost original/current skill %s: %v", name, offered)
		}
	}
	if got := registryNotes(t, journal, "skill_loaded"); len(got) != 2 {
		t.Fatalf("shared preparation must record each loaded skill once: %v", got)
	}
}

func TestRegistryPreparationFrozenWithinRun(t *testing.T) {
	m := newScriptedModel(textReply("unused"))
	defer m.srv.Close()
	l, _, _, journal := registryFixture(t, m)
	l.SetSkills(registrySkills(t, "orchard"))
	ctx, _, finish, err := l.beginRun(context.Background(), "s", "autopilot", "orchard inspection")
	if err != nil {
		t.Fatal(err)
	}
	defer finish(nil, nil)
	first, notes, err := l.prepareRunRegistry(ctx, "s", "")
	if err != nil {
		t.Fatal(err)
	}
	l.SetSkills(registrySkills(t, "harbor"))
	second, secondNotes, err := l.prepareRunRegistry(ctx, "s", "harbor")
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !reflect.DeepEqual(notes, secondNotes) {
		t.Fatalf("run capability bundle changed: first=%p second=%p notes=%v -> %v", first, second, notes, secondNotes)
	}
	if got := registryNotes(t, journal, "skill_loaded"); !reflect.DeepEqual(got, []string{"skill loaded: orchard"}) {
		t.Fatalf("cached preparation repeated/lost load evidence: %v", got)
	}
}

func TestRegistryRunBriefPolicy(t *testing.T) {
	for _, tc := range []struct{ name, original, current, want string }{
		{"chat", "", "harbor", "harbor"},
		{"whole plan", "orchard", "", "orchard"},
		{"revision", "orchard", "harbor", "orchard\nCurrent instruction: harbor"},
		{"duplicate", " orchard ", "orchard", "orchard"},
		{"empty", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := runBrief(tc.original, tc.current); got != tc.want {
				t.Fatalf("brief = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestRegistryPlanCommandsRetainOriginalSkill(t *testing.T) {
	for _, route := range []string{"whole", "selected", "chat-continue"} {
		t.Run(route, func(t *testing.T) {
			m := newScriptedModel(textReply("done"))
			defer m.srv.Close()
			l, _, session, journal := registryFixture(t, m)
			wr, err := episodic.Open(journal)
			if err != nil {
				t.Fatal(err)
			}
			defer wr.Close()
			auditAppend(t, wr, episodic.MsgUser, map[string]string{"text": "orchard task"})
			auditAppend(t, wr, episodic.Plan, auditPlan())
			auditWrite(t, filepath.Join(session, "x"), "OLD")
			l.SetSkills(registrySkills(t, "orchard"))
			switch route {
			case "whole":
				_, err = l.RunAutopilot(context.Background(), "s")
			case "selected":
				_, id, planErr := LatestPlan(journal)
				if planErr != nil {
					t.Fatal(planErr)
				}
				_, err = l.RunSelected(context.Background(), "s", id, []int{0})
			case "chat-continue":
				_, err = l.Run(context.Background(), "s", "keep going", "autopilot")
			}
			if err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			if len(m.offered) != 1 || !strings.Contains(strings.Join(m.offered[0], ","), "orchard_tool") {
				t.Fatalf("plan lost original task capability: %v", m.offered)
			}
			if got := registryNotes(t, journal, "skill_loaded"); !reflect.DeepEqual(got, []string{"skill loaded: orchard"}) {
				t.Fatalf("original skill load evidence = %v", got)
			}
		})
	}
}

func TestRegistryRegistrationFailureStopsBeforeInference(t *testing.T) {
	for _, route := range []string{"chat", "step"} {
		t.Run(route, func(t *testing.T) {
			m := newScriptedModel(textReply("should not be called"))
			defer m.srv.Close()
			l, _, _, journal := registryFixture(t, m)
			dir := t.TempDir()
			for _, name := range []string{"read", "valid-reflex"} {
				auditWrite(t, filepath.Join(dir, name+".rfx.yaml"), fmt.Sprintf("rfx: 1\nname: %s\ndescription: registration fixture\nrisk: safe\nkind: pipeline\nsteps:\n  - read: {path: input.txt}\n", name))
			}
			loader := rfx.NewLoader(dir, func(name string) bool { return name == "read" })
			if len(loader.List()) != 2 {
				t.Fatalf("fixtures rejected at load, not registration: %v", loader.Errors())
			}
			l.SetReflexes(loader)
			var runErr error
			if route == "chat" {
				_, runErr = l.Run(context.Background(), "s", "inspect capabilities", "discussion")
			} else {
				wr, err := episodic.Open(journal)
				if err != nil {
					t.Fatal(err)
				}
				auditAppend(t, wr, episodic.Plan, auditPlan())
				wr.Close()
				_, runErr = l.RunStep(context.Background(), "s", StepRunRequest{Step: 0})
			}
			if runErr == nil || !strings.Contains(runErr.Error(), "collides") {
				t.Fatalf("registration must fail closed: %v", runErr)
			}
			m.mu.Lock()
			calls := len(m.offered)
			m.mu.Unlock()
			if calls != 0 {
				t.Fatalf("inference ran with a partial registry: %d calls", calls)
			}
			if state := l.RunStateOf("s"); state == nil || state.Status != "failed" {
				t.Fatalf("missing terminal failure: %+v", state)
			}
			if got := registryNotes(t, journal, "rfx_rejected"); len(got) != 1 || !strings.Contains(got[0], `"read"`) {
				t.Fatalf("rejection evidence = %v", got)
			}
		})
	}
}
