package api

// Opt-in evidence collection against an already-running Core. This test host
// deliberately omits main's memory indexer, idle parker and lifecycle routes.
// No model request is made unless CERVEAU_RECOVERY_LIVE_RUN=1. It never resumes
// a production session: all effects belong to a fresh, retained fixture copy.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"cerveau/internal/config"
	"cerveau/internal/episodic"
	"cerveau/internal/guard"
	"cerveau/internal/llm"
	"cerveau/internal/loop"
	"cerveau/internal/plan"
	"cerveau/internal/session"
	"cerveau/internal/tools"
	"cerveau/internal/window"
)

const recoveryLiveOriginalWorkspace = "/home/shiny/Pictures/MultiRigGames/Minecraft"
const recoveryLiveOriginalSession = "/home/shiny/.crv/sessions/20260905-160857-minecraft"

// All shell calls, including harness verification and following steps, retain
// recovery's read-only filesystem + private scratch + no-network policy. Only
// the jailed structured tools may write the disposable workspace.
type recoveryLiveShell struct{ tools.Tool }

func (s recoveryLiveShell) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return s.Tool.Execute(tools.WithRecoveryShell(ctx), args)
}

func recoveryLiveRegistry(ws string) *tools.Registry {
	p := tools.NewApplyPatch()
	r := tools.NewRegistry(
		tools.Entry{Tool: tools.NewRead(ws), RiskTier: tools.RiskSafe},
		tools.Entry{Tool: tools.NewGrep(ws), RiskTier: tools.RiskSafe},
		tools.Entry{Tool: tools.NewGlob(ws), RiskTier: tools.RiskSafe},
		tools.Entry{Tool: tools.NewEdit(ws), RiskTier: tools.RiskSensitive},
		tools.Entry{Tool: tools.NewWrite(ws), RiskTier: tools.RiskSensitive},
		tools.Entry{Tool: p, RiskTier: tools.RiskSensitive},
		tools.Entry{Tool: recoveryLiveShell{tools.NewBash(ws)}, RiskTier: tools.RiskDangerous, Modes: []string{tools.ModeAutopilot}},
	)
	r.SetWorkspace(ws)
	p.SetRegistry(r)
	g := guard.New(ws)
	r.SetGuard(g.Check)
	r.SetRemediator(func(name string, args json.RawMessage) (json.RawMessage, error) {
		return g.Remediate(name, args, time.Now())
	})
	return r
}

func recoveryLiveWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}

func recoveryLiveJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	recoveryLiveWrite(t, path, append(raw, '\n'))
}

