package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"cerveau/internal/guard"
)

func TestRegistryNormalizesInternalNoArguments(t *testing.T) {
	for _, args := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(" \nnull\t")} {
		var guarded, executed string
		reg := NewRegistry(Entry{Tool: &stubTool{name: "noargs", fn: func(raw json.RawMessage, mode string) (string, error) {
			executed = string(raw)
			return "ok", nil
		}}, RiskTier: RiskSafe})
		reg.SetGuard(func(name string, raw json.RawMessage) error {
			guarded = string(raw)
			return nil
		})
		if _, err := reg.ExecuteMode(context.Background(), "noargs", args, ModeAutopilot); err != nil {
			t.Fatalf("no-argument shorthand %q rejected: %v", args, err)
		}
		if guarded != "{}" || executed != "{}" {
			t.Fatalf("args %q: guard saw %q, tool saw %q; both must receive {}", args, guarded, executed)
		}
	}
}

func TestRegistryRejectsMalformedOrNonObjectArguments(t *testing.T) {
	for _, args := range []string{"", " ", "{", `{} {}`, `[]`, `"text"`, `true`, `42`} {
		t.Run(args, func(t *testing.T) {
			calls := 0
			reg := NewRegistry(Entry{Tool: &stubTool{name: "noargs", fn: func(raw json.RawMessage, mode string) (string, error) {
				calls++
				return "", nil
			}}, RiskTier: RiskSafe})
			if _, err := reg.ExecuteMode(context.Background(), "noargs", json.RawMessage(args), ModeAutopilot); err == nil {
				t.Fatalf("invalid arguments %q accepted", args)
			}
			if calls != 0 {
				t.Fatalf("invalid arguments dispatched %d times", calls)
			}
		})
	}
}

func TestGuidebookRepairCannotBypassGuardOrReuseApproval(t *testing.T) {
	for _, tier := range []string{guard.TierSensitive, guard.TierCatastrophic} {
		t.Run(tier, func(t *testing.T) {
			calls, guardCalls := 0, 0
			reg := NewRegistry(Entry{Tool: &stubTool{name: "serve", fn: func(raw json.RawMessage, mode string) (string, error) {
				calls++
				return "first port failed", errors.New("address already in use")
			}}, RiskTier: RiskSafe})
			reg.SetGuard(func(name string, raw json.RawMessage) error {
				guardCalls++
				var args struct{ Port int }
				if err := json.Unmarshal(raw, &args); err != nil {
					return err
				}
				if args.Port == 8001 {
					return &guard.TierError{Tier: tier, Reason: "new target needs review"}
				}
				return nil
			})
			out, err := reg.ExecuteMode(WithHumanApproval(context.Background()), "serve", json.RawMessage(`{"action":"start","port":8000}`), ModeAutopilot)
			if err == nil || !strings.Contains(err.Error(), "guard denied repaired call") {
				t.Fatalf("repaired target bypassed guard: out=%q, err=%v", out, err)
			}
			if calls != 1 || guardCalls != 2 || out != "first port failed" {
				t.Fatalf("calls=%d guards=%d output=%q; rejected repair must retain initial evidence without dispatch", calls, guardCalls, out)
			}
		})
	}
}

func TestGuidebookRepairHonorsRemediationFailure(t *testing.T) {
	calls, remediationCalls := 0, 0
	reg := NewRegistry(Entry{Tool: &stubTool{name: "serve", fn: func(raw json.RawMessage, mode string) (string, error) {
		calls++
		return "first port failed", errors.New("address already in use")
	}}, RiskTier: RiskSafe})
	reg.SetRemediator(func(name string, args json.RawMessage) (json.RawMessage, error) {
		remediationCalls++
		if remediationCalls == 2 {
			return nil, errors.New("cannot make repaired call safe")
		}
		return args, nil
	})
	_, err := reg.ExecuteMode(context.Background(), "serve", json.RawMessage(`{"action":"start","port":8000}`), ModeAutopilot)
	if err == nil || !strings.Contains(err.Error(), "cannot make repaired call safe") || calls != 1 || remediationCalls != 2 {
		t.Fatalf("calls=%d remediation=%d err=%v", calls, remediationCalls, err)
	}
}

func TestRemediationRechecksGuardBeforeDispatch(t *testing.T) {
	for _, tier := range []string{guard.TierSensitive, guard.TierCatastrophic} {
		t.Run(tier, func(t *testing.T) {
			calls, guardCalls, remediationCalls := 0, 0, 0
			reg := NewRegistry(Entry{Tool: &stubTool{name: "bash", fn: func(raw json.RawMessage, mode string) (string, error) {
				calls++
				return "", nil
			}}, RiskTier: RiskSafe})
			reg.SetGuard(func(name string, args json.RawMessage) error {
				guardCalls++
				if strings.Contains(string(args), "new target") {
					return &guard.TierError{Tier: tier, Reason: "rewritten target denied"}
				}
				return nil
			})
			reg.SetRemediator(func(name string, args json.RawMessage) (json.RawMessage, error) {
				remediationCalls++
				// Equal lengths deliberately exercise an in-place argument rewrite.
				copy(args, json.RawMessage(`{"command":"new target"}`))
				return args, nil
			})
			_, err := reg.ExecuteMode(WithHumanApproval(context.Background()), "bash", json.RawMessage(`{"command":"old target"}`), ModeAutopilot)
			if err == nil || !strings.Contains(err.Error(), "guard denied remediated call") || calls != 0 || guardCalls != 2 || remediationCalls != 1 {
				t.Fatalf("calls=%d guard=%d remediation=%d err=%v", calls, guardCalls, remediationCalls, err)
			}
		})
	}
}

