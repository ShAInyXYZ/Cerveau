package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"cerveau/internal/cores"
)

func wakeFixtureTarget() wakeTarget {
	return wakeTarget{id: "selected", endpoint: "http://127.0.0.1:18020", unit: "selected-core.service", alternatives: []string{"optional-core.service"}}
}

func TestSelectTargetUsesConfiguredEndpointAndNeverStartCommand(t *testing.T) {
	reg := &cores.Registry{Active: "stale", Cores: []cores.Core{
		{ID: "stale", Endpoint: "http://127.0.0.1:18000", Unit: "old-core.service", Start: "sh -c 'do-not-run'"},
		{ID: "selected", Endpoint: "http://127.0.0.1:18020", Unit: "selected-core.service", Start: "touch do-not-run"},
		{ID: "alias", Endpoint: "http://127.0.0.1:18021", Unit: "selected-core.service"},
	}}
	target, err := selectTarget("http://127.0.0.1:18020", reg)
	if err != nil || target.id != "selected" || target.unit != "selected-core.service" || !reflect.DeepEqual(target.alternatives, []string{"old-core.service"}) {
		t.Fatalf("selection followed stale label or included selected-unit alias: %+v %v", target, err)
	}
	remote, err := selectTarget("https://remote.example/v1", reg)
	if err != nil || remote.id != "unmanaged" || remote.unit != "" || !reflect.DeepEqual(remote.units(), []string{"cerveau.service", "cerveau-embed.service"}) {
		t.Fatalf("unmanaged endpoint acquired a guessed model unit: %+v %v", remote, err)
	}
}

func TestSelectTargetRejectsUnsafeAndAmbiguousConfiguration(t *testing.T) {
	for _, unit := range []string{"--all.service", "../bad.service", "bad.service;touch", "bad.service\n", "bad.socket", strings.Repeat("a", 256) + ".service"} {
		if _, err := selectTarget("http://model", &cores.Registry{Cores: []cores.Core{{Endpoint: "http://model", Unit: unit}}}); err == nil {
			t.Errorf("unsafe service unit accepted: %q", unit)
		}
	}
	if _, err := selectTarget("http://model", &cores.Registry{Cores: []cores.Core{{Endpoint: "http://model"}, {Endpoint: "http://model"}}}); err == nil {
		t.Fatal("ambiguous configured endpoint accepted")
	}
	if _, err := selectTarget("", &cores.Registry{}); err == nil {
		t.Fatal("missing configured endpoint accepted")
	}
	if _, err := selectTarget("http://model", nil); err == nil {
		t.Fatal("missing registry accepted")
	}
}

func TestWakeQueuesConfiguredParkedCoreAsynchronously(t *testing.T) {
	target := wakeFixtureTarget()
	var calls [][]string
	manager := &wakeManager{load: func() (wakeTarget, error) { return target, nil }, now: time.Now,
		command: func(_ context.Context, args ...string) (string, error) {
			calls = append(calls, append([]string(nil), args...))
			if args[0] == "show" {
				return "Id=optional-core.service\nLoadState=loaded\nActiveState=inactive\n", nil
			}
			return "", nil
		}}
	if err := manager.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"show", "--property=Id,Names,LoadState,ActiveState", "--no-pager", "optional-core.service"},
		{"start", "--no-block", "cerveau.service", "cerveau-embed.service", "selected-core.service"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected wake commands: %q", calls)
	}
}

func TestWakeRefusesCompetingOrUnverifiableCore(t *testing.T) {
	for _, state := range []string{"active", "activating", "reloading", "deactivating", "unknown", ""} {
		t.Run(state, func(t *testing.T) {
			target := wakeFixtureTarget()
			manager := &wakeManager{load: func() (wakeTarget, error) { return target, nil }, now: time.Now,
				command: func(_ context.Context, args ...string) (string, error) {
					if args[0] != "show" {
						t.Fatalf("competing Core caused a start: %q", args)
					}
					return "Id=optional-core.service\nLoadState=loaded\nActiveState=" + state + "\n", nil
				}}
			if err := manager.ensure(context.Background()); err == nil {
				t.Fatalf("Core state %q allowed a competing start", state)
			}
		})
	}
}