// Reject links/special files, cap retained bytes, and compare every copy. This
// function never removes or moves source data; both versions are retained.
func recoveryLiveTree(t *testing.T, root, copyTo string) map[string]string {
	t.Helper()
	out := map[string]string{}
	var total int64
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if e.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("fixture refuses symlink: %s", rel)
		}
		if e.IsDir() {
			if copyTo != "" {
				return os.MkdirAll(filepath.Join(copyTo, rel), 0700)
			}
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 64<<20 {
			return fmt.Errorf("unsupported fixture source: %s", rel)
		}
		total += info.Size()
		if total > 128<<20 {
			return fmt.Errorf("fixture exceeds 128 MiB safety cap")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = fmt.Sprintf("%x", sha256.Sum256(data))
		if copyTo != "" {
			target := filepath.Join(copyTo, rel)
			recoveryLiveWrite(t, target, data)
			copied, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(data, copied) {
				return fmt.Errorf("copy verification failed: %s", rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type recoveryLiveCoreObservation struct {
	State  string `json:"state"`
	Error  string `json:"error,omitempty"`
	Active bool   `json:"active"`
}

func recoveryLiveClassifyCore(data []byte, err error) recoveryLiveCoreObservation {
	s := string(data)
	out := recoveryLiveCoreObservation{State: s, Active: err == nil && strings.Contains(s, "ActiveState=active\n") && strings.Contains(s, "SubState=running\n") && strings.Contains(s, "MainPID=") && !strings.Contains(s, "MainPID=0\n")}
	if err != nil {
		out.Error = err.Error()
	}
	return out
}

func recoveryLiveObserveCore() recoveryLiveCoreObservation {
	data, err := exec.Command("systemctl", "--user", "show", "crv-core-vllm-27b-w8a16.service", "-p", "MainPID", "-p", "ActiveState", "-p", "SubState").Output()
	return recoveryLiveClassifyCore(data, err)
}

func recoveryLiveCoreState(t *testing.T) string {
	t.Helper()
	observation := recoveryLiveObserveCore()
	if !observation.Active {
		t.Fatal("existing W8A16 Core must already be active and running")
	}
	return observation.State
}

func recoveryLiveAppend(t *testing.T, wr *episodic.Writer, kind episodic.EventType, value any) episodic.Event {
	t.Helper()
	ev, err := wr.Append(kind, value)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func recoveryLiveSeedBaseline(t *testing.T, a *API, sid, ws, evidenceRoot string, reg *tools.Registry) int {
	t.Helper()
	// The fixture is intentionally source-shaped and larger than one read page.
	// Only line 160 is defective; prior foundation and final continuation checks
	// are independent, executable assertions rather than file-existence checks.
	var source strings.Builder
	source.WriteString("export const foundation = 'FOUNDATION_OK';\n")
	for i := 2; i < 159; i++ {
		fmt.Fprintf(&source, "// source fixture line %03d: stable unrelated context remains unchanged during regional repair.\n", i)
	}
	source.WriteString("export function double(value) {\n  return value * 2;\n}\n")
	for i := 162; i <= 340; i++ {
		fmt.Fprintf(&source, "// retained source fixture line %03d: preserve unrelated implementation context.\n", i)
	}
	good := source.String()
	recoveryLiveWrite(t, filepath.Join(ws, "world.mjs"), []byte(good))
	p := &loop.Plan{Title: "Explicitly seeded live regional-recovery baseline", Steps: []loop.PlanStep{
		{Title: "Retain the verified foundation", Files: []string{"world.mjs"}, Detail: "Preserve foundation and all unrelated source lines.", Verify: &plan.Verify{Kind: "command", Command: "node --input-type=module -e \"import {foundation} from './world.mjs'; if(foundation!=='FOUNDATION_OK') process.exit(1)\""}},
		{Title: "Repair double and verify the failed calculation", Files: []string{"world.mjs"}, Detail: "Diagnose the observed syntax error around world.mjs line 160 using a targeted read, make the smallest source correction, and run the committed check. Preserve foundation and unrelated comments. Do not rewrite this plan or weaken checks.", Verify: &plan.Verify{Kind: "command", Command: "node --input-type=module -e \"import {double} from './world.mjs'; if(double(21)!==42 || double(-3)!==-6) process.exit(1)\""}},
		{Title: "Continue with the integration marker", Files: []string{"next.md"}, Detail: "Write next.md containing exactly RECOVERY_CONTINUED followed by a newline, read it back, then stop.", Verify: &plan.Verify{Kind: "contains", File: "next.md", Symbol: "RECOVERY_CONTINUED"}},
	}}
	first := loop.RunVerify(context.Background(), reg, ws, p.Steps[0].Verify)
	if !first.Pass {
		t.Fatal("baseline foundation precondition failed; see fixture environment")
	}
	bad := strings.Replace(good, "  return value * 2;", "  return value * ;", 1)
	if err := os.WriteFile(filepath.Join(ws, "world.mjs"), []byte(bad), 0600); err != nil {
		t.Fatal(err)
	}
	failed := loop.RunVerify(context.Background(), reg, ws, p.Steps[1].Verify)
	if failed.Pass {
		t.Fatal("baseline defect must actually fail before recovery")
	}
	wr, err := a.Writer(sid)
	if err != nil {
		t.Fatal(err)
	}
	recoveryLiveAppend(t, wr, episodic.MsgUser, map[string]string{"text": "This is a harness-authored regression fixture, not the full voxel benchmark. Recover the blocked regional source defect, retain the foundation, and continue the existing plan. Use structured edits. Report checks honestly."})
	pe := recoveryLiveAppend(t, wr, episodic.Plan, p)
	recoveryLiveAppend(t, wr, episodic.ToolCall, map[string]any{"name": "write", "args": map[string]string{"path": "world.mjs", "content": good}, "fixture_seed": true})
	recoveryLiveAppend(t, wr, episodic.ToolResult, map[string]any{"name": "write", "output": "fixture setup wrote and read back this source before injecting the recorded defect", "fixture_seed": true})
	s := loop.PlanState{Title: p.Title, PlanID: pe.ID, Next: -1, Blocked: 1, RevisionTarget: -1, Steps: []loop.StepState{
		{ID: "step-1", Index: 0, Title: p.Steps[0].Title, Status: "passed", Attempts: 1, Verdict: &first},
		{ID: "step-2", Index: 1, Title: p.Steps[1].Title, Status: "blocked", Attempts: 2, Verdict: &failed, Reason: "Harness setup injected and reproduced the regional syntax defect; attempt count is seeded to exercise the real recovery command."},
		{ID: "step-3", Index: 2, Title: p.Steps[2].Title, Status: "pending"},
	}}
	recoveryLiveAppend(t, wr, episodic.PlanState, s)
	recoveryLiveJSON(t, filepath.Join(evidenceRoot, "seed.json"), map[string]any{"synthetic_seed": true, "prior_check": first, "failed_check": failed, "defect_line": 160, "source_before_defect_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(good))), "source_after_defect_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(bad))), "initial_state": s})
	return 1
}

func recoveryLiveCopySession(t *testing.T, store *session.FSStore, sid, ws, root string) int {
	t.Helper()
	original := filepath.Join(recoveryLiveOriginalSession, "events.jsonl")
	raw, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	recoveryLiveWrite(t, filepath.Join(root, "original-events.jsonl"), raw)
	// Only path/session identities are rewritten. Original plan/checks and event
	// ordering are retained; the untouched journal copy gives exact provenance.
	rewritten := bytes.ReplaceAll(raw, []byte(recoveryLiveOriginalWorkspace), []byte(ws))
	rewritten = bytes.ReplaceAll(rewritten, []byte(recoveryLiveOriginalSession), []byte(filepath.Dir(store.EventsPath(sid))))
	rewritten = bytes.ReplaceAll(rewritten, []byte(filepath.Base(recoveryLiveOriginalSession)), []byte(sid))
	if err := os.WriteFile(store.EventsPath(sid), rewritten, 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(recoveryLiveOriginalSession, "recovery")
	if _, err := os.Stat(archive); err == nil {
		recoveryLiveTree(t, archive, filepath.Join(filepath.Dir(store.EventsPath(sid)), "recovery"))
	}
	events, err := episodic.Replay(store.EventsPath(sid))
	if err != nil {
		t.Fatal(err)
	}
	s, id, err := loop.ReducePlan(events)
	if err != nil {
		t.Fatal(err)
	}
	blocked := s.Blocked()
	if blocked < 0 || blocked+1 >= len(s.Steps) {
		t.Fatal("the source session must have a blocked step and a following step; do not silently substitute a different case")
	}
	recoveryLiveJSON(t, filepath.Join(root, "seed.json"), map[string]any{"synthetic_seed": false, "original_journal_sha256": fmt.Sprintf("%x", sha256.Sum256(raw)), "rewritten_journal_sha256": fmt.Sprintf("%x", sha256.Sum256(rewritten)), "rewrites": []string{"workspace prefix", "session directory prefix", "session identity"}, "plan_event_id": id, "blocked_step_index": blocked, "original_plan_preserved": true, "initial_steps": s.Steps})
	return blocked
}

func TestRecoveryLiveRegionalBaseline(t *testing.T) { recoveryLiveAcceptance(t, "baseline") }
func TestRecoveryLiveMinecraftCopy(t *testing.T)    { recoveryLiveAcceptance(t, "minecraft-copy") }

func newRecoveryLiveProbe(t *testing.T, endpoint string) *labrigModelProbe {
	t.Helper()
	// A large repair may legitimately generate for more than 110s. Preserve the
	// production client's 10-minute limit; the case's 600s cancellation remains
	// the hard outer bound, also covering tokenization and verification.
	return newLABRIGModelProbeWithTimeout(t, endpoint, 10*time.Minute)
}

func TestRecoveryLiveProbeTimeoutMatchesProduction(t *testing.T) {
	// No upstream requests: inspect the actual clients that will make them.
	recovery := newRecoveryLiveProbe(t, "http://127.0.0.1:1")
	defer recovery.server.Close()
	if recovery.client.Timeout != 10*time.Minute {
		t.Fatal("live recovery proxy must not impose a shorter per-request limit than production")
	}
	legacy := newLABRIGModelProbe(t, "http://127.0.0.1:1")
	defer legacy.server.Close()
	if legacy.client.Timeout != 110*time.Second {
		t.Fatal("short lifecycle smoke fixtures must retain their existing timeout")
	}
}

func TestRecoveryLiveInactiveCoreObservationIsRetained(t *testing.T) {
	for _, observation := range []recoveryLiveCoreObservation{
		recoveryLiveClassifyCore([]byte("MainPID=0\nActiveState=inactive\nSubState=dead\n"), nil),
		recoveryLiveClassifyCore([]byte("MainPID=123\nActiveState=activating\nSubState=start-post\n"), nil),
		recoveryLiveClassifyCore(nil, fmt.Errorf("exit status 1")),
	} {
		if observation.Active {
			t.Fatal("inactive or unreadable Core cannot be certified unchanged")
		}
		path := filepath.Join(t.TempDir(), "protection.json")
		recoveryLiveJSON(t, path, map[string]any{"unchanged": false, "core_after": observation})
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Contains(data, []byte(`"unchanged": false`)) {
			t.Fatal("Core mismatch evidence must survive a failed protection check")
		}
	}
}

func recoveryLiveAcceptance(t *testing.T, name string) {
	if os.Getenv("CERVEAU_RECOVERY_LIVE_PREPARE") != "1" && os.Getenv("CERVEAU_RECOVERY_LIVE_RUN") != "1" {
		t.Skip("opt-in only: prepare or run isolated recovery acceptance")
	}
	parent := os.Getenv("CERVEAU_RECOVERY_LIVE_ROOT")
	if !filepath.IsAbs(parent) {
		t.Fatal("CERVEAU_RECOVERY_LIVE_ROOT must be an existing absolute evidence directory")
	}
	parent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, name)
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal("refusing to reuse evidence case directory")
	}
	ws := filepath.Join(root, "workspace")
	if err := os.Mkdir(ws, 0700); err != nil {
		t.Fatal(err)
	}
	coreBefore := recoveryLiveCoreState(t)
	originalBefore := recoveryLiveTree(t, recoveryLiveOriginalWorkspace, "")
	originalJournal, err := os.ReadFile(filepath.Join(recoveryLiveOriginalSession, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	originalJournalSHA := sha256.Sum256(originalJournal)
	defer func() {
		after := recoveryLiveTree(t, recoveryLiveOriginalWorkspace, "")
		journal, err := os.ReadFile(filepath.Join(recoveryLiveOriginalSession, "events.jsonl"))
		coreAfter := recoveryLiveObserveCore()
		unchanged := reflect.DeepEqual(originalBefore, after) && err == nil && originalJournalSHA == sha256.Sum256(journal) && coreAfter.Active && coreAfter.State == coreBefore
		recoveryLiveJSON(t, filepath.Join(root, "protection.json"), map[string]any{"unchanged": unchanged, "original_workspace_before": originalBefore, "original_workspace_after": after, "original_journal_before_sha256": fmt.Sprintf("%x", originalJournalSHA), "original_journal_after_sha256": fmt.Sprintf("%x", sha256.Sum256(journal)), "core_before": coreBefore, "core_after": coreAfter.State, "core_observation": coreAfter})
		if !unchanged {
			t.Error("protected source/Core identity changed; inspect protection.json, do not install")
		}
	}()
	if name == "minecraft-copy" {
		copied := recoveryLiveTree(t, recoveryLiveOriginalWorkspace, ws)
		if !reflect.DeepEqual(copied, originalBefore) {
			t.Fatal("source changed while copying")
		}
	}
	store, err := session.NewFSStore(filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	meta, err := store.CreateInWorkspace("Live recovery "+name, ws)
	if err != nil {
		t.Fatal(err)
	}
	modelCtx, err := strconv.Atoi(os.Getenv("CERVEAU_RECOVERY_MODEL_CTX"))
	if err != nil || modelCtx <= 0 {
		t.Fatal("CERVEAU_RECOVERY_MODEL_CTX must be the positive configured production context size")
	}
	cfg := &config.Config{Project: config.ProjectName, Workspace: ws, SessionsDir: filepath.Join(root, "sessions"), ModelCtx: modelCtx, ThinkingMode: "autopilot", ThinkingEffort: "medium", Sampling: "default"}
	a := New(cfg, store)
	reg := recoveryLiveRegistry(ws)
	var blocked int
	if name == "baseline" {
		blocked = recoveryLiveSeedBaseline(t, a, meta.ID, ws, root, reg)
	} else {
		blocked = recoveryLiveCopySession(t, store, meta.ID, ws, root)
	}
	identity := map[string]any{"fixture": name, "workspace": ws, "session_id": meta.ID, "source_build": BinaryBuildInfo(), "settings": map[string]any{"thinking_mode": "autopilot", "thinking_effort": "medium", "sampling": "default", "configured_model_ctx": cfg.ModelCtx}, "packing": "production window manager, 2048 reserve, existing Core tokenizer and read-only context probe; only initialized in live mode", "tool_scope": "real read/grep/glob/edit/write/apply_patch; bash always read-only, no network; no browser/RFX/lifecycle tools", "live_model_requested": os.Getenv("CERVEAU_RECOVERY_LIVE_RUN") == "1", "boundary": "not the full voxel acceptance benchmark; original plan checks unchanged in copied case"}
	recoveryLiveJSON(t, filepath.Join(root, "identity.json"), identity)
	if os.Getenv("CERVEAU_RECOVERY_LIVE_RUN") != "1" {
		t.Logf("PREPARED ONLY, no model requests: %s", root)
		return
	}
	endpoint := os.Getenv("CERVEAU_ACCEPTANCE_MODEL_URL")
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		t.Fatal("Core endpoint must be the existing local HTTP endpoint without credentials/query")
	}
	probe := newRecoveryLiveProbe(t, endpoint)
	// Match main's packing/admission path. Tokenization and serving-window
	// probes are read-only calls to the same Core, not synthetic model replies.
	win := window.NewManager(cfg.ModelCtx, 2048, window.NewHTTPCounter(endpoint))
	a.SetContextFunc(win.Budget)
	a.SetContextSync(win.Sync)
	l := loop.New(llm.NewClient(probe.server.URL), reg, a.Writer, store.EventsPath, win)
	l.SetWorkspaceFunc(func(sid string) string {
		if sid != meta.ID {
			return ""
		}
		return ws
	})
	l.SetThinking("autopilot", "medium")
	l.SetSampling("default")
	a.SetLoop(l)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions/{id}/commands", a.Command)
	mux.HandleFunc("GET /api/sessions/{id}/state", a.SessionState)
	mux.HandleFunc("POST /api/sessions/{id}/kill", a.Kill)
	host := httptest.NewServer(mux)
	started := time.Now()
	evidence := map[string]any{"fixture": name, "verified": false, "started": started.UTC(), "host": host.URL, "limits": map[string]any{"seconds": 600, "model_calls": 32, "completion_tokens": 70000}, "recovery_target": blocked}
	defer func() {
		l.Kill(meta.ID)
		until := time.Now().Add(5 * time.Second)
		for len(l.RunningSessions()) > 0 && time.Now().Before(until) {
			time.Sleep(20 * time.Millisecond)
		}
		l.WaitBackground(time.Second)
		host.Close()
		probe.server.Close()
		if snap, err := l.Snapshot(meta.ID); err == nil {
			recoveryLiveJSON(t, filepath.Join(root, "final-snapshot.json"), snap)
		}
		evidence["elapsed_ms"] = time.Since(started).Milliseconds()
		evidence["model_requests"] = len(probe.observations())
		evidence["effective_model_ctx"] = win.Budget()
		evidence["production_window_manager"] = true
		recoveryLiveJSON(t, filepath.Join(root, "requests.json"), probe.observations())
		recoveryLiveJSON(t, filepath.Join(root, "evidence.json"), evidence)
		a.wmu.Lock()
		for _, wr := range a.writers {
			_ = wr.Close()
		}
		a.wmu.Unlock()
		t.Logf("live recovery evidence retained: %s", root)
	}()
	p, planID, err := loop.LatestPlan(store.EventsPath(meta.ID))
	if err != nil {
		t.Fatal(err)
	}
	initial, err := l.Snapshot(meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	startCursor := len(initial.Events)
	cmd := loop.Command{ID: fmt.Sprintf("qa-recovery-%d", time.Now().UnixNano()), Kind: "step", Step: blocked, Continue: true, PlanID: planID, Sampling: "default", Reason: "Recover the recorded failed check and continue the existing plan."}
	recoveryLiveJSON(t, filepath.Join(root, "command.json"), cmd)
	raw, _ := json.Marshal(cmd)
	response, err := http.Post(host.URL+"/api/sessions/"+meta.ID+"/commands", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal("isolated recovery command transport failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		evidence["http_status"] = response.StatusCode
		t.Fatal("isolated recovery command not accepted; inspect retained snapshot")
	}
	var accepted struct {
		Run *loop.RunState `json:"run"`
	}
	if err := json.NewDecoder(response.Body).Decode(&accepted); err != nil || accepted.Run == nil {
		t.Fatal("missing accepted run identity")
	}
	evidence["run_id"] = accepted.Run.ID
	deadline := started.Add(600 * time.Second)
	for time.Now().Before(deadline) {
		snap, err := l.Snapshot(meta.ID)
		if err != nil {
			t.Fatal(err)
		}
		calls, completion := len(probe.observations()), 0
		for _, request := range probe.observations() {
			completion += request.Usage.CompletionTokens
		}
		if calls >= 32 || completion >= 70000 {
			evidence["stopped_by"] = "fixture model-call/completion-token budget"
			break
		}
		state, err := l.PlanStateOf(meta.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(state.Steps) != len(p.Steps) {
			evidence["stopped_by"] = "plan changed during fixture"
			break
		}
		if state.Steps[blocked+1].Status == "passed" {
			// The real project case deliberately stops after the following step,
			// not after finishing its browser/game benchmark. Preserve that distinction.
			if name == "minecraft-copy" {
				l.Kill(meta.ID)
				evidence["stopped_by"] = "fixture scope complete after following step"
			}
		}
		if snap.Run != nil && snap.Run.ID == accepted.Run.ID && !snap.Running {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if time.Now().After(deadline) {
		evidence["stopped_by"] = "fixture wall-clock budget"
	}
	l.Kill(meta.ID)
	state, err := l.PlanStateOf(meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := l.Snapshot(meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	rechecks := map[int]int{}
	for _, event := range snap.Events[startCursor:] {
		var payload struct {
			Name  string
			Kind  string
			Index int
		}
		_ = json.Unmarshal(event.Payload, &payload)
		if event.Type == episodic.ToolCall {
			counts[payload.Name]++
		}
		if event.Type == episodic.Note && payload.Kind == "verify_finished" {
			rechecks[payload.Index]++
		}
	}
	allEarlierPassed := true
	for i := 0; i < blocked; i++ {
		allEarlierPassed = allEarlierPassed && state.Steps[i].Status == "passed" && rechecks[i] > 0
	}
	verified := state.Steps[blocked].Status == "passed" && state.Steps[blocked+1].Status == "passed" && allEarlierPassed && counts["edit"]+counts["write"]+counts["apply_patch"] > 0
	evidence["verified"], evidence["steps"], evidence["tool_calls"], evidence["verification_events"] = verified, state.Steps, counts, rechecks
	if !verified {
		t.Error("live recovery acceptance not verified; see retained evidence, do not claim the full benchmark passed")
	}
}