func TestRemediationPreservesUnchangedApproval(t *testing.T) {
	calls := 0
	reg := NewRegistry(Entry{Tool: &stubTool{name: "bash", fn: func(raw json.RawMessage, mode string) (string, error) {
		calls++
		return "ok", nil
	}}, RiskTier: RiskSafe})
	reg.SetGuard(func(name string, args json.RawMessage) error {
		return &guard.TierError{Tier: guard.TierSensitive, Reason: "original action needs confirmation"}
	})
	reg.SetRemediator(func(name string, args json.RawMessage) (json.RawMessage, error) { return args, nil })
	if _, err := reg.ExecuteMode(WithHumanApproval(context.Background()), "bash", json.RawMessage(`{"command":"git push"}`), ModeAutopilot); err != nil || calls != 1 {
		t.Fatalf("unchanged approved action rejected: calls=%d err=%v", calls, err)
	}
}

func TestRemediationRejectsInvalidOutput(t *testing.T) {
	for _, output := range []string{"", "{", "null", "[]", `"text"`} {
		t.Run(output, func(t *testing.T) {
			calls := 0
			reg := NewRegistry(Entry{Tool: &stubTool{name: "noargs", fn: func(raw json.RawMessage, mode string) (string, error) {
				calls++
				return "", nil
			}}, RiskTier: RiskSafe})
			reg.SetRemediator(func(name string, args json.RawMessage) (json.RawMessage, error) { return json.RawMessage(output), nil })
			if _, err := reg.ExecuteMode(context.Background(), "noargs", json.RawMessage(`{}`), ModeAutopilot); err == nil || calls != 0 {
				t.Fatalf("invalid remediation dispatched: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestGuidebookRemediationCannotIntroduceDeniedTarget(t *testing.T) {
	calls, remediationCalls := 0, 0
	reg := NewRegistry(Entry{Tool: &stubTool{name: "serve", fn: func(raw json.RawMessage, mode string) (string, error) {
		calls++
		return "first port failed", errors.New("address already in use")
	}}, RiskTier: RiskSafe})
	reg.SetGuard(func(name string, args json.RawMessage) error {
		if strings.Contains(string(args), "9999") {
			return &guard.TierError{Tier: guard.TierCatastrophic, Reason: "denied remediated port"}
		}
		return nil
	})
	reg.SetRemediator(func(name string, args json.RawMessage) (json.RawMessage, error) {
		remediationCalls++
		if remediationCalls == 2 {
			return json.RawMessage(`{"action":"start","port":9999}`), nil
		}
		return args, nil
	})
	_, err := reg.ExecuteMode(WithHumanApproval(context.Background()), "serve", json.RawMessage(`{"action":"start","port":8000}`), ModeAutopilot)
	if err == nil || !strings.Contains(err.Error(), "guard denied remediated call") || calls != 1 || remediationCalls != 2 {
		t.Fatalf("calls=%d remediation=%d err=%v", calls, remediationCalls, err)
	}
}

func TestMvRemediationCannotIntroduceExternalDelete(t *testing.T) {
	workspace, outside := t.TempDir(), t.TempDir()
	g := guard.New(workspace)
	calls := 0
	reg := NewRegistry(Entry{Tool: &stubTool{name: "bash", fn: func(raw json.RawMessage, mode string) (string, error) {
		calls++
		return "stub only: never run shell", nil
	}}, RiskTier: RiskDangerous})
	reg.SetGuard(g.Check)
	reg.SetRemediator(func(name string, args json.RawMessage) (json.RawMessage, error) {
		return g.Remediate(name, args, time.Unix(0, 0))
	})
	args, _ := json.Marshal(map[string]string{"command": "mv " + outside + "/source.txt destination.txt"})
	_, err := reg.ExecuteMode(WithHumanApproval(context.Background()), "bash", args, ModeAutopilot)
	if err == nil || !strings.Contains(err.Error(), "guard denied remediated call") || calls != 0 {
		t.Fatalf("remediated external deletion reached dispatch: calls=%d err=%v", calls, err)
	}
}

func TestRemediationCannotChangeDiscussionWriteToCode(t *testing.T) {
	calls := 0
	reg := NewRegistry(Entry{Tool: &stubTool{name: "write", fn: func(raw json.RawMessage, mode string) (string, error) {
		calls++
		return "", nil
	}}, RiskTier: RiskSafe})
	reg.SetRemediator(func(name string, args json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"path":"main.go","content":"changed"}`), nil
	})
	_, err := reg.ExecuteMode(context.Background(), "write", json.RawMessage(`{"path":"docs/design.md","content":"draft"}`), ModeDiscussion)
	if err == nil || !strings.Contains(err.Error(), "discussion mode") || calls != 0 {
		t.Fatalf("remediated code write bypassed mode fence: calls=%d err=%v", calls, err)
	}
}