func TestWakeDistinguishesAbsentOptionalUnitsFromUnavailableMetadata(t *testing.T) {
	for _, test := range []struct {
		name, output string
		err          error
		allowed      bool
	}{
		{"absent", "Id=optional-core.service\nLoadState=not-found\nActiveState=inactive\n", errors.New("exit status 1"), true},
		{"parked", "Id=optional-core.service\nLoadState=loaded\nActiveState=inactive\n", nil, true},
		{"failed", "Id=optional-core.service\nLoadState=loaded\nActiveState=failed\n", nil, true},
		{"alias", "Id=canonical.service\nNames=canonical.service optional-core.service\nLoadState=loaded\nActiveState=inactive\n", nil, true},
		{"bus unavailable", "Failed to connect to bus", errors.New("bus unavailable"), false},
		{"empty success", "", nil, false},
		{"wrong identity", "Id=other.service\nLoadState=not-found\nActiveState=inactive\n", nil, false},
		{"missing load state", "Id=optional-core.service\nActiveState=inactive\n", nil, false},
		{"load error", "Id=optional-core.service\nLoadState=error\nActiveState=inactive\n", nil, false},
		{"partial failure", "Id=optional-core.service\nLoadState=loaded\nActiveState=inactive\n", errors.New("command failed"), false},
		{"duplicate", "Id=optional-core.service\nLoadState=loaded\nActiveState=active\nActiveState=inactive\n", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := alternateCoreInactive("optional-core.service", test.output, test.err); (err == nil) != test.allowed {
				t.Fatalf("allowed=%t err=%v", test.allowed, err)
			}
		})
	}
}

func TestWakeRechecksProfileBeforeEnqueue(t *testing.T) {
	first, latest := wakeFixtureTarget(), wakeFixtureTarget()
	latest.endpoint, latest.unit = "http://127.0.0.1:18030", "new-core.service"
	loads, starts := 0, 0
	manager := &wakeManager{now: time.Now, load: func() (wakeTarget, error) {
		loads++
		if loads == 1 {
			return first, nil
		}
		return latest, nil
	}, command: func(_ context.Context, args ...string) (string, error) {
		if args[0] == "show" {
			return "Id=optional-core.service\nLoadState=loaded\nActiveState=inactive\n", nil
		}
		starts++
		if args[len(args)-1] != latest.unit {
			t.Fatalf("queued stale profile: %q", args)
		}
		return "", nil
	}}
	if err := manager.ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "changed") || starts != 0 {
		t.Fatalf("profile changed during preflight but starts=%d err=%v", starts, err)
	}
	if err := manager.ensure(context.Background()); err != nil || starts != 1 {
		t.Fatalf("new selection inherited old throttle failure: starts=%d err=%v", starts, err)
	}
}

func TestWakeConcurrentRequestsQueueOnce(t *testing.T) {
	target := wakeFixtureTarget()
	target.alternatives = nil
	starts := 0
	fixedNow := time.Now()
	manager := &wakeManager{load: func() (wakeTarget, error) { return target, nil }, now: func() time.Time { return fixedNow },
		command: func(context.Context, ...string) (string, error) { starts++; return "", nil }}
	var workers sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 32; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			if err := manager.ensure(context.Background()); err != nil {
				t.Errorf("concurrent wake: %v", err)
			}
		}()
	}
	close(start)
	workers.Wait()
	if starts != 1 {
		t.Fatalf("concurrent requests queued %d starts", starts)
	}
}

func TestWakeFailuresThrottleAndCanceledRequestsDoNotStart(t *testing.T) {
	target := wakeFixtureTarget()
	target.alternatives = nil
	now := time.Now()
	starts := 0
	queueErr := errors.New("systemd unavailable")
	manager := &wakeManager{load: func() (wakeTarget, error) { return target, nil }, now: func() time.Time { return now },
		command: func(context.Context, ...string) (string, error) { starts++; return "", queueErr }}
	for i := 0; i < 2; i++ {
		if err := manager.ensure(context.Background()); !errors.Is(err, queueErr) {
			t.Fatalf("wake failure hidden: %v", err)
		}
	}
	if starts != 1 {
		t.Fatal("failure retries were not throttled")
	}
	now = now.Add(6 * time.Second)
	queueErr = nil
	if err := manager.ensure(context.Background()); err != nil || starts != 2 {
		t.Fatalf("bounded retry failed: starts=%d err=%v", starts, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.ensure(ctx); !errors.Is(err, context.Canceled) || starts != 2 {
		t.Fatalf("canceled request started services: starts=%d err=%v", starts, err)
	}
}

func TestWakeThrottleBeginsAfterSlowPreflightCompletes(t *testing.T) {
	target := wakeFixtureTarget()
	target.alternatives = nil
	now := time.Now()
	starts := 0
	manager := &wakeManager{load: func() (wakeTarget, error) { return target, nil }, now: func() time.Time { return now },
		command: func(context.Context, ...string) (string, error) {
			starts++
			now = now.Add(6 * time.Second)
			return "", nil
		}}
	for i := 0; i < 2; i++ {
		if err := manager.ensure(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if starts != 1 {
		t.Fatal("slow preflight expired its own concurrency throttle")
	}
}

func TestWakeUnmanagedEndpointStartsNoModel(t *testing.T) {
	target := wakeTarget{id: "unmanaged", endpoint: "https://remote.example", alternatives: []string{"old-core.service"}}
	manager := &wakeManager{load: func() (wakeTarget, error) { return target, nil }, now: time.Now,
		command: func(_ context.Context, args ...string) (string, error) {
			if !reflect.DeepEqual(args, []string{"start", "--no-block", "cerveau.service", "cerveau-embed.service"}) {
				t.Fatalf("unmanaged endpoint started a model or executed Start: %q", args)
			}
			return "", nil
		}}
	if err := manager.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestIgniteStatusRoutesDoNotWake(t *testing.T) {
	manager := &wakeManager{load: func() (wakeTarget, error) { return wakeFixtureTarget(), nil }, now: time.Now,
		command: func(_ context.Context, args ...string) (string, error) {
			if args[0] != "is-active" {
				t.Fatalf("status route invoked wake: %q", args)
			}
			return "inactive\n", nil
		}}
	proxied := 0
	handler := igniteHandler(manager, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxied++; w.WriteHeader(http.StatusOK) }), func(context.Context) bool { return true })
	paths := []string{"/ignite/state", "/api/idle", "/api/cores", "/api/health", "/api/sessions", "/api/sessions/current/state", "/api/system/stats"}
	for _, path := range paths {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("%s %s status=%d", method, path, rec.Code)
			}
		}
	}
	if proxied != (len(paths)-1)*2 || !manager.last.IsZero() {
		t.Fatal("status routes changed wake state")
	}
}

func TestIgniteNavigationWakesAndReportsUnavailableStack(t *testing.T) {
	for _, test := range []struct {
		name  string
		ready bool
		err   error
		code  int
	}{
		{"ready", true, nil, http.StatusOK},
		{"booting", false, nil, http.StatusServiceUnavailable},
		{"queue failed", true, errors.New("queue unavailable"), http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := wakeFixtureTarget()
			target.alternatives = nil
			starts := 0
			manager := &wakeManager{load: func() (wakeTarget, error) { return target, nil }, now: time.Now,
				command: func(_ context.Context, args ...string) (string, error) {
					if args[0] != "start" || args[1] != "--no-block" {
						t.Fatalf("unexpected navigation command: %q", args)
					}
					starts++
					return "", test.err
				}}
			handler := igniteHandler(manager, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }), func(context.Context) bool { return test.ready })
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			if rec.Code != test.code || starts != 1 {
				t.Fatalf("navigation status=%d starts=%d", rec.Code, starts)
			}
		})
	}
}

type igniteTransport func(*http.Request) (*http.Response, error)

func (f igniteTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestIgniteForwardedMarkerSurvivesClientHopHeader(t *testing.T) {
	target, _ := url.Parse(coreURL)
	proxy := forwardedProxy(target)
	calls := 0
	proxy.Transport = igniteTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get(forwardedMarker) != "1" || r.Header.Get("Connection") != "" {
			t.Fatalf("client stripped/forged proxy trust marker: %v", r.Header)
		}
		if r.Header.Get("Authorization") != "Bearer caller-token" {
			t.Fatal("forwarding changed caller authentication")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})
	for _, marker := range []string{"", "0", "forged"} {
		req := httptest.NewRequest(http.MethodGet, "http://ignite.test/api/idle", nil)
		req.Header.Set("Connection", forwardedMarker)
		req.Header.Set(forwardedMarker, marker)
		req.Header.Set("Authorization", "Bearer caller-token")
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("proxy status=%d", rec.Code)
		}
	}
	if calls != 3 {
		t.Fatal("requests were not forwarded")
	}
}
